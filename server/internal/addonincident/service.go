package addonincident

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	maximumIncidentsPerProfile = 500
	retention                  = 90 * 24 * time.Hour
	maximumEventsPerIncident   = 100
)

type Service struct {
	pool       *pgxpool.Pool
	now        func() time.Time
	mu         sync.Mutex
	quietUntil map[string]time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: func() time.Time { return time.Now().UTC() }, quietUntil: make(map[string]time.Time)}
}

// RecordFailure records only a closed classification. It deliberately accepts
// no upstream error or request data so URLs, credentials, queries and bodies
// cannot enter the incident store.
func (service *Service) RecordFailure(ctx context.Context, profileID, addonID, addonName, code string) error {
	if !validUUID(profileID) || !validUUID(addonID) || !validFailureCode(code) {
		return ErrInvalid
	}
	addonName = strings.TrimSpace(addonName)
	if addonName == "" {
		addonName = "Extension"
	}
	addonName = truncateRunes(addonName, 200)
	now := service.now()
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin addon incident failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, profileID+":"+addonID); err != nil {
		return fmt.Errorf("lock addon incident stream: %w", err)
	}
	var incidentID string
	var created bool
	err = tx.QueryRow(ctx, `
		WITH existing AS (
			SELECT id FROM addon_incidents
			WHERE profile_id=$1::uuid AND addon_id=$2::uuid AND code=$4 AND state IN ('open','recovering')
			FOR UPDATE
		), updated AS (
			UPDATE addon_incidents incident
			SET addon_name=$3, state='open', occurrence_count=occurrence_count+1,
			    consecutive_successes=0, last_occurred_at=$5, recovery_started_at=NULL,
			    resolved_at=NULL, updated_at=$5
			FROM existing WHERE incident.id=existing.id
			RETURNING incident.id::text, false
		), inserted AS (
			INSERT INTO addon_incidents (profile_id,addon_id,addon_name,code,state,first_occurred_at,last_occurred_at,updated_at)
			SELECT $1::uuid,$2::uuid,$3,$4,'open',$5,$5,$5 WHERE NOT EXISTS (SELECT 1 FROM existing)
			RETURNING id::text, true
		)
		SELECT * FROM updated UNION ALL SELECT * FROM inserted
	`, profileID, addonID, addonName, code, now).Scan(&incidentID, &created)
	if err != nil {
		return fmt.Errorf("aggregate addon incident failure: %w", err)
	}
	eventType := "occurred"
	if created {
		eventType = "opened"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO addon_incident_events (incident_id,event_type,code,occurred_at) VALUES ($1::uuid,$2,$3,$4)`, incidentID, eventType, code, now); err != nil {
		return fmt.Errorf("record addon incident event: %w", err)
	}
	if err := service.pruneTx(ctx, tx, profileID, incidentID, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit addon incident failure: %w", err)
	}
	service.clearQuiet(profileID, addonID)
	return nil
}

func (service *Service) RecordSuccess(ctx context.Context, profileID, addonID string) error {
	if !validUUID(profileID) || !validUUID(addonID) {
		return ErrInvalid
	}
	if service.isQuiet(profileID, addonID) {
		return nil
	}
	now := service.now()
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin addon incident success: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, profileID+":"+addonID); err != nil {
		return fmt.Errorf("lock addon incident stream: %w", err)
	}
	rows, err := tx.Query(ctx, `
		UPDATE addon_incidents
		SET state=CASE state WHEN 'open' THEN 'recovering' ELSE 'resolved' END,
		    consecutive_successes=CASE state WHEN 'open' THEN 1 ELSE 2 END,
		    last_success_at=$3,
		    recovery_started_at=CASE state WHEN 'open' THEN $3 ELSE recovery_started_at END,
		    resolved_at=CASE state WHEN 'recovering' THEN $3 ELSE NULL END,
		    updated_at=$3
		WHERE profile_id=$1::uuid AND addon_id=$2::uuid AND state IN ('open','recovering')
		RETURNING id::text, code, state
	`, profileID, addonID, now)
	if err != nil {
		return fmt.Errorf("advance addon incident recovery: %w", err)
	}
	type transition struct{ id, code, state string }
	transitions := make([]transition, 0)
	for rows.Next() {
		var value transition
		if err := rows.Scan(&value.id, &value.code, &value.state); err != nil {
			rows.Close()
			return fmt.Errorf("scan addon incident recovery: %w", err)
		}
		transitions = append(transitions, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate addon incident recovery: %w", err)
	}
	rows.Close()
	allResolved := true
	for _, transition := range transitions {
		if _, err := tx.Exec(ctx, `INSERT INTO addon_incident_events (incident_id,event_type,code,occurred_at) VALUES ($1::uuid,$2,$3,$4)`, transition.id, transition.state, transition.code, now); err != nil {
			return fmt.Errorf("record addon incident recovery event: %w", err)
		}
		if err := service.pruneTx(ctx, tx, profileID, transition.id, now); err != nil {
			return err
		}
		allResolved = allResolved && transition.state == StateResolved
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit addon incident success: %w", err)
	}
	if allResolved {
		service.markQuiet(profileID, addonID, now.Add(30*time.Second))
	}
	return nil
}

func (service *Service) List(ctx context.Context, principal auth.Principal) (List, error) {
	tx, profileID, err := service.beginAuthorizedProfile(ctx, principal)
	if err != nil {
		return List{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, incidentSelect+`
		WHERE profile_id=$1::uuid
		ORDER BY CASE state WHEN 'open' THEN 0 WHEN 'recovering' THEN 1 ELSE 2 END, updated_at DESC, id DESC
		LIMIT $2`, profileID, maximumIncidentsPerProfile)
	if err != nil {
		return List{}, fmt.Errorf("list addon incidents: %w", err)
	}
	incidents := make([]Incident, 0)
	for rows.Next() {
		incident, err := scanIncident(rows)
		if err != nil {
			rows.Close()
			return List{}, fmt.Errorf("scan addon incident: %w", err)
		}
		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return List{}, fmt.Errorf("iterate addon incidents: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return List{}, fmt.Errorf("commit addon incident list: %w", err)
	}
	return List{Incidents: incidents}, nil
}

func (service *Service) Detail(ctx context.Context, principal auth.Principal, incidentID string) (Detail, error) {
	if !validUUID(incidentID) {
		return Detail{}, ErrNotFound
	}
	tx, profileID, err := service.beginAuthorizedProfile(ctx, principal)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	incident, err := scanIncident(tx.QueryRow(ctx, incidentSelect+` WHERE id=$1::uuid AND profile_id=$2::uuid`, incidentID, profileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("query addon incident: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT id,event_type,code,occurred_at FROM addon_incident_events WHERE incident_id=$1::uuid ORDER BY occurred_at DESC,id DESC LIMIT $2`, incidentID, maximumEventsPerIncident)
	if err != nil {
		return Detail{}, fmt.Errorf("list addon incident events: %w", err)
	}
	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Type, &event.Code, &event.OccurredAt); err != nil {
			rows.Close()
			return Detail{}, fmt.Errorf("scan addon incident event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Detail{}, fmt.Errorf("iterate addon incident events: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return Detail{}, fmt.Errorf("commit addon incident detail: %w", err)
	}
	return Detail{Incident: incident, Events: events}, nil
}

func (service *Service) Acknowledge(ctx context.Context, principal auth.Principal, incidentID string) (Incident, error) {
	if !validUUID(incidentID) {
		return Incident{}, ErrNotFound
	}
	tx, profileID, err := service.beginAuthorizedProfile(ctx, principal)
	if err != nil {
		return Incident{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := service.now()
	incident, err := scanIncident(tx.QueryRow(ctx, incidentSelect+`
		WHERE id=$1::uuid AND profile_id=$2::uuid FOR UPDATE`, incidentID, profileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, fmt.Errorf("lock addon incident acknowledgement: %w", err)
	}
	if incident.AcknowledgedAt == nil {
		if _, err := tx.Exec(ctx, `UPDATE addon_incidents SET acknowledged_at=$2,acknowledged_by_user_id=$3::uuid,updated_at=$2 WHERE id=$1::uuid`, incidentID, now, principal.UserID); err != nil {
			return Incident{}, fmt.Errorf("acknowledge addon incident: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO addon_incident_events (incident_id,event_type,code,occurred_at) VALUES ($1::uuid,'acknowledged',$2,$3)`, incidentID, incident.Code, now); err != nil {
			return Incident{}, fmt.Errorf("record addon incident acknowledgement: %w", err)
		}
		incident.AcknowledgedAt = &now
		acknowledgedBy := principal.UserID
		incident.AcknowledgedByUserID = &acknowledgedBy
		incident.UpdatedAt = now
	}
	if err := tx.Commit(ctx); err != nil {
		return Incident{}, fmt.Errorf("commit addon incident acknowledgement: %w", err)
	}
	return incident, nil
}

func (service *Service) beginAuthorizedProfile(ctx context.Context, principal auth.Principal) (pgx.Tx, string, error) {
	if principal.ActiveProfileID == nil || !principal.ActiveProfileCanManage {
		return nil, "", ErrForbidden
	}
	profileID := strings.TrimSpace(*principal.ActiveProfileID)
	if !validUUID(profileID) {
		return nil, "", ErrForbidden
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin addon incident authorization: %w", err)
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", fmt.Errorf("authorize addon incident profile: %w", err)
	}
	if !authorized {
		_ = tx.Rollback(ctx)
		return nil, "", ErrForbidden
	}
	selected, err := auth.LockActiveProfileSelection(ctx, tx, principal)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", fmt.Errorf("lock addon incident profile selection: %w", err)
	}
	if !selected {
		_ = tx.Rollback(ctx)
		return nil, "", ErrForbidden
	}
	return tx, profileID, nil
}

func (service *Service) pruneTx(ctx context.Context, tx pgx.Tx, profileID, incidentID string, now time.Time) error {
	if _, err := tx.Exec(ctx, `DELETE FROM addon_incident_events WHERE incident_id=$1::uuid AND id NOT IN (SELECT id FROM addon_incident_events WHERE incident_id=$1::uuid ORDER BY occurred_at DESC,id DESC LIMIT $2)`, incidentID, maximumEventsPerIncident); err != nil {
		return fmt.Errorf("prune addon incident events: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM addon_incidents WHERE profile_id=$1::uuid AND state='resolved' AND resolved_at < $2`, profileID, now.Add(-retention)); err != nil {
		return fmt.Errorf("expire addon incidents: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM addon_incidents WHERE id IN (SELECT id FROM addon_incidents WHERE profile_id=$1::uuid AND state='resolved' ORDER BY updated_at DESC,id DESC OFFSET $2)`, profileID, maximumIncidentsPerProfile); err != nil {
		return fmt.Errorf("bound addon incident history: %w", err)
	}
	return nil
}

func (service *Service) isQuiet(profileID, addonID string) bool {
	key := profileID + ":" + addonID
	service.mu.Lock()
	defer service.mu.Unlock()
	until, exists := service.quietUntil[key]
	if exists && until.After(service.now()) {
		return true
	}
	delete(service.quietUntil, key)
	return false
}

func (service *Service) markQuiet(profileID, addonID string, until time.Time) {
	service.mu.Lock()
	if len(service.quietUntil) >= 4096 {
		clear(service.quietUntil)
	}
	service.quietUntil[profileID+":"+addonID] = until
	service.mu.Unlock()
}

func (service *Service) clearQuiet(profileID, addonID string) {
	service.mu.Lock()
	delete(service.quietUntil, profileID+":"+addonID)
	service.mu.Unlock()
}

const incidentSelect = `SELECT id::text,profile_id::text,addon_id::text,addon_name,code,state,occurrence_count,first_occurred_at,last_occurred_at,last_success_at,recovery_started_at,resolved_at,acknowledged_at,acknowledged_by_user_id::text,updated_at FROM addon_incidents `

type rowScanner interface{ Scan(...any) error }

func scanIncident(row rowScanner) (Incident, error) {
	var incident Incident
	err := row.Scan(&incident.ID, &incident.ProfileID, &incident.AddonID, &incident.AddonName, &incident.Code, &incident.State, &incident.OccurrenceCount, &incident.FirstOccurredAt, &incident.LastOccurredAt, &incident.LastSuccessAt, &incident.RecoveryStartedAt, &incident.ResolvedAt, &incident.AcknowledgedAt, &incident.AcknowledgedByUserID, &incident.UpdatedAt)
	incident.Impact = impactFor(incident.Code)
	return incident, err
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' { return false }
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) { return false }
	}
	return true
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) > maximum { runes = runes[:maximum] }
	return string(runes)
}
