package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maximumStoredDisplayPreferenceBytes = 32 << 10
	maximumDisplayPreferencesPerScope   = 128
)

var (
	ErrInvalidDisplayPreferences = errors.New("invalid display preferences")
	ErrDisplayPreferenceLimit    = errors.New("display preference limit reached")
)

type displayPreferenceScope struct {
	userID    string
	profileID string
	client    string
	id        string
}

// DisplayPreferenceStorage is the durable boundary used by the compatibility
// service. Implementations must isolate every operation by the complete scope.
type DisplayPreferenceStorage interface {
	Load(context.Context, displayPreferenceScope) (json.RawMessage, bool, error)
	Save(context.Context, displayPreferenceScope, json.RawMessage) error
}

// DisplayPreferences is the handler-facing service contract.
type DisplayPreferences interface {
	Get(context.Context, AuthenticatedSession, string, string) (DisplayPreferencesDto, bool, error)
	Update(context.Context, AuthenticatedSession, string, string, DisplayPreferencesDto) error
}

// DisplayPreferenceRepository persists opaque, validated Jellyfin preference
// documents. Native account and profile identifiers are both part of the key.
type DisplayPreferenceRepository struct {
	pool *pgxpool.Pool
}

func NewDisplayPreferenceRepository(pool *pgxpool.Pool) (*DisplayPreferenceRepository, error) {
	if pool == nil {
		return nil, fmt.Errorf("display preference repository database is required")
	}
	return &DisplayPreferenceRepository{pool: pool}, nil
}

func (repository *DisplayPreferenceRepository) Load(ctx context.Context, scope displayPreferenceScope) (json.RawMessage, bool, error) {
	if repository == nil || repository.pool == nil || !validDisplayPreferenceScope(scope) {
		return nil, false, ErrInvalidDisplayPreferences
	}
	var payload []byte
	err := repository.pool.QueryRow(ctx, `
		SELECT preferences::text
		FROM jellyfin_display_preferences
		WHERE user_id = $1::uuid
		  AND profile_id = $2::uuid
		  AND client = $3
		  AND display_preferences_id = $4
	`, scope.userID, scope.profileID, scope.client, scope.id).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load display preferences: %w", err)
	}
	if !validStoredDisplayPreferencePayload(payload) {
		return nil, false, fmt.Errorf("load display preferences: %w", ErrInvalidDisplayPreferences)
	}
	return json.RawMessage(payload), true, nil
}

func (repository *DisplayPreferenceRepository) Save(ctx context.Context, scope displayPreferenceScope, payload json.RawMessage) error {
	if repository == nil || repository.pool == nil || !validDisplayPreferenceScope(scope) || !validStoredDisplayPreferencePayload(payload) {
		return ErrInvalidDisplayPreferences
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin display preference update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	lockKey := scope.userID + ":" + scope.profileID
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock display preference scope: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM jellyfin_display_preferences
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
			  AND client = $3 AND display_preferences_id = $4
		)
	`, scope.userID, scope.profileID, scope.client, scope.id).Scan(&exists); err != nil {
		return fmt.Errorf("check display preference: %w", err)
	}
	if !exists {
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM jellyfin_display_preferences
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
		`, scope.userID, scope.profileID).Scan(&count); err != nil {
			return fmt.Errorf("count display preferences: %w", err)
		}
		if count >= maximumDisplayPreferencesPerScope {
			return ErrDisplayPreferenceLimit
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO jellyfin_display_preferences (
			user_id, profile_id, client, display_preferences_id, preferences
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb)
		ON CONFLICT (user_id, profile_id, client, display_preferences_id)
		DO UPDATE SET preferences = EXCLUDED.preferences, updated_at = now()
	`, scope.userID, scope.profileID, scope.client, scope.id, payload); err != nil {
		return fmt.Errorf("store display preferences: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit display preference update: %w", err)
	}
	return nil
}

// DisplayPreferenceService validates the Jellyfin DTO and maps it to the
// durable account/profile/client/display-id scope without changing Rivune
// global settings.
type DisplayPreferenceService struct {
	storage DisplayPreferenceStorage
}

func NewDisplayPreferenceService(storage DisplayPreferenceStorage) (*DisplayPreferenceService, error) {
	if storage == nil {
		return nil, fmt.Errorf("display preference storage is required")
	}
	return &DisplayPreferenceService{storage: storage}, nil
}

func (service *DisplayPreferenceService) Get(ctx context.Context, session AuthenticatedSession, client, id string) (DisplayPreferencesDto, bool, error) {
	scope, err := displayPreferenceScopeForSession(session, client, id)
	if err != nil || service == nil || service.storage == nil {
		return DisplayPreferencesDto{}, false, ErrInvalidDisplayPreferences
	}
	payload, found, err := service.storage.Load(ctx, scope)
	if err != nil || !found {
		return DisplayPreferencesDto{}, found, err
	}
	value, err := decodeStoredDisplayPreferences(payload)
	if err != nil || !validDisplayPreferences(value, id, client) {
		return DisplayPreferencesDto{}, false, fmt.Errorf("decode display preferences: %w", ErrInvalidDisplayPreferences)
	}
	value.Id = id
	value.Client = client
	if value.CustomPrefs == nil {
		value.CustomPrefs = map[string]string{}
	}
	return value, true, nil
}

func (service *DisplayPreferenceService) Update(ctx context.Context, session AuthenticatedSession, client, id string, value DisplayPreferencesDto) error {
	scope, err := displayPreferenceScopeForSession(session, client, id)
	if err != nil || service == nil || service.storage == nil || !validDisplayPreferences(value, id, client) {
		return ErrInvalidDisplayPreferences
	}
	value.Id = id
	value.Client = client
	if value.CustomPrefs == nil {
		value.CustomPrefs = map[string]string{}
	}
	payload, err := json.Marshal(value)
	if err != nil || !validStoredDisplayPreferencePayload(payload) {
		return ErrInvalidDisplayPreferences
	}
	return service.storage.Save(ctx, scope, payload)
}

func displayPreferenceScopeForSession(session AuthenticatedSession, client, id string) (displayPreferenceScope, error) {
	scope := displayPreferenceScope{
		userID: strings.TrimSpace(session.Principal.UserID), profileID: strings.TrimSpace(session.ProfileID),
		client: strings.TrimSpace(client), id: strings.TrimSpace(id),
	}
	if !validAuthenticatedSession(session) || !validDisplayPreferenceScope(scope) {
		return displayPreferenceScope{}, ErrInvalidDisplayPreferences
	}
	return scope, nil
}

func validDisplayPreferenceScope(scope displayPreferenceScope) bool {
	_, userOK := canonicalCompatUUID(scope.userID)
	_, profileOK := canonicalCompatUUID(scope.profileID)
	return userOK && profileOK && boundedUTF8(scope.client, 1, 64) && validDisplayPreferenceID(scope.id) &&
		scope.client == strings.TrimSpace(scope.client) && scope.id == strings.TrimSpace(scope.id)
}

func validStoredDisplayPreferencePayload(payload []byte) bool {
	if len(payload) == 0 || len(payload) > maximumStoredDisplayPreferenceBytes || !json.Valid(payload) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(payload, &object) == nil && object != nil
}

func decodeStoredDisplayPreferences(payload []byte) (DisplayPreferencesDto, error) {
	var value DisplayPreferencesDto
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return DisplayPreferencesDto{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return DisplayPreferencesDto{}, ErrInvalidDisplayPreferences
	}
	return value, nil
}
