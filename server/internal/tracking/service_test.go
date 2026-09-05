package tracking

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

const (
	trackingTestProfileID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	trackingTestUserID    = "22222222-2222-4222-8222-222222222222"
)

func TestAuthorizeProfileRejectsMalformedID(t *testing.T) {
	pool := openTrackingTestPool(t)
	service := &Service{pool: pool}
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin authorization transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = service.authorizeProfile(ctx, tx, trackingTestPrincipal(), "not-a-uuid", false)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("authorize malformed profile ID: got %v, want ErrForbidden", err)
	}
}

func TestAuthorizeProfileCanonicalizesUppercaseID(t *testing.T) {
	pool := openTrackingTestPool(t)
	service := &Service{pool: pool}
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin authorization transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	profileID, err := service.authorizeProfile(ctx, tx, trackingTestPrincipal(), strings.ToUpper(trackingTestProfileID), false)
	if err != nil {
		t.Fatalf("authorize uppercase profile ID: %v", err)
	}
	if profileID != trackingTestProfileID {
		t.Fatalf("authorize uppercase profile ID: got %q, want canonical %q", profileID, trackingTestProfileID)
	}
}

func TestCompleteDeviceAuthorizationPreservesQueryError(t *testing.T) {
	pool := openTrackingTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TEMPORARY TABLE profile_tracking_authorizations (id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create malformed temporary authorizations table: %v", err)
	}

	service := &Service{pool: pool}
	_, err := service.CompleteDeviceAuthorization(ctx, trackingTestPrincipal(), trackingTestProfileID, "trakt", "33333333-3333-4333-8333-333333333333")
	if err == nil {
		t.Fatal("complete authorization unexpectedly succeeded")
	}
	if errors.Is(err, ErrAuthorizationGone) {
		t.Fatalf("query error was reported as ErrAuthorizationGone: %v", err)
	}
	if !strings.Contains(err.Error(), "query tracking authorization") {
		t.Fatalf("query error was not wrapped with operation context: %v", err)
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		t.Fatalf("wrapped error does not preserve PostgreSQL failure: %v", err)
	}
}

func TestCompleteDeviceAuthorizationReportsMissingAndExpiredAsGone(t *testing.T) {
	pool := openTrackingTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profile_tracking_authorizations (
			id uuid PRIMARY KEY,
			profile_id uuid NOT NULL,
			provider text NOT NULL,
			provider_code_encrypted bytea NOT NULL,
			cipher_version smallint NOT NULL DEFAULT 1,
			encryption_key_version integer NOT NULL DEFAULT 1,
			interval_seconds integer NOT NULL,
			expires_at timestamptz NOT NULL,
			last_polled_at timestamptz
		)
	`); err != nil {
		t.Fatalf("create temporary authorizations table: %v", err)
	}
	const expiredAuthorizationID = "33333333-3333-4333-8333-333333333333"
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_tracking_authorizations (
			id, profile_id, provider, provider_code_encrypted, interval_seconds, expires_at
		) VALUES ($1::uuid, $2::uuid, 'trakt', '\x01'::bytea, 5, $3)
	`, expiredAuthorizationID, trackingTestProfileID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("seed expired authorization: %v", err)
	}

	service := &Service{pool: pool}
	for name, authorizationID := range map[string]string{
		"missing": "44444444-4444-4444-8444-444444444444",
		"expired": expiredAuthorizationID,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.CompleteDeviceAuthorization(ctx, trackingTestPrincipal(), trackingTestProfileID, "trakt", authorizationID)
			if !errors.Is(err, ErrAuthorizationGone) {
				t.Fatalf("complete %s authorization: got %v, want ErrAuthorizationGone", name, err)
			}
		})
	}
}

func TestCompleteDeviceAuthorizationClaimsPollBeforeProviderCall(t *testing.T) {
	pool := openTrackingConcurrencyTestPool(t)
	ctx := context.Background()

	providerStarted := make(chan struct{}, 2)
	releaseProvider := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseProvider) })
	}
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		providerStarted <- struct{}{}
		<-releaseProvider
		response.WriteHeader(http.StatusInternalServerError)
	}))
	defer provider.Close()
	defer release()

	encryptionKey, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{0x42}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	firstService, err := NewService(pool, encryptionKey, "trakt-client", "trakt-secret", "", provider.Client(), nil)
	if err != nil {
		t.Fatalf("create first tracking service: %v", err)
	}
	secondService, err := NewService(pool, encryptionKey, "trakt-client", "trakt-secret", "", provider.Client(), nil)
	if err != nil {
		t.Fatalf("create second tracking service: %v", err)
	}
	firstService.client.trakt.authURL = provider.URL
	secondService.client.trakt.authURL = provider.URL

	const authorizationID = "33333333-3333-4333-8333-333333333333"
	encryptedCode, err := firstService.cipher.encrypt("provider-device-code", trackingTestProfileID+":trakt:authorization")
	if err != nil {
		t.Fatalf("encrypt provider device code: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_tracking_authorizations (
			id, profile_id, provider, provider_code_encrypted, interval_seconds, expires_at
		) VALUES ($1::uuid, $2::uuid, 'trakt', $3, 5, now() + interval '1 hour')
	`, authorizationID, trackingTestProfileID, encryptedCode); err != nil {
		t.Fatalf("seed pending authorization: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, service := range []*Service{firstService, secondService} {
		service := service
		go func() {
			<-start
			_, err := service.CompleteDeviceAuthorization(ctx, trackingTestPrincipal(), trackingTestProfileID, "trakt", authorizationID)
			results <- err
		}()
	}
	close(start)
	select {
	case <-providerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("neither service reached provider")
	}
	select {
	case err := <-results:
		if !errors.Is(err, ErrAuthorizationSlow) {
			t.Fatalf("concurrent poll error = %v, want ErrAuthorizationSlow", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent poll remained blocked while the provider call was in flight")
	}
	if calls := providerCalls.Load(); calls != 1 {
		t.Fatalf("concurrent provider calls = %d, want exactly one", calls)
	}

	release()
	select {
	case err := <-results:
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("claimed provider poll error = %v, want ErrProviderUnavailable", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("claimed provider poll did not finish")
	}

	if _, err := pool.Exec(ctx, `
		UPDATE profile_tracking_authorizations
		SET last_polled_at = now() - interval '6 seconds'
		WHERE id = $1::uuid
	`, authorizationID); err != nil {
		t.Fatalf("expire poll interval: %v", err)
	}
	if _, err := secondService.CompleteDeviceAuthorization(ctx, trackingTestPrincipal(), trackingTestProfileID, "trakt", authorizationID); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("poll after interval expiry error = %v, want ErrProviderUnavailable", err)
	}
	if calls := providerCalls.Load(); calls != 2 {
		t.Fatalf("provider calls after interval expiry = %d, want 2", calls)
	}
	if _, err := firstService.CompleteDeviceAuthorization(ctx, trackingTestPrincipal(), trackingTestProfileID, "trakt", authorizationID); !errors.Is(err, ErrAuthorizationSlow) {
		t.Fatalf("immediate poll after provider failure error = %v, want ErrAuthorizationSlow", err)
	}
	if calls := providerCalls.Load(); calls != 2 {
		t.Fatalf("provider failure reopened polling burst: calls = %d, want 2", calls)
	}
}

func TestTrackingMutationsRequireManagementAndRemainAtomic(t *testing.T) {
	pool := openTrackingTestPool(t)
	ctx := context.Background()
	categoryID := "11111111-1111-4111-8111-111111111111"
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profile_tracking_accounts (
			profile_id uuid NOT NULL,
			provider text NOT NULL,
			access_token_encrypted bytea NOT NULL,
			sync_watched boolean NOT NULL,
			sync_progress boolean NOT NULL,
			sync_library boolean NOT NULL,
			PRIMARY KEY (profile_id, provider)
		)
	`); err != nil {
		t.Fatalf("create tracking mutation fixtures: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH categorized_profile AS (
			UPDATE profiles SET category_id = $1::uuid WHERE id = $2::uuid
			RETURNING id
		), inserted_access AS (
			INSERT INTO user_profile_access (user_id, profile_id, can_manage)
			VALUES ($3::uuid, $2::uuid, false)
			RETURNING profile_id
		)
		INSERT INTO profile_tracking_accounts (
			profile_id, provider, access_token_encrypted, sync_watched, sync_progress, sync_library
		) VALUES ($2::uuid, 'trakt', '\x01'::bytea, true, true, true)
	`, categoryID, trackingTestProfileID, trackingTestUserID); err != nil {
		t.Fatalf("seed read-only tracking account: %v", err)
	}
	viewer := auth.Principal{
		UserID: trackingTestUserID, Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
	}
	service := &Service{pool: pool}
	disabled := false
	if _, err := service.BeginDeviceAuthorization(ctx, viewer, trackingTestProfileID, "trakt"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only authorization start: got %v, want ErrForbidden", err)
	}
	if _, err := service.CompleteDeviceAuthorization(ctx, viewer, trackingTestProfileID, "trakt", "33333333-3333-4333-8333-333333333333"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only authorization completion: got %v, want ErrForbidden", err)
	}
	if _, err := service.UpdatePreferences(ctx, viewer, trackingTestProfileID, "trakt", PreferencesInput{SyncWatched: &disabled}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only preference update: got %v, want ErrForbidden", err)
	}
	if err := service.Disconnect(ctx, viewer, trackingTestProfileID, "trakt"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only disconnect: got %v, want ErrForbidden", err)
	}
	var watched bool
	var accountCount int
	if err := pool.QueryRow(ctx, `
		SELECT sync_watched,
		       (SELECT count(*) FROM profile_tracking_accounts WHERE profile_id = $1::uuid AND provider = 'trakt')
		FROM profile_tracking_accounts
		WHERE profile_id = $1::uuid AND provider = 'trakt'
	`, trackingTestProfileID).Scan(&watched, &accountCount); err != nil {
		t.Fatalf("query tracking account after denied mutations: %v", err)
	}
	if !watched || accountCount != 1 {
		t.Fatalf("denied tracking mutations changed account: watched=%v count=%d", watched, accountCount)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access
		SET can_manage = true
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, trackingTestUserID, trackingTestProfileID); err != nil {
		t.Fatalf("grant tracking management: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin manager authorization transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := service.authorizeProfile(ctx, tx, viewer, trackingTestProfileID, true); err != nil {
		t.Fatalf("manager tracking authorization rejected: %v", err)
	}
}

func openTrackingTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL tracking service test")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (id uuid PRIMARY KEY, category_id uuid);
		CREATE TEMPORARY TABLE user_profile_access (profile_id uuid NOT NULL, user_id uuid NOT NULL, can_manage boolean NOT NULL DEFAULT false)
	`); err != nil {
		t.Fatalf("create tracking authorization fixtures: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profiles (id) VALUES ($1::uuid)`, trackingTestProfileID); err != nil {
		t.Fatalf("seed tracking authorization fixtures: %v", err)
	}
	return pool
}

func openTrackingConcurrencyTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL tracking concurrency test")
	}

	schema := fmt.Sprintf("tracking_poll_claim_%d", time.Now().UnixNano())
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 2
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open concurrent test database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})

	ctx := context.Background()
	fixtures := fmt.Sprintf(`
		CREATE SCHEMA %[1]s;
		CREATE TABLE %[1]s.profiles (
			id uuid PRIMARY KEY,
			category_id uuid
		);
		CREATE TABLE %[1]s.user_profile_access (
			profile_id uuid NOT NULL,
			user_id uuid NOT NULL,
			can_manage boolean NOT NULL DEFAULT false
		);
		CREATE TABLE %[1]s.profile_tracking_authorizations (
			id uuid PRIMARY KEY,
			profile_id uuid NOT NULL,
			provider text NOT NULL,
			provider_code_encrypted bytea NOT NULL,
			cipher_version smallint NOT NULL DEFAULT 1,
			encryption_key_version integer NOT NULL DEFAULT 1,
			interval_seconds integer NOT NULL,
			expires_at timestamptz NOT NULL,
			last_polled_at timestamptz
		);
	`, schema)
	if _, err := pool.Exec(ctx, fixtures); err != nil {
		t.Fatalf("create concurrent tracking fixtures: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profiles (id) VALUES ($1::uuid)`, trackingTestProfileID); err != nil {
		t.Fatalf("seed concurrent tracking fixtures: %v", err)
	}
	return pool
}

func trackingTestPrincipal() auth.Principal {
	return auth.Principal{
		UserID: trackingTestUserID, Role: "admin",
		AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}
}

func TestOutboxCoalescesLatestPendingIntent(t *testing.T) {
	pool := openTrackingOutboxTestPool(t, 2)
	seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
	service := &Service{pool: pool, logger: slog.Default()}
	ctx := context.Background()
	titleID := trackingOutboxTitleID(1)
	var firstSequence int64

	for version := 1; version <= 500; version++ {
		event := Event{
			Type: "progress", TitleID: titleID, PositionSeconds: version,
			DurationSeconds: 500, Version: int64(version), OccurredAt: time.Now().UTC(),
		}
		if err := service.Enqueue(ctx, trackingTestProfileID, titleID, fmt.Sprintf("progress:%s:%d", titleID, version), event); err != nil {
			t.Fatalf("enqueue progress version %d: %v", version, err)
		}
		if version == 1 {
			if err := pool.QueryRow(ctx, `SELECT enqueue_sequence FROM profile_tracking_outbox`).Scan(&firstSequence); err != nil {
				t.Fatalf("query first enqueue sequence: %v", err)
			}
			if _, err := pool.Exec(ctx, `
				UPDATE profile_tracking_outbox
				SET attempt_count = 4,
				    next_attempt_at = now() + interval '1 hour',
				    last_error = 'provider_http_500'
			`); err != nil {
				t.Fatalf("seed pending retry state: %v", err)
			}
		}
	}

	var count, version, attempts int
	var key string
	var latestSequence int64
	var due, errorCleared bool
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(idempotency_key), max((payload->>'version')::integer),
		       max(enqueue_sequence), max(attempt_count),
		       bool_and(next_attempt_at <= now()), bool_and(last_error IS NULL)
		FROM profile_tracking_outbox
	`).Scan(&count, &key, &version, &latestSequence, &attempts, &due, &errorCleared); err != nil {
		t.Fatalf("query coalesced outbox: %v", err)
	}
	if count != 1 || key != fmt.Sprintf("progress:%s:500", titleID) || version != 500 ||
		latestSequence <= firstSequence || attempts != 0 || !due || !errorCleared {
		t.Fatalf("coalesced outbox count=%d key=%q version=%d sequence=%d first=%d attempts=%d due=%v errorCleared=%v",
			count, key, version, latestSequence, firstSequence, attempts, due, errorCleared)
	}
}

func TestOutboxPreservesLeasedPredecessorAndCrossEventOrder(t *testing.T) {
	pool := openTrackingOutboxTestPool(t, 2)
	seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
	service := &Service{pool: pool, logger: slog.Default()}
	ctx := context.Background()
	titleID := trackingOutboxTitleID(2)

	if err := service.Enqueue(ctx, trackingTestProfileID, titleID, "progress:old", Event{
		Type: "progress", TitleID: titleID, PositionSeconds: 10, DurationSeconds: 100, Version: 1,
	}); err != nil {
		t.Fatalf("enqueue leased predecessor: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE profile_tracking_outbox SET leased_until = now() + interval '1 minute'`); err != nil {
		t.Fatalf("lease predecessor: %v", err)
	}
	if err := service.Enqueue(ctx, trackingTestProfileID, titleID, "progress:new", Event{
		Type: "progress", TitleID: titleID, PositionSeconds: 20, DurationSeconds: 100, Version: 2,
	}); err != nil {
		t.Fatalf("enqueue successor: %v", err)
	}
	var total, leased, pending int
	if err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE leased_until IS NOT NULL),
		       count(*) FILTER (WHERE leased_until IS NULL)
		FROM profile_tracking_outbox
		WHERE profile_id = $1::uuid AND provider = 'trakt' AND title_id = $2::uuid
	`, trackingTestProfileID, titleID).Scan(&total, &leased, &pending); err != nil {
		t.Fatalf("count leased and pending work: %v", err)
	}
	if total != 2 || leased != 1 || pending != 1 {
		t.Fatalf("leased/pending cardinality total=%d leased=%d pending=%d", total, leased, pending)
	}

	otherTitleID := trackingOutboxTitleID(3)
	if err := service.Enqueue(ctx, trackingTestProfileID, otherTitleID, "watched:first", Event{
		Type: "watched", TitleID: otherTitleID, Completed: true, Version: 1,
	}); err != nil {
		t.Fatalf("enqueue watched head: %v", err)
	}
	if err := service.Enqueue(ctx, trackingTestProfileID, otherTitleID, "progress:completed", Event{
		Type: "progress", TitleID: otherTitleID, Completed: true, Version: 2,
	}); err != nil {
		t.Fatalf("enqueue completed progress head: %v", err)
	}
	var eventType, key string
	if err := pool.QueryRow(ctx, `
		SELECT event_type, idempotency_key
		FROM profile_tracking_outbox
		WHERE title_id = $1::uuid AND leased_until IS NULL
	`, otherTitleID).Scan(&eventType, &key); err != nil {
		t.Fatalf("query completed progress ordering: %v", err)
	}
	if eventType != "progress" || key != "progress:completed" {
		t.Fatalf("newer completed progress did not supersede watched: type=%q key=%q", eventType, key)
	}
	if err := service.Enqueue(ctx, trackingTestProfileID, otherTitleID, "watched:last", Event{
		Type: "watched", TitleID: otherTitleID, Completed: false, Version: 3,
	}); err != nil {
		t.Fatalf("enqueue final watched head: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT event_type, idempotency_key
		FROM profile_tracking_outbox
		WHERE title_id = $1::uuid AND leased_until IS NULL
	`, otherTitleID).Scan(&eventType, &key); err != nil {
		t.Fatalf("query watched ordering: %v", err)
	}
	if eventType != "watched" || key != "watched:last" {
		t.Fatalf("newer watched did not supersede completed progress: type=%q key=%q", eventType, key)
	}
}

func TestRetryDeletesSupersededLeasedWork(t *testing.T) {
	pool := openTrackingOutboxTestPool(t, 2)
	seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
	service := &Service{pool: pool, logger: slog.Default()}
	ctx := context.Background()
	titleID := trackingOutboxTitleID(4)
	if err := service.Enqueue(ctx, trackingTestProfileID, titleID, "progress:old", Event{Type: "progress", TitleID: titleID, Version: 1}); err != nil {
		t.Fatalf("enqueue old work: %v", err)
	}
	var old queuedWork
	if err := pool.QueryRow(ctx, `
		UPDATE profile_tracking_outbox
		SET leased_until = now() + interval '1 minute'
		RETURNING id::text, profile_id::text, provider, title_id::text, event_type, idempotency_key, payload, attempt_count
	`).Scan(&old.ID, &old.ProfileID, &old.Provider, &old.TitleID, &old.EventType, &old.IdempotencyKey, &old.Payload, &old.Attempts); err != nil {
		t.Fatalf("lease old work: %v", err)
	}
	if err := service.Enqueue(ctx, trackingTestProfileID, titleID, "progress:new", Event{Type: "progress", TitleID: titleID, Version: 2}); err != nil {
		t.Fatalf("enqueue new work: %v", err)
	}
	service.retry(ctx, old, errors.New("provider failed"))

	var count int
	var key string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(idempotency_key) FROM profile_tracking_outbox`).Scan(&count, &key); err != nil {
		t.Fatalf("query retry outcome: %v", err)
	}
	if count != 1 || key != "progress:new" {
		t.Fatalf("superseded retry left count=%d key=%q", count, key)
	}
}

func TestOutboxCapacityIsExactAndRollsBackCallerMutation(t *testing.T) {
	pool := openTrackingOutboxTestPool(t, 2)
	seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
	service := &Service{
		pool: pool, logger: slog.Default(),
		profileProviderOutboxLimit: 2, globalOutboxLimit: 2,
	}
	ctx := context.Background()
	for index := 1; index <= 2; index++ {
		titleID := trackingOutboxTitleID(10 + index)
		if err := service.Enqueue(ctx, trackingTestProfileID, titleID, fmt.Sprintf("progress:%d", index), Event{
			Type: "progress", TitleID: titleID, Version: int64(index),
		}); err != nil {
			t.Fatalf("fill capacity slot %d: %v", index, err)
		}
	}
	firstTitleID := trackingOutboxTitleID(11)
	if err := service.Enqueue(ctx, trackingTestProfileID, firstTitleID, "progress:coalesced", Event{
		Type: "progress", TitleID: firstTitleID, Version: 3,
	}); err != nil {
		t.Fatalf("coalescence at capacity was rejected: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin caller mutation: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE mutation_probe SET version = version + 1`); err != nil {
		t.Fatalf("mutate caller state: %v", err)
	}
	rejectedTitleID := trackingOutboxTitleID(13)
	err = service.EnqueueTx(ctx, tx, trackingTestProfileID, rejectedTitleID, "progress:rejected", Event{
		Type: "progress", TitleID: rejectedTitleID, Version: 1,
	})
	if !errors.Is(err, ErrOutboxCapacity) {
		t.Fatalf("N+1 enqueue error=%v, want ErrOutboxCapacity", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback rejected caller mutation: %v", err)
	}
	var mutationVersion, outboxCount, rejectedHeads int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT version FROM mutation_probe),
		       (SELECT count(*) FROM profile_tracking_outbox),
		       (SELECT count(*) FROM profile_tracking_event_heads WHERE title_id = $1::uuid)
	`, rejectedTitleID).Scan(&mutationVersion, &outboxCount, &rejectedHeads); err != nil {
		t.Fatalf("query rollback state: %v", err)
	}
	if mutationVersion != 0 || outboxCount != 2 || rejectedHeads != 0 {
		t.Fatalf("capacity rollback mutation=%d outbox=%d rejectedHeads=%d", mutationVersion, outboxCount, rejectedHeads)
	}
}

func TestConcurrentAdmissionsSerializeTheLastGlobalSlot(t *testing.T) {
	pool := openTrackingOutboxTestPool(t, 4)
	seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
	service := &Service{
		pool: pool, logger: slog.Default(),
		profileProviderOutboxLimit: 10, globalOutboxLimit: 1,
	}
	ctx := context.Background()
	transactions := make([]pgx.Tx, 2)
	for index := range transactions {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin concurrent admission transaction %d: %v", index, err)
		}
		transactions[index] = tx
	}
	start := make(chan struct{})
	results := make(chan error, len(transactions))
	for index, tx := range transactions {
		go func(index int, tx pgx.Tx) {
			<-start
			titleID := trackingOutboxTitleID(21 + index)
			err := service.EnqueueTx(ctx, tx, trackingTestProfileID, titleID, fmt.Sprintf("progress:concurrent:%d", index), Event{
				Type: "progress", TitleID: titleID, Version: int64(index + 1),
			})
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
			results <- err
		}(index, tx)
	}
	close(start)
	successes, capacityErrors := 0, 0
	for range transactions {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrOutboxCapacity):
			capacityErrors++
		default:
			t.Fatalf("unexpected concurrent admission error: %v", err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_tracking_outbox`).Scan(&count); err != nil {
		t.Fatalf("count concurrent admissions: %v", err)
	}
	if successes != 1 || capacityErrors != 1 || count != 1 {
		t.Fatalf("concurrent last slot successes=%d capacity=%d count=%d", successes, capacityErrors, count)
	}
}

func TestBatchAdmissionAcrossProvidersIsAllOrNothing(t *testing.T) {
	pool := openTrackingOutboxTestPool(t, 2)
	seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
	seedTrackingAccount(t, pool, trackingTestProfileID, "simkl")
	service := &Service{
		pool: pool, logger: slog.Default(),
		profileProviderOutboxLimit: 1, globalOutboxLimit: 10,
	}
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	seedTitleID := trackingOutboxTitleID(30)
	if err := service.enqueueWithProvider(ctx, tx, trackingTestProfileID, "trakt", seedTitleID, "library:seed", Event{
		Type: "library", TitleID: seedTitleID, InLibrary: true,
	}); err != nil {
		t.Fatalf("seed provider capacity: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit provider capacity seed: %v", err)
	}

	batchTitleID := trackingOutboxTitleID(31)
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin batch transaction: %v", err)
	}
	err = service.EnqueueBatchTx(ctx, tx, trackingTestProfileID, []BatchEvent{{
		TitleID: batchTitleID, IdempotencyKey: "library:batch",
		Event: Event{Type: "library", TitleID: batchTitleID, InLibrary: true},
	}})
	if !errors.Is(err, ErrOutboxCapacity) {
		t.Fatalf("multi-provider batch error=%v, want ErrOutboxCapacity", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback rejected batch: %v", err)
	}
	var traktCount, simklCount, batchHeads int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE provider = 'trakt'),
		       count(*) FILTER (WHERE provider = 'simkl'),
		       (SELECT count(*) FROM profile_tracking_event_heads WHERE title_id = $1::uuid)
		FROM profile_tracking_outbox
	`, batchTitleID).Scan(&traktCount, &simklCount, &batchHeads); err != nil {
		t.Fatalf("query rejected batch state: %v", err)
	}
	if traktCount != 1 || simklCount != 0 || batchHeads != 0 {
		t.Fatalf("partial batch persisted trakt=%d simkl=%d heads=%d", traktCount, simklCount, batchHeads)
	}
}

func TestAdmissionEvictsOnlyProvenStalePendingWork(t *testing.T) {
	pool := openTrackingOutboxTestPool(t, 2)
	seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
	service := &Service{pool: pool, logger: slog.Default(), profileProviderOutboxLimit: 3, globalOutboxLimit: 3}
	ctx := context.Background()
	titleID := trackingOutboxTitleID(40)
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_tracking_event_heads (
			profile_id, provider, title_id, event_type, idempotency_key, affects_watched, updated_at
		) VALUES
			($1::uuid, 'trakt', $2::uuid, 'progress', 'progress:stale', true, now() - interval '1 minute'),
			($1::uuid, 'trakt', $2::uuid, 'watched', 'watched:head', true, now())
	`, trackingTestProfileID, titleID); err != nil {
		t.Fatalf("seed stale heads: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_tracking_outbox (
			profile_id, provider, title_id, event_type, payload, idempotency_key, leased_until
		) VALUES
			($1::uuid, 'trakt', $2::uuid, 'progress', '{"type":"progress"}', 'progress:stale', NULL),
			($1::uuid, 'trakt', $2::uuid, 'progress', '{"type":"progress"}', 'progress:leased', now() + interval '1 minute'),
			($1::uuid, 'trakt', $2::uuid, 'watched', '{"type":"watched"}', 'watched:head', NULL)
	`, trackingTestProfileID, titleID); err != nil {
		t.Fatalf("seed stale outbox: %v", err)
	}
	newTitleID := trackingOutboxTitleID(41)
	if err := service.Enqueue(ctx, trackingTestProfileID, newTitleID, "library:new", Event{
		Type: "library", TitleID: newTitleID, InLibrary: true,
	}); err != nil {
		t.Fatalf("enqueue while evicting stale work: %v", err)
	}
	var stale, head, leased int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE idempotency_key = 'progress:stale'),
		       count(*) FILTER (WHERE idempotency_key = 'watched:head'),
		       count(*) FILTER (WHERE idempotency_key = 'progress:leased')
		FROM profile_tracking_outbox
	`).Scan(&stale, &head, &leased); err != nil {
		t.Fatalf("query selective eviction: %v", err)
	}
	if stale != 0 || head != 1 || leased != 1 {
		t.Fatalf("selective eviction stale=%d head=%d leased=%d", stale, head, leased)
	}
}

func TestVariantTrackingAdmissionRejectsCommittablyWithoutMutationOrCapacityUse(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		pool := openTrackingOutboxTestPool(t, 2)
		seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
		canonicalID := trackingOutboxTitleID(201)
		variantID := trackingOutboxTitleID(202)
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO titles (id, hierarchy_variant) VALUES ($1::uuid, ''), ($2::uuid, 'tvdb:2')
		`, canonicalID, variantID); err != nil {
			t.Fatalf("seed tracking titles: %v", err)
		}
		service := &Service{pool: pool, logger: slog.Default(), profileProviderOutboxLimit: 1, globalOutboxLimit: 1}
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatalf("begin variant tracking transaction: %v", err)
		}
		err = service.EnqueueTx(t.Context(), tx, trackingTestProfileID, variantID, "progress:variant", Event{
			Type: "progress", TitleID: variantID, Version: 1,
		})
		if !errors.Is(err, ErrInvalidInput) {
			_ = tx.Rollback(t.Context())
			t.Fatalf("variant enqueue error=%v, want ErrInvalidInput", err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatalf("commit rejected variant transaction: %v", err)
		}
		var heads, outbox int
		if err := pool.QueryRow(t.Context(), `
			SELECT (SELECT count(*) FROM profile_tracking_event_heads),
			       (SELECT count(*) FROM profile_tracking_outbox)
		`).Scan(&heads, &outbox); err != nil {
			t.Fatalf("query rejected variant state: %v", err)
		}
		if heads != 0 || outbox != 0 {
			t.Fatalf("rejected variant left heads=%d outbox=%d", heads, outbox)
		}
		if err := service.Enqueue(t.Context(), trackingTestProfileID, canonicalID, "progress:canonical", Event{
			Type: "progress", TitleID: canonicalID, Version: 1,
		}); err != nil {
			t.Fatalf("canonical enqueue after rejected variant consumed capacity: %v", err)
		}
		if err := pool.QueryRow(t.Context(), `
			SELECT (SELECT count(*) FROM profile_tracking_event_heads),
			       (SELECT count(*) FROM profile_tracking_outbox)
		`).Scan(&heads, &outbox); err != nil {
			t.Fatalf("query canonical tracking state: %v", err)
		}
		if heads != 1 || outbox != 1 {
			t.Fatalf("canonical enqueue left heads=%d outbox=%d", heads, outbox)
		}
	})

	t.Run("mixed batch", func(t *testing.T) {
		pool := openTrackingOutboxTestPool(t, 2)
		seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
		canonicalID := trackingOutboxTitleID(203)
		variantID := trackingOutboxTitleID(204)
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO titles (id, hierarchy_variant) VALUES ($1::uuid, ''), ($2::uuid, 'tvdb:3')
		`, canonicalID, variantID); err != nil {
			t.Fatalf("seed mixed tracking titles: %v", err)
		}
		service := &Service{pool: pool, logger: slog.Default(), profileProviderOutboxLimit: 1, globalOutboxLimit: 1}
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatalf("begin mixed tracking transaction: %v", err)
		}
		err = service.EnqueueBatchTx(t.Context(), tx, trackingTestProfileID, []BatchEvent{
			{TitleID: canonicalID, IdempotencyKey: "watched:canonical", Event: Event{Type: "watched", TitleID: canonicalID}},
			{TitleID: variantID, IdempotencyKey: "watched:variant", Event: Event{Type: "watched", TitleID: variantID}},
		})
		if !errors.Is(err, ErrInvalidInput) {
			_ = tx.Rollback(t.Context())
			t.Fatalf("mixed variant batch error=%v, want ErrInvalidInput", err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatalf("commit rejected mixed batch transaction: %v", err)
		}
		var heads, outbox int
		if err := pool.QueryRow(t.Context(), `
			SELECT (SELECT count(*) FROM profile_tracking_event_heads),
			       (SELECT count(*) FROM profile_tracking_outbox)
		`).Scan(&heads, &outbox); err != nil {
			t.Fatalf("query rejected mixed batch state: %v", err)
		}
		if heads != 0 || outbox != 0 {
			t.Fatalf("rejected mixed batch left heads=%d outbox=%d", heads, outbox)
		}
		if err := service.Enqueue(t.Context(), trackingTestProfileID, canonicalID, "watched:canonical", Event{
			Type: "watched", TitleID: canonicalID,
		}); err != nil {
			t.Fatalf("canonical enqueue after rejected mixed batch consumed capacity: %v", err)
		}
	})
}

func TestProcessAvailableDrainsMoreThanTwentyDueRows(t *testing.T) {
	pool := openTrackingOutboxTestPool(t, 2)
	seedTrackingAccount(t, pool, trackingTestProfileID, "trakt")
	service := &Service{pool: pool, logger: slog.Default()}
	ctx := context.Background()
	for index := range 25 {
		titleID := trackingOutboxTitleID(100 + index)
		if _, err := pool.Exec(ctx, `
			INSERT INTO profile_tracking_outbox (
				profile_id, provider, title_id, event_type, payload, idempotency_key
			) VALUES ($1::uuid, 'trakt', $2::uuid, 'progress', '{"type":"progress"}', $3)
		`, trackingTestProfileID, titleID, fmt.Sprintf("stale:%d", index)); err != nil {
			t.Fatalf("seed due row %d: %v", index, err)
		}
	}
	futureTitleID := trackingOutboxTitleID(999)
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_tracking_outbox (
			profile_id, provider, title_id, event_type, payload, idempotency_key, next_attempt_at
		) VALUES ($1::uuid, 'trakt', $2::uuid, 'progress', '{"type":"progress"}', 'future', now() + interval '1 hour')
	`, trackingTestProfileID, futureTitleID); err != nil {
		t.Fatalf("seed future row: %v", err)
	}
	service.processAvailable(ctx)
	var count int
	var key string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(idempotency_key) FROM profile_tracking_outbox`).Scan(&count, &key); err != nil {
		t.Fatalf("count drained outbox: %v", err)
	}
	if count != 1 || key != "future" {
		t.Fatalf("processAvailable left count=%d key=%q after draining 25 due rows", count, key)
	}
}

func openTrackingOutboxTestPool(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run PostgreSQL tracking outbox tests")
	}
	schema := fmt.Sprintf("tracking_outbox_%d", time.Now().UnixNano())
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse tracking outbox database URL: %v", err)
	}
	config.MaxConns = maxConns
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open tracking outbox database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	if _, err := pool.Exec(context.Background(), fmt.Sprintf(`
		CREATE SCHEMA %[1]s;
		SET search_path TO %[1]s, public;
		CREATE TABLE titles (
			id uuid PRIMARY KEY,
			hierarchy_variant text NOT NULL DEFAULT ''
		);
		CREATE TABLE profile_tracking_accounts (
			profile_id uuid NOT NULL,
			provider text NOT NULL,
			sync_watched boolean NOT NULL DEFAULT true,
			sync_progress boolean NOT NULL DEFAULT true,
			sync_library boolean NOT NULL DEFAULT true,
			last_success_at timestamptz,
			last_error text,
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, provider)
		);
		CREATE TABLE profile_tracking_event_heads (
			profile_id uuid NOT NULL,
			provider text NOT NULL,
			title_id uuid NOT NULL,
			event_type text NOT NULL,
			idempotency_key text NOT NULL,
			affects_watched boolean NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
			PRIMARY KEY (profile_id, provider, title_id, event_type)
		);
		CREATE TABLE profile_tracking_outbox (
			enqueue_sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			profile_id uuid NOT NULL,
			provider text NOT NULL,
			title_id uuid NOT NULL,
			event_type text NOT NULL,
			payload jsonb NOT NULL,
			idempotency_key text NOT NULL,
			attempt_count integer NOT NULL DEFAULT 0,
			next_attempt_at timestamptz NOT NULL DEFAULT now(),
			leased_until timestamptz,
			last_error text,
			created_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE UNIQUE INDEX profile_tracking_outbox_pending_event_idx
			ON profile_tracking_outbox (profile_id, provider, title_id, event_type)
			WHERE leased_until IS NULL;
		CREATE INDEX profile_tracking_outbox_predecessor_idx
			ON profile_tracking_outbox (profile_id, provider, title_id, enqueue_sequence);
		CREATE TABLE mutation_probe (version integer NOT NULL);
		INSERT INTO mutation_probe VALUES (0);
	`, schema)); err != nil {
		t.Fatalf("create tracking outbox fixtures: %v", err)
	}
	return pool
}

func seedTrackingAccount(t *testing.T, pool *pgxpool.Pool, profileID, provider string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO profile_tracking_accounts (profile_id, provider)
		VALUES ($1::uuid, $2)
	`, profileID, provider); err != nil {
		t.Fatalf("seed %s tracking account: %v", provider, err)
	}
}

func trackingOutboxTitleID(index int) string {
	return fmt.Sprintf("550e8400-e29b-41d4-a716-%012d", index)
}
