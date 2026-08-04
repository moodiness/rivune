package tracking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

var (
	imdbIDPattern            = regexp.MustCompile(`^tt[0-9]{7,10}$`)
	authorizationCodePattern = regexp.MustCompile(`^[A-Za-z0-9-]{4,32}$`)
)

const (
	workerInterval = 5 * time.Second
	leaseDuration  = 30 * time.Second
)

type Service struct {
	pool   *pgxpool.Pool
	cipher *tokenCipher
	client *providerClient
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, encryptionKey []byte, traktClientID, traktClientSecret, simklClientID string, httpClient *http.Client, logger *slog.Logger) (*Service, error) {
	cipher, err := newTokenCipher(encryptionKey)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, cipher: cipher, client: newProviderClient(traktClientID, traktClientSecret, simklClientID, httpClient), logger: logger}, nil
}

func normalizeProvider(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "trakt" && provider != "simkl" {
		return "", fmt.Errorf("%w: provider must be trakt or simkl", ErrInvalidInput)
	}
	return provider, nil
}

func providerFacingError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*upstreamError); ok {
		return ErrProviderUnavailable
	}
	return err
}

func (s *Service) authorizeProfile(ctx context.Context, tx pgx.Tx, principal auth.Principal, profileID string) (string, error) {
	profileID = strings.ToLower(strings.TrimSpace(profileID))
	if profileID == "" {
		return "", ErrForbidden
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil {
		return "", fmt.Errorf("authorize tracking profile: %w", err)
	}
	if !authorized {
		return "", ErrForbidden
	}
	return profileID, nil
}

func (s *Service) Statuses(ctx context.Context, principal auth.Principal, profileID string) ([]Status, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tracking statuses query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	profileID, err = s.authorizeProfile(ctx, tx, principal, profileID)
	if err != nil {
		return nil, err
	}
	statuses, err := s.statuses(ctx, tx, profileID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tracking statuses query: %w", err)
	}
	return statuses, nil
}

func (s *Service) statuses(ctx context.Context, tx pgx.Tx, profileID string) ([]Status, error) {
	statuses := []Status{{Provider: "trakt", Configured: s.client.configured("trakt")}, {Provider: "simkl", Configured: s.client.configured("simkl")}}
	rows, err := tx.Query(ctx, `
		SELECT account.provider, account.sync_watched, account.sync_progress, account.sync_library,
		       account.connected_at, account.last_success_at, COALESCE(account.last_error, ''), count(outbox.id)
		FROM profile_tracking_accounts account
		LEFT JOIN profile_tracking_outbox outbox ON outbox.profile_id = account.profile_id AND outbox.provider = account.provider
		WHERE account.profile_id = $1::uuid
		GROUP BY account.profile_id, account.provider
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("query tracking statuses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var provider string
		var watched, progress, library bool
		var connected time.Time
		var success *time.Time
		var lastError string
		var pending int
		if err := rows.Scan(&provider, &watched, &progress, &library, &connected, &success, &lastError, &pending); err != nil {
			return nil, fmt.Errorf("scan tracking status: %w", err)
		}
		for index := range statuses {
			if statuses[index].Provider == provider {
				statuses[index].Connected = true
				statuses[index].SyncWatched = watched
				statuses[index].SyncProgress = progress
				statuses[index].SyncLibrary = library
				statuses[index].ConnectedAt = &connected
				statuses[index].LastSuccessAt = success
				statuses[index].LastError = lastError
				statuses[index].PendingItems = pending
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tracking statuses: %w", err)
	}
	return statuses, nil
}

func (s *Service) BeginDeviceAuthorization(ctx context.Context, principal auth.Principal, profileID, provider string) (DeviceAuthorization, error) {
	authorizationTx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("begin tracking authorization check: %w", err)
	}
	defer func() { _ = authorizationTx.Rollback(ctx) }()
	profileID, err = s.authorizeProfile(ctx, authorizationTx, principal, profileID)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	if err := authorizationTx.Commit(ctx); err != nil {
		return DeviceAuthorization{}, fmt.Errorf("commit tracking authorization check: %w", err)
	}

	provider, err = normalizeProvider(provider)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	code, err := s.client.beginDeviceAuthorization(ctx, provider)
	if err != nil {
		return DeviceAuthorization{}, providerFacingError(err)
	}
	if len(code.ProviderCode) < 1 || len(code.ProviderCode) > 4096 || !authorizationCodePattern.MatchString(code.UserCode) ||
		code.VerificationURL == "" || code.ExpiresIn < 1 || code.ExpiresIn > 24*60*60 || code.Interval < 1 || code.Interval > 300 {
		return DeviceAuthorization{}, fmt.Errorf("%w: invalid device authorization response", ErrProviderUnavailable)
	}
	encrypted, err := s.cipher.encrypt(code.ProviderCode, profileID+":"+provider+":authorization")
	if err != nil {
		return DeviceAuthorization{}, err
	}
	expiresAt := time.Now().UTC().Add(time.Duration(code.ExpiresIn) * time.Second)

	storeTx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("begin tracking authorization storage: %w", err)
	}
	defer func() { _ = storeTx.Rollback(ctx) }()
	profileID, err = s.authorizeProfile(ctx, storeTx, principal, profileID)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	var result DeviceAuthorization
	err = storeTx.QueryRow(ctx, `
		INSERT INTO profile_tracking_authorizations (profile_id, provider, provider_code_encrypted, user_code, verification_url, interval_seconds, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (profile_id, provider) DO UPDATE SET provider_code_encrypted = EXCLUDED.provider_code_encrypted,
			user_code = EXCLUDED.user_code, verification_url = EXCLUDED.verification_url,
			interval_seconds = EXCLUDED.interval_seconds, expires_at = EXCLUDED.expires_at,
			last_polled_at = NULL, created_at = now()
		RETURNING id::text, provider, user_code, verification_url, expires_at, interval_seconds
	`, profileID, provider, encrypted, code.UserCode, code.VerificationURL, code.Interval, expiresAt).Scan(&result.ID, &result.Provider, &result.UserCode, &result.VerificationURL, &result.ExpiresAt, &result.IntervalSeconds)
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("store tracking authorization: %w", err)
	}
	if err := storeTx.Commit(ctx); err != nil {
		return DeviceAuthorization{}, fmt.Errorf("commit tracking authorization storage: %w", err)
	}
	return result, nil
}

func (s *Service) CompleteDeviceAuthorization(ctx context.Context, principal auth.Principal, profileID, provider, authorizationID string) (Status, error) {
	pollTx, err := s.pool.Begin(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("begin tracking authorization poll: %w", err)
	}
	defer func() { _ = pollTx.Rollback(ctx) }()
	profileID, err = s.authorizeProfile(ctx, pollTx, principal, profileID)
	if err != nil {
		return Status{}, err
	}
	provider, err = normalizeProvider(provider)
	if err != nil {
		return Status{}, err
	}
	var encrypted []byte
	var interval int
	var expiresAt time.Time
	var lastPolled *time.Time
	err = pollTx.QueryRow(ctx, `SELECT provider_code_encrypted, interval_seconds, expires_at, last_polled_at FROM profile_tracking_authorizations WHERE id::text = $1 AND profile_id = $2::uuid AND provider = $3`, strings.TrimSpace(authorizationID), profileID, provider).Scan(&encrypted, &interval, &expiresAt, &lastPolled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{}, ErrAuthorizationGone
	}
	if err != nil {
		return Status{}, fmt.Errorf("query tracking authorization: %w", err)
	}
	if time.Now().After(expiresAt) {
		return Status{}, ErrAuthorizationGone
	}
	now := time.Now().UTC()
	if lastPolled != nil && now.Before(lastPolled.Add(time.Duration(interval)*time.Second)) {
		return Status{}, ErrAuthorizationSlow
	}
	if _, err := pollTx.Exec(ctx, `UPDATE profile_tracking_authorizations SET last_polled_at = $2 WHERE id::text = $1`, authorizationID, now); err != nil {
		return Status{}, fmt.Errorf("update tracking authorization poll: %w", err)
	}
	if err := pollTx.Commit(ctx); err != nil {
		return Status{}, fmt.Errorf("commit tracking authorization poll: %w", err)
	}

	code, err := s.cipher.decrypt(encrypted, profileID+":"+provider+":authorization")
	if err != nil {
		return Status{}, err
	}
	token, err := s.client.pollDeviceAuthorization(ctx, provider, code)
	if err != nil {
		return Status{}, providerFacingError(err)
	}
	accessEncrypted, err := s.cipher.encrypt(token.AccessToken, profileID+":"+provider+":access")
	if err != nil {
		return Status{}, err
	}
	var refreshEncrypted []byte
	if token.RefreshToken != "" {
		refreshEncrypted, err = s.cipher.encrypt(token.RefreshToken, profileID+":"+provider+":refresh")
		if err != nil {
			return Status{}, err
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("begin tracking connection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err = s.authorizeProfile(ctx, tx, principal, profileID)
	if err != nil {
		return Status{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile_tracking_accounts (profile_id, provider, access_token_encrypted, refresh_token_encrypted, token_expires_at)
		VALUES ($1::uuid, $2, $3, NULLIF($4, ''::bytea), $5)
		ON CONFLICT (profile_id, provider) DO UPDATE SET access_token_encrypted = EXCLUDED.access_token_encrypted,
			refresh_token_encrypted = EXCLUDED.refresh_token_encrypted, token_expires_at = EXCLUDED.token_expires_at,
			connected_at = now(), updated_at = now(), last_error = NULL
	`, profileID, provider, accessEncrypted, refreshEncrypted, token.ExpiresAt); err != nil {
		return Status{}, fmt.Errorf("store tracking connection: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM profile_tracking_authorizations WHERE profile_id = $1::uuid AND provider = $2`, profileID, provider); err != nil {
		return Status{}, fmt.Errorf("clear tracking authorization: %w", err)
	}
	if err := s.seedProfileState(ctx, tx, profileID, provider); err != nil {
		return Status{}, err
	}
	statuses, err := s.statuses(ctx, tx, profileID)
	if err != nil {
		return Status{}, err
	}
	var result Status
	found := false
	for _, status := range statuses {
		if status.Provider == provider {
			result, found = status, true
			break
		}
	}
	if !found {
		return Status{}, ErrNotConnected
	}
	if err := tx.Commit(ctx); err != nil {
		return Status{}, fmt.Errorf("commit tracking connection: %w", err)
	}
	return result, nil
}

func (s *Service) seedProfileState(ctx context.Context, tx pgx.Tx, profileID, provider string) error {
	type libraryState struct {
		titleID   string
		updatedAt time.Time
	}
	libraryRows, err := tx.Query(ctx, `SELECT title_id::text, updated_at FROM profile_library WHERE profile_id = $1::uuid ORDER BY title_id`, profileID)
	if err != nil {
		return fmt.Errorf("query library tracking seed: %w", err)
	}
	library := make([]libraryState, 0)
	for libraryRows.Next() {
		var state libraryState
		if err := libraryRows.Scan(&state.titleID, &state.updatedAt); err != nil {
			libraryRows.Close()
			return fmt.Errorf("scan library tracking seed: %w", err)
		}
		library = append(library, state)
	}
	if err := libraryRows.Err(); err != nil {
		libraryRows.Close()
		return fmt.Errorf("iterate library tracking seed: %w", err)
	}
	libraryRows.Close()
	type progressState struct {
		titleID            string
		position, duration int
		completed          bool
		version            int64
		updatedAt          time.Time
	}
	progressRows, err := tx.Query(ctx, `SELECT title_id::text, position_seconds, duration_seconds, completed, version, updated_at FROM profile_progress WHERE profile_id = $1::uuid ORDER BY title_id`, profileID)
	if err != nil {
		return fmt.Errorf("query progress tracking seed: %w", err)
	}
	progress := make([]progressState, 0)
	for progressRows.Next() {
		var state progressState
		if err := progressRows.Scan(&state.titleID, &state.position, &state.duration, &state.completed, &state.version, &state.updatedAt); err != nil {
			progressRows.Close()
			return fmt.Errorf("scan progress tracking seed: %w", err)
		}
		progress = append(progress, state)
	}
	if err := progressRows.Err(); err != nil {
		progressRows.Close()
		return fmt.Errorf("iterate progress tracking seed: %w", err)
	}
	progressRows.Close()
	for _, state := range library {
		if err := s.enqueueWithProvider(ctx, tx, profileID, provider, state.titleID, fmt.Sprintf("connect:library:%s:%d", state.titleID, state.updatedAt.UnixNano()), Event{
			Type: "library", TitleID: state.titleID, InLibrary: true, OccurredAt: state.updatedAt,
		}); err != nil {
			return err
		}
	}
	for _, state := range progress {
		if err := s.enqueueWithProvider(ctx, tx, profileID, provider, state.titleID, fmt.Sprintf("connect:watched:%s:%d", state.titleID, state.version), Event{
			Type: "watched", TitleID: state.titleID, Completed: state.completed, Version: state.version, OccurredAt: state.updatedAt,
		}); err != nil {
			return err
		}
		if !state.completed && state.position > 0 {
			if err := s.enqueueWithProvider(ctx, tx, profileID, provider, state.titleID, fmt.Sprintf("connect:progress:%s:%d", state.titleID, state.version), Event{
				Type: "progress", TitleID: state.titleID, PositionSeconds: state.position, DurationSeconds: state.duration,
				Version: state.version, OccurredAt: state.updatedAt,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) UpdatePreferences(ctx context.Context, principal auth.Principal, profileID, provider string, input PreferencesInput) (Status, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("begin tracking preferences update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err = s.authorizeProfile(ctx, tx, principal, profileID)
	if err != nil {
		return Status{}, err
	}
	provider, err = normalizeProvider(provider)
	if err != nil {
		return Status{}, err
	}
	if input.SyncWatched == nil && input.SyncProgress == nil && input.SyncLibrary == nil {
		return Status{}, fmt.Errorf("%w: at least one toggle is required", ErrInvalidInput)
	}
	result, err := tx.Exec(ctx, `
		UPDATE profile_tracking_accounts
		SET sync_watched = COALESCE($3, sync_watched), sync_progress = COALESCE($4, sync_progress),
		    sync_library = COALESCE($5, sync_library), updated_at = now()
		WHERE profile_id = $1::uuid AND provider = $2
	`, profileID, provider, input.SyncWatched, input.SyncProgress, input.SyncLibrary)
	if err != nil {
		return Status{}, fmt.Errorf("update tracking preferences: %w", err)
	}
	if result.RowsAffected() == 0 {
		return Status{}, ErrNotConnected
	}
	disabled := make([]string, 0, 3)
	if input.SyncWatched != nil && !*input.SyncWatched {
		disabled = append(disabled, "watched")
	}
	if input.SyncProgress != nil && !*input.SyncProgress {
		disabled = append(disabled, "progress")
	}
	if input.SyncLibrary != nil && !*input.SyncLibrary {
		disabled = append(disabled, "library")
	}
	if len(disabled) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM profile_tracking_outbox WHERE profile_id = $1::uuid AND provider = $2 AND event_type = ANY($3)`, profileID, provider, disabled); err != nil {
			return Status{}, fmt.Errorf("clear disabled tracking work: %w", err)
		}
	}
	if input.SyncWatched != nil && *input.SyncWatched || input.SyncProgress != nil && *input.SyncProgress || input.SyncLibrary != nil && *input.SyncLibrary {
		if err := s.seedProfileState(ctx, tx, profileID, provider); err != nil {
			return Status{}, err
		}
	}
	statuses, err := s.statuses(ctx, tx, profileID)
	if err != nil {
		return Status{}, err
	}
	var updated Status
	found := false
	for _, status := range statuses {
		if status.Provider == provider {
			updated, found = status, true
			break
		}
	}
	if !found {
		return Status{}, ErrNotConnected
	}
	if err := tx.Commit(ctx); err != nil {
		return Status{}, fmt.Errorf("commit tracking preferences update: %w", err)
	}
	return updated, nil
}

func (s *Service) Disconnect(ctx context.Context, principal auth.Principal, profileID, provider string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tracking disconnect: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err = s.authorizeProfile(ctx, tx, principal, profileID)
	if err != nil {
		return err
	}
	provider, err = normalizeProvider(provider)
	if err != nil {
		return err
	}
	var encrypted []byte
	err = tx.QueryRow(ctx, `
		WITH deleted_account AS (
			DELETE FROM profile_tracking_accounts
			WHERE profile_id = $1::uuid AND provider = $2
			RETURNING access_token_encrypted
		), deleted_authorization AS (
			DELETE FROM profile_tracking_authorizations
			WHERE profile_id = $1::uuid AND provider = $2
		)
		SELECT access_token_encrypted FROM deleted_account
	`, profileID, provider).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit tracking disconnect: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("disconnect tracking account: %w", err)
	}
	accessToken, decryptErr := s.cipher.decrypt(encrypted, profileID+":"+provider+":access")
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tracking disconnect: %w", err)
	}
	if decryptErr == nil {
		revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.client.revoke(revokeCtx, provider, accessToken)
	}
	return nil
}

type trackingExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (s *Service) Enqueue(ctx context.Context, profileID, titleID, idempotencyKey string, event Event) error {
	return s.enqueueWith(ctx, s.pool, profileID, titleID, idempotencyKey, event)
}

func (s *Service) EnqueueTx(ctx context.Context, tx pgx.Tx, profileID, titleID, idempotencyKey string, event Event) error {
	return s.enqueueWith(ctx, tx, profileID, titleID, idempotencyKey, event)
}

func (s *Service) enqueueWith(ctx context.Context, exec trackingExecer, profileID, titleID, idempotencyKey string, event Event) error {
	return s.enqueueWithProvider(ctx, exec, profileID, "", titleID, idempotencyKey, event)
}

func (s *Service) enqueueWithProvider(ctx context.Context, exec trackingExecer, profileID, provider, titleID, idempotencyKey string, event Event) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode tracking event: %w", err)
	}
	column := "sync_watched"
	switch event.Type {
	case "progress":
		column = "sync_progress"
	case "library":
		column = "sync_library"
	case "watched":
	default:
		if event.Type != "watched" {
			return fmt.Errorf("%w: unsupported tracking event", ErrInvalidInput)
		}
	}
	affectsWatched := event.Type == "watched" || event.Type == "progress" && event.Completed
	_, err = exec.Exec(ctx, fmt.Sprintf(`
		WITH enabled AS (
			SELECT account.profile_id, account.provider
			FROM profile_tracking_accounts account
			WHERE account.profile_id = $1::uuid AND account.%s
			  AND ($7 = '' OR account.provider = $7)
		), heads AS (
			INSERT INTO profile_tracking_event_heads (profile_id, provider, title_id, event_type, idempotency_key, affects_watched)
			SELECT enabled.profile_id, enabled.provider, $2::uuid, $3, $5, $6
			FROM enabled
			ON CONFLICT (profile_id, provider, title_id, event_type)
			DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key, affects_watched = EXCLUDED.affects_watched, updated_at = now()
			RETURNING profile_id, provider, title_id, event_type, idempotency_key
		)
		INSERT INTO profile_tracking_outbox (profile_id, provider, title_id, event_type, payload, idempotency_key)
		SELECT profile_id, provider, title_id, event_type, $4, idempotency_key FROM heads
		ON CONFLICT (profile_id, provider, idempotency_key) DO NOTHING
	`, column), profileID, titleID, event.Type, encoded, idempotencyKey, affectsWatched, provider)
	if err != nil {
		return fmt.Errorf("enqueue tracking event: %w", err)
	}
	return nil
}

func (s *Service) Run(ctx context.Context) {
	s.processAvailable(ctx)
	ticker := time.NewTicker(workerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processAvailable(ctx)
		}
	}
}

func (s *Service) processAvailable(ctx context.Context) {
	for count := 0; count < 20 && ctx.Err() == nil; count++ {
		work, found, err := s.claim(ctx)
		if err != nil {
			s.logger.Error("claim tracking work failed", "error", err)
			return
		}
		if !found {
			return
		}
		if err := s.process(ctx, work); err != nil {
			s.retry(ctx, work, err)
		} else {
			s.complete(ctx, work)
		}
	}
}

type queuedWork struct {
	ID, ProfileID, Provider, TitleID, EventType, IdempotencyKey string
	Payload                                                     []byte
	Attempts                                                    int
}

func (s *Service) claim(ctx context.Context) (queuedWork, bool, error) {
	var work queuedWork
	err := s.pool.QueryRow(ctx, `
		UPDATE profile_tracking_outbox SET leased_until = now() + $1::interval
		WHERE id = (
			SELECT candidate.id
			FROM profile_tracking_outbox candidate
			WHERE candidate.next_attempt_at <= now()
			  AND (candidate.leased_until IS NULL OR candidate.leased_until <= now())
			  AND NOT EXISTS (
				SELECT 1 FROM profile_tracking_outbox earlier
				WHERE earlier.profile_id = candidate.profile_id AND earlier.provider = candidate.provider
				  AND earlier.title_id = candidate.title_id AND earlier.enqueue_sequence < candidate.enqueue_sequence
			  )
			ORDER BY candidate.next_attempt_at, candidate.enqueue_sequence
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING id::text, profile_id::text, provider, title_id::text, event_type, idempotency_key, payload, attempt_count
	`, leaseDuration.String()).Scan(&work.ID, &work.ProfileID, &work.Provider, &work.TitleID, &work.EventType, &work.IdempotencyKey, &work.Payload, &work.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return queuedWork{}, false, nil
	}
	if err != nil {
		return queuedWork{}, false, fmt.Errorf("claim tracking work: %w", err)
	}
	return work, true, nil
}

func (s *Service) process(ctx context.Context, work queuedWork) error {
	var latest bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM profile_tracking_event_heads current
			WHERE current.profile_id = $1::uuid AND current.provider = $2 AND current.title_id = $3::uuid
			  AND current.event_type = $4 AND current.idempotency_key = $5
			  AND NOT EXISTS (
				SELECT 1 FROM profile_tracking_event_heads newer
				WHERE newer.profile_id = current.profile_id AND newer.provider = current.provider
				  AND newer.title_id = current.title_id AND newer.updated_at > current.updated_at
				  AND (newer.event_type = current.event_type OR current.affects_watched AND newer.affects_watched)
			  )
		)
	`, work.ProfileID, work.Provider, work.TitleID, work.EventType, work.IdempotencyKey).Scan(&latest); err != nil {
		return fmt.Errorf("check tracking event ordering: %w", err)
	}
	if !latest {
		return nil
	}
	var event Event
	if err := json.Unmarshal(work.Payload, &event); err != nil {
		return fmt.Errorf("decode queued tracking event: %w", err)
	}
	item, err := s.loadMediaItem(ctx, work.TitleID)
	if err != nil {
		return err
	}
	access, err := s.accessToken(ctx, work.ProfileID, work.Provider)
	if err != nil {
		return err
	}
	err = s.client.send(ctx, work.Provider, access, work.EventType, event, item)
	if upstream, ok := err.(*upstreamError); ok && upstream.status == http.StatusUnauthorized && work.Provider == "trakt" {
		if _, refreshErr := s.refreshAccessToken(ctx, work.ProfileID); refreshErr != nil {
			return refreshErr
		}
		access, refreshErr := s.accessToken(ctx, work.ProfileID, work.Provider)
		if refreshErr != nil {
			return refreshErr
		}
		return s.client.send(ctx, work.Provider, access, work.EventType, event, item)
	}
	return err
}

func (s *Service) accessToken(ctx context.Context, profileID, provider string) (string, error) {
	var encrypted, refreshEncrypted []byte
	var expiresAt *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT access_token_encrypted, refresh_token_encrypted, token_expires_at FROM profile_tracking_accounts WHERE profile_id = $1::uuid AND provider = $2`, profileID, provider).Scan(&encrypted, &refreshEncrypted, &expiresAt); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotConnected
	} else if err != nil {
		return "", fmt.Errorf("query tracking token: %w", err)
	}
	if provider == "trakt" && expiresAt != nil && time.Now().Add(time.Minute).After(*expiresAt) {
		if _, err := s.refreshAccessToken(ctx, profileID); err != nil {
			return "", err
		}
		return s.accessToken(ctx, profileID, provider)
	}
	return s.cipher.decrypt(encrypted, profileID+":"+provider+":access")
}

func (s *Service) refreshAccessToken(ctx context.Context, profileID string) (providerToken, error) {
	var encrypted []byte
	if err := s.pool.QueryRow(ctx, `SELECT refresh_token_encrypted FROM profile_tracking_accounts WHERE profile_id = $1::uuid AND provider = 'trakt'`, profileID).Scan(&encrypted); err != nil {
		return providerToken{}, fmt.Errorf("query Trakt refresh token: %w", err)
	}
	refresh, err := s.cipher.decrypt(encrypted, profileID+":trakt:refresh")
	if err != nil {
		return providerToken{}, err
	}
	token, err := s.client.refreshTrakt(ctx, refresh)
	if err != nil {
		return providerToken{}, err
	}
	accessEncrypted, err := s.cipher.encrypt(token.AccessToken, profileID+":trakt:access")
	if err != nil {
		return providerToken{}, err
	}
	refreshEncrypted, err := s.cipher.encrypt(token.RefreshToken, profileID+":trakt:refresh")
	if err != nil {
		return providerToken{}, err
	}
	_, err = s.pool.Exec(ctx, `UPDATE profile_tracking_accounts SET access_token_encrypted = $2, refresh_token_encrypted = $3, token_expires_at = $4, updated_at = now(), last_error = NULL WHERE profile_id = $1::uuid AND provider = 'trakt'`, profileID, accessEncrypted, refreshEncrypted, token.ExpiresAt)
	if err != nil {
		return providerToken{}, fmt.Errorf("store refreshed Trakt token: %w", err)
	}
	return token, nil
}

func (s *Service) loadMediaItem(ctx context.Context, titleID string) (mediaItem, error) {
	var item mediaItem
	var releaseDate *time.Time
	var season, episode *int
	var showTitle *string
	var showRelease *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT title.media_type, COALESCE(title.display_title, ''), title.release_date, title.ordinal,
		       season.ordinal, COALESCE(series.display_title, ''), series.release_date
		FROM titles title
		LEFT JOIN titles season ON title.media_type = 'episode' AND season.id = title.parent_id
		LEFT JOIN titles series ON season.parent_id = series.id
		WHERE title.id = $1::uuid
	`, titleID).Scan(&item.MediaType, &item.Title, &releaseDate, &episode, &season, &showTitle, &showRelease)
	if err != nil {
		return mediaItem{}, fmt.Errorf("query tracked title: %w", err)
	}
	if releaseDate != nil {
		item.Year = releaseDate.Year()
	}
	if episode != nil {
		item.Episode = *episode
	}
	if season != nil {
		item.Season = *season
	}
	if showTitle != nil {
		item.ShowTitle = *showTitle
	}
	if showRelease != nil {
		item.ShowYear = showRelease.Year()
	}
	item.IDs, err = s.loadIDs(ctx, titleID)
	if err != nil {
		return mediaItem{}, err
	}
	if item.MediaType == "episode" {
		err = s.pool.QueryRow(ctx, `SELECT series.id::text FROM titles episode JOIN titles season ON season.id = episode.parent_id JOIN titles series ON series.id = season.parent_id WHERE episode.id = $1::uuid`, titleID).Scan(&titleID)
		if err != nil {
			return mediaItem{}, fmt.Errorf("query tracked episode series: %w", err)
		}
		item.ShowIDs, err = s.loadIDs(ctx, titleID)
		if err != nil {
			return mediaItem{}, err
		}
	}
	return item, nil
}

func (s *Service) loadIDs(ctx context.Context, titleID string) (map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT provider, external_id FROM title_external_ids WHERE title_id = $1::uuid AND provider IN ('imdb', 'tmdb', 'tvdb')`, titleID)
	if err != nil {
		return nil, fmt.Errorf("query tracked title IDs: %w", err)
	}
	defer rows.Close()
	ids := make(map[string]any)
	for rows.Next() {
		var provider, externalID string
		if err := rows.Scan(&provider, &externalID); err != nil {
			return nil, fmt.Errorf("scan tracked title ID: %w", err)
		}
		if provider == "imdb" {
			if imdbIDPattern.MatchString(externalID) {
				ids[provider] = externalID
			}
			continue
		}
		if number, parseErr := strconv.ParseInt(externalID, 10, 64); parseErr == nil && number > 0 {
			ids[provider] = number
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tracked title IDs: %w", err)
	}
	return ids, nil
}

func (s *Service) complete(ctx context.Context, work queuedWork) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("begin tracking completion failed", "error", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM profile_tracking_outbox WHERE id = $1::uuid`, work.ID); err != nil {
		s.logger.Error("delete completed tracking work failed", "error", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE profile_tracking_accounts SET last_success_at = now(), last_error = NULL, updated_at = now() WHERE profile_id = $1::uuid AND provider = $2`, work.ProfileID, work.Provider); err != nil {
		s.logger.Error("update tracking success failed", "error", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit tracking completion failed", "error", err)
	}
}

func (s *Service) retry(ctx context.Context, work queuedWork, cause error) {
	attempt := work.Attempts + 1
	delay := 5 * time.Second
	for index := 1; index < attempt && delay < 6*time.Hour; index++ {
		delay *= 2
	}
	if delay > 6*time.Hour {
		delay = 6 * time.Hour
	}
	if upstream, ok := cause.(*upstreamError); ok && upstream.retryAfter > delay {
		delay = upstream.retryAfter
		if delay > 6*time.Hour {
			delay = 6 * time.Hour
		}
	}
	message := "sync_failed"
	switch {
	case errors.Is(cause, ErrProviderUnavailable):
		message = "provider_unavailable"
	case errors.Is(cause, ErrNotConnected):
		message = "authorization_required"
	case errors.Is(cause, ErrInvalidInput):
		message = "missing_or_invalid_mapping"
	default:
		if upstream, ok := cause.(*upstreamError); ok {
			message = fmt.Sprintf("provider_http_%d", upstream.status)
		}
	}
	s.logger.Warn("tracking work will be retried", "provider", work.Provider, "eventType", work.EventType, "attempt", attempt, "error", cause)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("begin tracking retry failed", "error", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE profile_tracking_outbox SET attempt_count = $2, next_attempt_at = now() + $3::interval, leased_until = NULL, last_error = $4 WHERE id = $1::uuid`, work.ID, attempt, delay.String(), message); err != nil {
		s.logger.Error("schedule tracking retry failed", "error", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE profile_tracking_accounts SET last_error = $3, updated_at = now() WHERE profile_id = $1::uuid AND provider = $2`, work.ProfileID, work.Provider, message); err != nil {
		s.logger.Error("update tracking error failed", "error", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit tracking retry failed", "error", err)
	}
}
