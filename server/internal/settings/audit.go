package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	DefaultAuditLimit = 50
	MaximumAuditLimit = 100
)

type AuditEvent struct {
	ID          int64           `json:"id"`
	Revision    int64           `json:"revision"`
	ActorUserID *string         `json:"actorUserId"`
	Action      string          `json:"action"`
	ChangedKeys []string        `json:"changedKeys"`
	Snapshot    json.RawMessage `json:"snapshot"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type AuditPage struct {
	Events     []AuditEvent `json:"events"`
	NextCursor *int64       `json:"nextCursor"`
}

func (s *Service) ListAuditEvents(ctx context.Context, principal auth.Principal, beforeID *int64, limit int) (AuditPage, error) {
	if !principal.IsGlobalAdministrator() {
		return AuditPage{}, ErrForbidden
	}
	if limit == 0 {
		limit = DefaultAuditLimit
	}
	if limit < 1 || limit > MaximumAuditLimit || beforeID != nil && *beforeID < 1 {
		return AuditPage{}, fmt.Errorf("%w: invalid audit page", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AuditPage{}, fmt.Errorf("begin audit query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAdministrator(ctx, tx, principal); err != nil {
		return AuditPage{}, err
	}

	cursor := int64(^uint64(0) >> 1)
	if beforeID != nil {
		cursor = *beforeID
	}
	rows, err := tx.Query(ctx, `
		SELECT id, revision, actor_user_id::text, action, changed_keys, snapshot, created_at
		FROM instance_configuration_audit_events
		WHERE instance_id = 1 AND id < $1
		ORDER BY id DESC
		LIMIT $2
	`, cursor, limit+1)
	if err != nil {
		return AuditPage{}, fmt.Errorf("query configuration audit: %w", err)
	}
	page := AuditPage{Events: make([]AuditEvent, 0, limit)}
	for rows.Next() {
		var event AuditEvent
		if err := rows.Scan(&event.ID, &event.Revision, &event.ActorUserID, &event.Action, &event.ChangedKeys, &event.Snapshot, &event.CreatedAt); err != nil {
			return AuditPage{}, fmt.Errorf("scan configuration audit: %w", err)
		}
		if err := validateAuditSnapshot(event.Action, event.Snapshot); err != nil {
			return AuditPage{}, err
		}
		if len(page.Events) == limit {
			cursor := page.Events[len(page.Events)-1].ID
			page.NextCursor = &cursor
			break
		}
		page.Events = append(page.Events, event)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, fmt.Errorf("iterate configuration audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AuditPage{}, fmt.Errorf("commit audit query: %w", err)
	}
	return page, nil
}

func authorizeGlobalAdministrator(ctx context.Context, tx pgx.Tx, principal auth.Principal) error {
	if !principal.IsGlobalAdministrator() || strings.TrimSpace(principal.UserID) == "" {
		return ErrForbidden
	}
	var administrator bool
	err := tx.QueryRow(ctx, `SELECT role = 'admin' FROM users WHERE id = $1::uuid FOR SHARE`, principal.UserID).Scan(&administrator)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("authorize settings administrator: %w", err)
	}
	if !administrator {
		return ErrForbidden
	}
	return nil
}

func incrementConfigurationRevision(ctx context.Context, tx pgx.Tx) (int64, error) {
	var revision int64
	if err := tx.QueryRow(ctx, `
		UPDATE instances SET configuration_revision = configuration_revision + 1, updated_at = now()
		WHERE id = 1 RETURNING configuration_revision
	`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("increment configuration revision: %w", err)
	}
	return revision, nil
}

func insertAuditEvent(ctx context.Context, tx pgx.Tx, revision int64, actorUserID, action string, changedKeys []string, snapshot []byte) error {
	sort.Strings(changedKeys)
	if err := validateAuditSnapshot(action, snapshot); err != nil {
		return err
	}
	var actor any
	if actorUserID != "" {
		actor = actorUserID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO instance_configuration_audit_events
		(instance_id, revision, actor_user_id, action, changed_keys, snapshot)
		VALUES (1, $1, $2::uuid, $3, $4, $5)
	`, revision, actor, action, changedKeys, snapshot); err != nil {
		return fmt.Errorf("insert configuration audit event: %w", err)
	}
	return nil
}

func marshalSettings(values Values) ([]byte, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode instance settings: %w", err)
	}
	if err := ensureNoSecretSettings(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

func ensureNoSecretSettings(encoded []byte) error {
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return fmt.Errorf("decode settings safety boundary: %w", err)
	}
	if containsSecretKey(value) {
		return errors.New("settings data contains a forbidden credential field")
	}
	return nil
}

func containsSecretKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(key)
			switch normalized {
			case "tmdbaccesstoken", "fanartapikey", "mdblistapikey", "tvdbapikey", "tvdbpin", "traktclientid", "traktclientsecret", "simklclientid":
				return true
			}
			if containsSecretKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSecretKey(child) {
				return true
			}
		}
	}
	return false
}

func validateAuditSnapshot(action string, encoded []byte) error {
	if action == "settings.updated" {
		return ensureNoSecretSettings(encoded)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return fmt.Errorf("decode redacted audit snapshot: %w", err)
	}
	credentials := value
	if action == "legacy_environment.imported" {
		nested, ok := value["credentials"].(map[string]any)
		if !ok {
			return errors.New("legacy audit snapshot is not redacted status data")
		}
		credentials = nested
		delete(value, "credentials")
		settings, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode audit safety boundary: %w", err)
		}
		if err := ensureNoSecretSettings(settings); err != nil {
			return err
		}
	}
	if len(credentials) != len(integrationNames) {
		return errors.New("integration audit snapshot is incomplete")
	}
	for key, status := range credentials {
		known := false
		for _, name := range integrationNames {
			if key == name {
				known = true
				break
			}
		}
		if !known {
			return errors.New("integration audit snapshot contains an unknown field")
		}
		if _, ok := status.(bool); !ok {
			return errors.New("integration audit snapshot is not redacted status data")
		}
	}
	return nil
}

func instancePatchKeys(patch Patch) []string {
	keys := make([]string, 0, 32)
	for name, set := range map[string]bool{
		"interfaceLanguage": patch.InterfaceLanguage.Set, "theme": patch.Theme.Set,
		"maximumResolution": patch.MaximumResolution.Set, "maximumCastMembers": patch.MaximumCastMembers.Set,
		"maximumDirectTitles": patch.MaximumDirectTitles.Set, "preferDirectPlay": patch.PreferDirectPlay.Set,
		"allowTranscoding": patch.AllowTranscoding.Set, "jellyfinEnabled": patch.JellyfinEnabled.Set,
		"timezone": patch.Timezone.Set, "jellyfinDebug": patch.JellyfinDebug.Set,
		"hardwareAcceleration": patch.HardwareAcceleration.Set, "transcodeMaxBitrateKbps": patch.TranscodeMaxBitrateKbps.Set,
		"mediaMaxStorageMB": patch.MediaMaxStorageMB.Set, "artworkMaxStorageMB": patch.ArtworkMaxStorageMB.Set,
		"hideUnreleased": patch.HideUnreleased.Set, "metadataLanguage": patch.MetadataLanguage.Set,
		"metadataRegion": patch.MetadataRegion.Set, "seriesMappingProvider": patch.SeriesMappingProvider.Set,
		"audioLanguage": patch.AudioLanguage.Set, "subtitleLanguage": patch.SubtitleLanguage.Set,
		"forcedSubtitleLanguage": patch.ForcedSubtitleLanguage.Set, "autoplayNextEpisode": patch.AutoplayNextEpisode.Set,
		"skipIntroEnabled": patch.SkipIntroEnabled.Set, "skipRecapEnabled": patch.SkipRecapEnabled.Set,
		"skipOutroEnabled": patch.SkipOutroEnabled.Set, "cardDensity": patch.CardDensity.Set,
		"animationsEnabled": patch.AnimationsEnabled.Set, "subtitleSizePercent": patch.SubtitleSizePercent.Set,
		"subtitleTextColor":                patch.SubtitleTextColor.Set,
		"subtitleBackgroundOpacityPercent": patch.SubtitleBackgroundOpacityPercent.Set,
		"notificationsEnabled":             patch.NotificationsEnabled.Set,
		"notificationDurationSeconds":      patch.NotificationDurationSeconds.Set,
		"notificationPollIntervalSeconds":  patch.NotificationPollIntervalSeconds.Set,
	} {
		if set {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	return keys
}
