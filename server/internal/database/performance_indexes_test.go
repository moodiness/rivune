package database

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBackendHotpathMigrationDefinesCoveringIndexes(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000045_backend_hotpath_indexes.sql")
	if err != nil {
		t.Fatalf("read backend hotpath migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	for _, statement := range []string{
		"CREATE INDEX IF NOT EXISTS profile_tracking_outbox_predecessor_idx ON profile_tracking_outbox (profile_id, provider, title_id, enqueue_sequence)",
		"CREATE INDEX IF NOT EXISTS auth_sessions_revoked_cleanup_idx ON auth_sessions (revoked_at, id) WHERE revoked_at IS NOT NULL",
		"CREATE INDEX IF NOT EXISTS auth_sessions_refresh_expiry_cleanup_idx ON auth_sessions (refresh_expires_at, id) WHERE revoked_at IS NULL",
		"CREATE INDEX IF NOT EXISTS auth_session_notifications_expiry_cleanup_idx ON auth_session_notifications (expires_at, id)",
		"CREATE INDEX IF NOT EXISTS introdb_segment_cache_expiry_cleanup_idx ON introdb_segment_cache (expires_at, imdb_id, season_number, episode_number)",
		"CREATE INDEX IF NOT EXISTS fanart_response_cache_expiry_cleanup_idx ON fanart_response_cache (expires_at, resource_type, external_id, language)",
		"CREATE INDEX IF NOT EXISTS fanart_response_cache_capacity_cleanup_idx ON fanart_response_cache (updated_at DESC, resource_type, external_id, language)",
		"DROP INDEX IF EXISTS auth_session_notifications_expires_at_idx",
		"DROP INDEX IF EXISTS introdb_segment_cache_expiry_idx",
		"DROP INDEX IF EXISTS fanart_response_cache_expires_at_idx",
	} {
		if !strings.Contains(normalized, statement) {
			t.Fatalf("migration lacks covering index %q: %s", statement, normalized)
		}
	}
}

func TestNativePairingPersistenceMigrationExtendsActiveSessionsAndDefinesCleanupIndex(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000078_consumed_refresh_token_cleanup.sql")
	if err != nil {
		t.Fatalf("read native pairing persistence migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	for _, statement := range []string{
		"SET refresh_expires_at = '9999-12-31 23:59:59+00'::timestamptz",
		"session.authorization_scope = 'category'",
		"session.revoked_at IS NULL",
		"session.refresh_expires_at > now()",
		"device.approved_at IS NOT NULL",
		"device.platform IN ('android', 'android_tv', 'ios', 'tvos', 'visionos', 'macos', 'apple', 'windows')",
		"SET expires_at = '9999-12-31 23:59:59+00'::timestamptz",
		"token.consumed_at IS NULL",
		"CREATE INDEX auth_refresh_tokens_consumed_cleanup_idx ON auth_refresh_tokens (consumed_at, token_hash) WHERE consumed_at IS NOT NULL;",
	} {
		if !strings.Contains(normalized, statement) {
			t.Fatalf("native pairing persistence migration lacks %q: %s", statement, normalized)
		}
	}
}

func TestTrackingOutboxBoundsMigrationDefinesAdmissionIndexes(t *testing.T) {
	contents, err := migrationFiles.ReadFile("migrations/000052_tracking_outbox_bounds.sql")
	if err != nil {
		t.Fatalf("read tracking outbox bounds migration: %v", err)
	}
	normalized := strings.Join(strings.Fields(string(contents)), " ")
	for _, statement := range []string{
		"ALTER TABLE profile_tracking_outbox DROP CONSTRAINT IF EXISTS profile_tracking_outbox_profile_id_provider_idempotency_key_key",
		"CREATE UNIQUE INDEX IF NOT EXISTS profile_tracking_outbox_pending_event_idx ON profile_tracking_outbox (profile_id, provider, title_id, event_type) WHERE leased_until IS NULL",
	} {
		if !strings.Contains(normalized, statement) {
			t.Fatalf("migration lacks tracking admission index %q: %s", statement, normalized)
		}
	}
}

func TestTrackingOutboxBoundsMigrationDeduplicatesPendingByHeadThenSequence(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run tracking migration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open tracking migration database: %v", err)
	}
	defer pool.Close()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire tracking migration connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `
		CREATE TEMP TABLE profile_tracking_event_heads (
			profile_id integer NOT NULL,
			provider text NOT NULL,
			title_id integer NOT NULL,
			event_type text NOT NULL,
			idempotency_key text NOT NULL
		);
		CREATE TEMP TABLE profile_tracking_outbox (
			id integer PRIMARY KEY,
			profile_id integer NOT NULL,
			provider text NOT NULL,
			title_id integer NOT NULL,
			event_type text NOT NULL,
			idempotency_key text NOT NULL,
			enqueue_sequence bigint NOT NULL,
			leased_until timestamptz
		);
		INSERT INTO profile_tracking_event_heads
		VALUES (1, 'trakt', 1, 'progress', 'head');
		INSERT INTO profile_tracking_outbox VALUES
			(1, 1, 'trakt', 1, 'progress', 'head', 1, NULL),
			(2, 1, 'trakt', 1, 'progress', 'newer-but-stale', 2, NULL),
			(3, 1, 'trakt', 2, 'library', 'older', 3, NULL),
			(4, 1, 'trakt', 2, 'library', 'newest', 4, NULL),
			(5, 1, 'trakt', 1, 'progress', 'leased', 5, now() + interval '1 minute');
	`); err != nil {
		t.Fatalf("seed tracking migration fixtures: %v", err)
	}
	contents, err := migrationFiles.ReadFile("migrations/000052_tracking_outbox_bounds.sql")
	if err != nil {
		t.Fatalf("read tracking bounds migration: %v", err)
	}
	if _, err := connection.Exec(ctx, string(contents)); err != nil {
		t.Fatalf("apply tracking bounds migration: %v", err)
	}
	rows, err := connection.Query(ctx, `
		SELECT idempotency_key
		FROM profile_tracking_outbox
		ORDER BY enqueue_sequence
	`)
	if err != nil {
		t.Fatalf("query migrated tracking outbox: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan migrated tracking outbox: %v", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated tracking outbox: %v", err)
	}
	if strings.Join(keys, ",") != "head,newest,leased" {
		t.Fatalf("migration retained %v, want head, newest, and leased predecessor", keys)
	}
}

func TestBackendHotpathIndexesAreSelectedByPostgres(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run index plan tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open index plan database: %v", err)
	}
	defer pool.Close()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire index plan connection: %v", err)
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, `
		CREATE TEMP TABLE profile_tracking_outbox (
			id bigint PRIMARY KEY,
			profile_id integer NOT NULL,
			provider text NOT NULL,
			title_id integer NOT NULL,
			event_type text NOT NULL,
			enqueue_sequence bigint NOT NULL,
			next_attempt_at timestamptz NOT NULL,
			leased_until timestamptz
		);
		CREATE INDEX profile_tracking_outbox_due_idx
			ON profile_tracking_outbox (next_attempt_at, enqueue_sequence);
		CREATE INDEX profile_tracking_outbox_predecessor_idx
			ON profile_tracking_outbox (profile_id, provider, title_id, enqueue_sequence);
		CREATE UNIQUE INDEX profile_tracking_outbox_pending_event_idx
			ON profile_tracking_outbox (profile_id, provider, title_id, event_type)
			WHERE leased_until IS NULL;
		INSERT INTO profile_tracking_outbox
		SELECT value, value % 20, 'trakt', value, 'progress', value, now() - interval '1 minute', NULL
		FROM generate_series(1, 20000) value;

		CREATE TEMP TABLE auth_sessions (
			id bigint PRIMARY KEY,
			revoked_at timestamptz,
			refresh_expires_at timestamptz NOT NULL
		);
		CREATE INDEX auth_sessions_revoked_cleanup_idx
			ON auth_sessions (revoked_at, id) WHERE revoked_at IS NOT NULL;
		CREATE INDEX auth_sessions_refresh_expiry_cleanup_idx
			ON auth_sessions (refresh_expires_at, id) WHERE revoked_at IS NULL;
		INSERT INTO auth_sessions
		SELECT value,
			CASE WHEN value % 3 = 0 THEN now() - make_interval(secs => value) END,
			CASE WHEN value % 2 = 0 THEN now() - interval '1 hour' ELSE now() + interval '1 hour' END
		FROM generate_series(1, 20000) value;

		CREATE TEMP TABLE auth_session_notifications (
			id bigint PRIMARY KEY,
			expires_at timestamptz NOT NULL
		);
		CREATE INDEX auth_session_notifications_expiry_cleanup_idx
			ON auth_session_notifications (expires_at, id);
		INSERT INTO auth_session_notifications
		SELECT value, now() - make_interval(secs => value)
		FROM generate_series(1, 20000) value;

		CREATE TEMP TABLE introdb_segment_cache (
			imdb_id text NOT NULL,
			season_number integer NOT NULL,
			episode_number integer NOT NULL,
			expires_at timestamptz NOT NULL
		);
		CREATE INDEX introdb_segment_cache_expiry_cleanup_idx
			ON introdb_segment_cache (expires_at, imdb_id, season_number, episode_number);
		INSERT INTO introdb_segment_cache
		SELECT 'tt' || lpad(value::text, 7, '0'), 1, value, now() - make_interval(secs => value)
		FROM generate_series(1, 20000) value;

		CREATE TEMP TABLE fanart_response_cache (
			resource_type text NOT NULL,
			external_id text NOT NULL,
			language text NOT NULL,
			expires_at timestamptz NOT NULL,
			updated_at timestamptz NOT NULL
		);
		CREATE INDEX fanart_response_cache_expiry_cleanup_idx
			ON fanart_response_cache (expires_at, resource_type, external_id, language);
		CREATE INDEX fanart_response_cache_capacity_cleanup_idx
			ON fanart_response_cache (updated_at DESC, resource_type, external_id, language);
		INSERT INTO fanart_response_cache
		SELECT 'movie', value::text, 'en', now() - make_interval(secs => value), now() - make_interval(secs => value)
		FROM generate_series(1, 20000) value;
		ANALYZE profile_tracking_outbox;
		ANALYZE auth_sessions;
		ANALYZE auth_session_notifications;
		ANALYZE introdb_segment_cache;
		ANALYZE fanart_response_cache;
		SET enable_seqscan = off;
	`); err != nil {
		t.Fatalf("prepare index plan fixtures: %v", err)
	}

	plans := map[string]struct {
		query string
		index string
	}{
		"tracking predecessor": {
			query: `
				SELECT candidate.id
				FROM profile_tracking_outbox candidate
				WHERE candidate.next_attempt_at <= now()
				  AND (candidate.leased_until IS NULL OR candidate.leased_until <= now())
				  AND NOT EXISTS (
					SELECT 1 FROM profile_tracking_outbox earlier
					WHERE earlier.profile_id = candidate.profile_id
					  AND earlier.provider = candidate.provider
					  AND earlier.title_id = candidate.title_id
					  AND earlier.enqueue_sequence < candidate.enqueue_sequence
				  )
				ORDER BY candidate.enqueue_sequence
				LIMIT 1`,
			index: "profile_tracking_outbox_predecessor_idx",
		},
		"tracking pending coalescence": {
			query: "SELECT id FROM profile_tracking_outbox WHERE profile_id = 7 AND provider = 'trakt' AND title_id = 407 AND event_type = 'progress' AND leased_until IS NULL",
			index: "profile_tracking_outbox_pending_event_idx",
		},
		"tracking profile provider count": {
			query: "SELECT count(*) FROM profile_tracking_outbox WHERE profile_id = 7 AND provider = 'trakt'",
			index: "profile_tracking_outbox_predecessor_idx",
		},
		"revoked sessions": {
			query: "SELECT id FROM auth_sessions WHERE revoked_at IS NOT NULL ORDER BY revoked_at, id LIMIT 500",
			index: "auth_sessions_revoked_cleanup_idx",
		},
		"expired refresh sessions": {
			query: "SELECT id FROM auth_sessions WHERE revoked_at IS NULL AND refresh_expires_at <= now() ORDER BY refresh_expires_at, id LIMIT 500",
			index: "auth_sessions_refresh_expiry_cleanup_idx",
		},
		"consumed refresh tokens": {
			query: "SELECT token_hash FROM auth_refresh_tokens WHERE consumed_at <= now() - interval '30 days' ORDER BY consumed_at, token_hash LIMIT 500",
			index: "auth_refresh_tokens_consumed_cleanup_idx",
		},
		"expired notifications": {
			query: "SELECT id FROM auth_session_notifications WHERE expires_at <= now() ORDER BY expires_at, id LIMIT 500",
			index: "auth_session_notifications_expiry_cleanup_idx",
		},
		"expired IntroDB entries": {
			query: "SELECT imdb_id, season_number, episode_number FROM introdb_segment_cache WHERE expires_at <= now() ORDER BY expires_at, imdb_id, season_number, episode_number LIMIT 128",
			index: "introdb_segment_cache_expiry_cleanup_idx",
		},
		"expired Fanart entries": {
			query: "SELECT resource_type, external_id, language FROM fanart_response_cache WHERE expires_at <= now() ORDER BY expires_at, resource_type, external_id, language LIMIT 256",
			index: "fanart_response_cache_expiry_cleanup_idx",
		},
		"Fanart capacity": {
			query: "SELECT resource_type, external_id, language FROM fanart_response_cache ORDER BY updated_at DESC, resource_type, external_id, language LIMIT 256 OFFSET 10000",
			index: "fanart_response_cache_capacity_cleanup_idx",
		},
	}
	for name, expectation := range plans {
		plan := explainPlan(t, ctx, connection, expectation.query)
		if !strings.Contains(plan, expectation.index) {
			t.Fatalf("%s plan does not use %s:\n%s", name, expectation.index, plan)
		}
	}
}

func explainPlan(t *testing.T, ctx context.Context, connection *pgxpool.Conn, query string) string {
	t.Helper()
	rows, err := connection.Query(ctx, "EXPLAIN "+query)
	if err != nil {
		t.Fatalf("explain hotpath query: %v", err)
	}
	plan, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect hotpath plan: %v", err)
	}
	return strings.Join(plan, "\n")
}

func TestAddonEnabledMigrationBackfillsExistingInstallations(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run addon availability migration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open addon migration database: %v", err)
	}
	defer pool.Close()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire addon migration connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `
		CREATE TEMPORARY TABLE profile_addons (id integer PRIMARY KEY);
		INSERT INTO profile_addons (id) VALUES (1);
	`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("seed pre-migration addon: %v", err)
	}
	contents, err := migrationFiles.ReadFile("migrations/000057_addon_enabled.sql")
	if err != nil {
		t.Fatalf("read addon enabled migration: %v", err)
	}
	if _, err := connection.Exec(ctx, string(contents)); err != nil {
		t.Fatalf("apply addon enabled migration: %v", err)
	}
	if _, err := connection.Exec(ctx, `INSERT INTO profile_addons (id) VALUES (2)`); err != nil {
		t.Fatalf("insert post-migration addon: %v", err)
	}
	var total, enabled int
	if err := connection.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE enabled) FROM profile_addons`).Scan(&total, &enabled); err != nil {
		t.Fatalf("query migrated addon availability: %v", err)
	}
	if total != 2 || enabled != 2 {
		t.Fatalf("migrated addon availability = %d/%d enabled, want 2/2", enabled, total)
	}
}
