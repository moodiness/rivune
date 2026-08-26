package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	maximumFailoverCandidates       = 8
	maximumFailoverAttempts         = 3
	failoverTTL                     = 5 * time.Hour
	maximumFailoversPerAuthSession  = 16
	failoverCleanupBatchSize        = 500
	failoverCooldown                = 5 * time.Minute
)

var (
	ErrFailoverNotFound   = errors.New("playback source failover not found")
	ErrFailoverConflict   = errors.New("playback source failover revision conflict")
	ErrFailoverIneligible = errors.New("playback error is not eligible for source failover")
)

type FailoverError string

const (
	FailoverSourceFailed  FailoverError = "source_failed"
	FailoverSourceTimeout FailoverError = "source_timeout"
	FailoverEndedEarly    FailoverError = "ended_early"
	FailoverDecodeFailed  FailoverError = "decode_failed"
	FailoverAccessDenied  FailoverError = "access_denied"
	FailoverUserCancelled FailoverError = "user_cancelled"
)

type CreateFailoverInput struct {
	CandidateSourceRefs []string `json:"candidateSourceRefs"`
	SelectedSourceRef   string   `json:"selectedSourceRef"`
	MaximumAttempts     int      `json:"maximumAttempts,omitempty"`
}

type AdvanceFailoverInput struct {
	Error            FailoverError `json:"error"`
	PositionSeconds  float64       `json:"positionSeconds"`
	ExpectedRevision int64         `json:"expectedRevision"`
}

type FailoverCandidateHealth struct {
	Position      int        `json:"position"`
	Status        string     `json:"status"`
	CooldownUntil *time.Time `json:"cooldownUntil,omitempty"`
}

type FailoverState struct {
	ID               string                    `json:"id"`
	CurrentSourceRef string                    `json:"currentSourceRef,omitempty"`
	CurrentPosition  int                       `json:"currentPosition"`
	PositionSeconds  float64                   `json:"positionSeconds"`
	AttemptCount     int                       `json:"attemptCount"`
	MaximumAttempts  int                       `json:"maximumAttempts"`
	Revision         int64                     `json:"revision"`
	Status           string                    `json:"status"`
	LastError        FailoverError             `json:"lastError,omitempty"`
	Explanation      string                    `json:"explanation,omitempty"`
	CandidateHealth  []FailoverCandidateHealth `json:"candidateHealth"`
	ExpiresAt        time.Time                 `json:"expiresAt"`
}

type failoverRecord struct {
	ID               string
	Candidates       []string
	FailedCandidates []string
	CurrentSourceRef string
	PositionSeconds  float64
	AttemptCount     int
	MaximumAttempts  int
	Revision         int64
	Status           string
	LastError        FailoverError
	UpdatedAt        time.Time
	ExpiresAt        time.Time
}

func (service *Service) CreateFailover(ctx context.Context, principal auth.Principal, input CreateFailoverInput) (FailoverState, error) {
	input.SelectedSourceRef = strings.TrimSpace(input.SelectedSourceRef)
	if len(input.CandidateSourceRefs) < 2 || len(input.CandidateSourceRefs) > maximumFailoverCandidates || input.MaximumAttempts < 0 || input.MaximumAttempts > maximumFailoverAttempts {
		return FailoverState{}, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(input.CandidateSourceRefs))
	candidates := make([]string, len(input.CandidateSourceRefs))
	createdAt := service.now()
	expiresAt := createdAt.Add(failoverTTL)
	var resourceID, mediaType string
	for index, raw := range input.CandidateSourceRefs {
		candidate := strings.TrimSpace(raw)
		if len(candidate) < 16 || len(candidate) > 128 {
			return FailoverState{}, ErrInvalidInput
		}
		if _, duplicate := seen[candidate]; duplicate {
			return FailoverState{}, ErrInvalidInput
		}
		seen[candidate] = struct{}{}
		reference, err := service.references.get(candidate, principal)
		if err != nil {
			return FailoverState{}, err
		}
		if index == 0 {
			resourceID, mediaType = reference.ResourceID, reference.MediaType
		} else if reference.ResourceID != resourceID || reference.MediaType != mediaType {
			return FailoverState{}, ErrInvalidInput
		}
		expiresAt = earlierFailoverExpiration(expiresAt, reference.ExpiresAt)
		candidates[index] = candidate
	}
	if input.SelectedSourceRef != candidates[0] {
		return FailoverState{}, ErrInvalidInput
	}
	maximumAttempts := input.MaximumAttempts
	if maximumAttempts == 0 || maximumAttempts > len(candidates)-1 {
		maximumAttempts = min(maximumFailoverAttempts, len(candidates)-1)
	}
	encodedCandidates, err := json.Marshal(candidates)
	if err != nil {
		return FailoverState{}, fmt.Errorf("encode playback failover candidates: %w", err)
	}
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return FailoverState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('playback_source_failover'), hashtext($1::text))`, principal.SessionID); err != nil {
		return FailoverState{}, fmt.Errorf("lock playback source failover admission: %w", err)
	}
	if _, err := service.cleanupFailoversBatch(ctx, tx); err != nil {
		return FailoverState{}, err
	}
	var activeCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM playback_source_failovers
		WHERE auth_session_id = $1::uuid AND expires_at > now() AND status = 'active' AND created_at >= $2
	`, principal.SessionID, service.failoverStartedAt).Scan(&activeCount); err != nil {
		return FailoverState{}, fmt.Errorf("count active playback source failovers: %w", err)
	}
	if activeCount >= maximumFailoversPerAuthSession {
		return FailoverState{}, ErrMediaCapacityReached
	}
	var record failoverRecord
	err = tx.QueryRow(ctx, `
		INSERT INTO playback_source_failovers (
			auth_session_id, profile_id, candidate_refs, current_source_ref, max_attempts, expires_at, created_at, updated_at
		) VALUES ($1, $2, $3::jsonb, $4, $5, $6, $7, $7)
		RETURNING id::text, position_seconds, attempt_count, revision, status, updated_at, expires_at
	`, principal.SessionID, *principal.ActiveProfileID, encodedCandidates, candidates[0], maximumAttempts, expiresAt, createdAt).Scan(
		&record.ID, &record.PositionSeconds, &record.AttemptCount, &record.Revision, &record.Status, &record.UpdatedAt, &record.ExpiresAt,
	)
	if err != nil {
		return FailoverState{}, fmt.Errorf("store playback source failover: %w", err)
	}
	record.Candidates = candidates
	record.CurrentSourceRef = candidates[0]
	record.MaximumAttempts = maximumAttempts
	if err := tx.Commit(ctx); err != nil {
		return FailoverState{}, fmt.Errorf("commit playback source failover: %w", err)
	}
	return projectFailover(record), nil
}
func earlierFailoverExpiration(current, candidate time.Time) time.Time {
	if candidate.Before(current) {
		return candidate
	}
	return current
}

type failoverCleanupQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (service *Service) cleanupFailoversBatch(ctx context.Context, queryer failoverCleanupQueryer) (int, error) {
	if queryer == nil {
		return 0, nil
	}
	var removed int
	err := queryer.QueryRow(ctx, `
		WITH expired_candidates AS MATERIALIZED (
			SELECT id, 0 AS priority, expires_at AS order_at
			FROM playback_source_failovers
			WHERE expires_at <= now()
			ORDER BY expires_at, id
			LIMIT $1 FOR UPDATE SKIP LOCKED
		), terminal_candidates AS MATERIALIZED (
			SELECT stored.id, 1 AS priority, stored.updated_at AS order_at
			FROM playback_source_failovers stored
			WHERE stored.status IN ('exhausted', 'cancelled')
			  AND NOT EXISTS (SELECT 1 FROM expired_candidates expired WHERE expired.id = stored.id)
			ORDER BY stored.updated_at, stored.id
			LIMIT GREATEST($1 - (SELECT count(*) FROM expired_candidates), 0) FOR UPDATE SKIP LOCKED
		), stale_candidates AS MATERIALIZED (
			SELECT stored.id, 2 AS priority, stored.created_at AS order_at
			FROM playback_source_failovers stored
			WHERE stored.status = 'active' AND stored.created_at < $2
			  AND NOT EXISTS (SELECT 1 FROM expired_candidates expired WHERE expired.id = stored.id)
			ORDER BY stored.created_at, stored.id
			LIMIT GREATEST($1 - (SELECT count(*) FROM expired_candidates) - (SELECT count(*) FROM terminal_candidates), 0) FOR UPDATE SKIP LOCKED
		), candidates AS MATERIALIZED (
			SELECT id FROM (
				SELECT * FROM expired_candidates
				UNION ALL SELECT * FROM terminal_candidates
				UNION ALL SELECT * FROM stale_candidates
			) ranked
			ORDER BY priority, order_at, id
			LIMIT $1
		), deleted AS (
			DELETE FROM playback_source_failovers stored
			USING candidates
			WHERE stored.id = candidates.id
			RETURNING 1
		)
		SELECT count(*) FROM deleted
	`, failoverCleanupBatchSize, service.failoverStartedAt).Scan(&removed)
	if err != nil {
		return 0, fmt.Errorf("clean playback source failovers batch: %w", err)
	}
	return removed, nil
}

func (service *Service) cleanupPersistedFailovers(ctx context.Context) error {
	if service.pool == nil {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		removed, err := service.cleanupFailoversBatch(ctx, service.pool)
		if err != nil {
			return err
		}
		if removed < failoverCleanupBatchSize {
			return nil
		}
	}
}

func (service *Service) Failover(ctx context.Context, principal auth.Principal, identifier string) (FailoverState, error) {
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return FailoverState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := service.loadFailover(ctx, tx, principal, strings.TrimSpace(identifier), false)
	if err != nil {
		return FailoverState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FailoverState{}, fmt.Errorf("commit playback source failover read: %w", err)
	}
	return projectFailover(record), nil
}

func (service *Service) AdvanceFailover(ctx context.Context, principal auth.Principal, identifier string, input AdvanceFailoverInput) (FailoverState, error) {
	if !validFailoverError(input.Error) || input.PositionSeconds < 0 || input.PositionSeconds > 86400 || input.ExpectedRevision < 1 {
		return FailoverState{}, ErrInvalidInput
	}
	if !eligibleFailoverError(input.Error) {
		return FailoverState{}, ErrFailoverIneligible
	}
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return FailoverState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := service.loadFailover(ctx, tx, principal, strings.TrimSpace(identifier), true)
	if err != nil {
		return FailoverState{}, err
	}
	if record.Revision != input.ExpectedRevision || record.Status != "active" {
		return FailoverState{}, ErrFailoverConflict
	}
	next, advanced := advanceFailoverRecord(record, input.Error, input.PositionSeconds, service.now())
	failedJSON, marshalErr := json.Marshal(next.FailedCandidates)
	if marshalErr != nil {
		return FailoverState{}, fmt.Errorf("encode failed playback candidates: %w", marshalErr)
	}
	command, err := tx.Exec(ctx, `
		UPDATE playback_source_failovers
		SET failed_candidates = $1::jsonb, current_source_ref = $2, position_seconds = $3,
			attempt_count = $4, revision = $5, status = $6, last_error = $7, updated_at = $8
		WHERE id::text = $9 AND auth_session_id = $10::uuid AND profile_id = $11::uuid AND revision = $12
	`, failedJSON, next.CurrentSourceRef, next.PositionSeconds, next.AttemptCount, next.Revision, next.Status, string(next.LastError), next.UpdatedAt,
		record.ID, principal.SessionID, *principal.ActiveProfileID, input.ExpectedRevision)
	if err != nil {
		return FailoverState{}, fmt.Errorf("advance playback source failover: %w", err)
	}
	if command.RowsAffected() != 1 {
		return FailoverState{}, ErrFailoverConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return FailoverState{}, fmt.Errorf("commit playback source failover advance: %w", err)
	}
	state := projectFailover(next)
	if advanced {
		state.Explanation = "Playback moved to the next available source and resumed at the saved position."
	} else {
		state.Explanation = "No unused source remains within the automatic failover budget."
	}
	return state, nil
}

func (service *Service) CancelFailover(ctx context.Context, principal auth.Principal, identifier string) error {
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE playback_source_failovers
		SET status = 'cancelled', revision = revision + 1, updated_at = now()
		WHERE id::text = $1 AND auth_session_id = $2::uuid AND profile_id = $3::uuid
		  AND expires_at > now() AND status = 'active' AND created_at >= $4
	`, strings.TrimSpace(identifier), principal.SessionID, *principal.ActiveProfileID, service.failoverStartedAt)
	if err != nil {
		return fmt.Errorf("cancel playback source failover: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrFailoverNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit playback source failover cancellation: %w", err)
	}
	return nil
}

func (service *Service) loadFailover(ctx context.Context, tx pgx.Tx, principal auth.Principal, identifier string, lock bool) (failoverRecord, error) {
	if len(identifier) != 36 {
		return failoverRecord{}, ErrFailoverNotFound
	}
	query := `
		SELECT id::text, candidate_refs, failed_candidates, current_source_ref, position_seconds,
		       attempt_count, max_attempts, revision, status, COALESCE(last_error, ''), updated_at, expires_at
		FROM playback_source_failovers
		WHERE id::text = $1 AND auth_session_id = $2::uuid AND profile_id = $3::uuid
		  AND expires_at > now() AND created_at >= $4`
	if lock {
		query += " FOR UPDATE"
	}
	var record failoverRecord
	var candidatesJSON, failedJSON []byte
	err := tx.QueryRow(ctx, query, identifier, principal.SessionID, *principal.ActiveProfileID, service.failoverStartedAt).Scan(
		&record.ID, &candidatesJSON, &failedJSON, &record.CurrentSourceRef, &record.PositionSeconds,
		&record.AttemptCount, &record.MaximumAttempts, &record.Revision, &record.Status, &record.LastError, &record.UpdatedAt, &record.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return failoverRecord{}, ErrFailoverNotFound
	}
	if err != nil {
		return failoverRecord{}, fmt.Errorf("load playback source failover: %w", err)
	}
	if json.Unmarshal(candidatesJSON, &record.Candidates) != nil || json.Unmarshal(failedJSON, &record.FailedCandidates) != nil {
		return failoverRecord{}, errors.New("invalid persisted playback source failover")
	}
	return record, nil
}

func advanceFailoverRecord(record failoverRecord, failure FailoverError, position float64, now time.Time) (failoverRecord, bool) {
	record.FailedCandidates = appendUnique(record.FailedCandidates, record.CurrentSourceRef)
	record.PositionSeconds = position
	record.AttemptCount++
	record.Revision++
	record.LastError = failure
	record.UpdatedAt = now
	if record.AttemptCount > record.MaximumAttempts {
		record.AttemptCount = record.MaximumAttempts
		record.Status = "exhausted"
		return record, false
	}
	current := candidateIndex(record.Candidates, record.CurrentSourceRef)
	for index := current + 1; index < len(record.Candidates); index++ {
		if candidateIndex(record.FailedCandidates, record.Candidates[index]) >= 0 {
			continue
		}
		record.CurrentSourceRef = record.Candidates[index]
		return record, true
	}
	record.Status = "exhausted"
	return record, false
}

func projectFailover(record failoverRecord) FailoverState {
	current := candidateIndex(record.Candidates, record.CurrentSourceRef)
	failed := make(map[string]struct{}, len(record.FailedCandidates))
	for _, candidate := range record.FailedCandidates {
		failed[candidate] = struct{}{}
	}
	health := make([]FailoverCandidateHealth, len(record.Candidates))
	cooldownUntil := record.UpdatedAt.Add(failoverCooldown)
	for index, candidate := range record.Candidates {
		status := "available"
		var cooldown *time.Time
		if _, unavailable := failed[candidate]; unavailable {
			status = "cooling_down"
			value := cooldownUntil
			cooldown = &value
		} else if candidate == record.CurrentSourceRef {
			status = "current"
		}
		health[index] = FailoverCandidateHealth{Position: index, Status: status, CooldownUntil: cooldown}
	}
	return FailoverState{
		ID: record.ID, CurrentSourceRef: record.CurrentSourceRef, CurrentPosition: current,
		PositionSeconds: record.PositionSeconds, AttemptCount: record.AttemptCount, MaximumAttempts: record.MaximumAttempts,
		Revision: record.Revision, Status: record.Status, LastError: record.LastError, CandidateHealth: health, ExpiresAt: record.ExpiresAt,
	}
}

func validFailoverError(value FailoverError) bool {
	switch value {
	case FailoverSourceFailed, FailoverSourceTimeout, FailoverEndedEarly, FailoverDecodeFailed, FailoverAccessDenied, FailoverUserCancelled:
		return true
	default:
		return false
	}
}

func eligibleFailoverError(value FailoverError) bool {
	return value == FailoverSourceFailed || value == FailoverSourceTimeout || value == FailoverEndedEarly
}

func appendUnique(values []string, value string) []string {
	if candidateIndex(values, value) >= 0 {
		return values
	}
	return append(values, value)
}

func candidateIndex(values []string, value string) int {
	for index, candidate := range values {
		if candidate == value {
			return index
		}
	}
	return -1
}
