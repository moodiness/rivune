package watchstate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/secretcrypto"
	"github.com/moodiness/rivune/server/internal/tracking"
)

const (
	trackingVariantProfileID   = "11111111-1111-4111-8111-111111111111"
	trackingCanonicalEpisodeID = "10000000-0000-4000-8000-000000000011"
	trackingVariantEpisodeID   = "10000000-0000-4000-8000-000000000021"
)

func TestTrackingEnabledVariantWatchMutationsRemainLocal(t *testing.T) {
	t.Run("variant progress and watched", func(t *testing.T) {
		pool, service, principal := newTrackingVariantWatchstateFixture(t)

		progress, err := service.UpdateProgress(t.Context(), principal, trackingVariantEpisodeID, UpdateProgressInput{
			PositionSeconds: 120,
			DurationSeconds: 1200,
		})
		if err != nil {
			t.Fatalf("store variant progress with tracking enabled: %v", err)
		}
		watched, err := service.SetWatched(t.Context(), principal, trackingVariantEpisodeID, true, CompletionInput{
			ExpectedVersion: progress.Version,
		})
		if err != nil {
			t.Fatalf("store variant watched state with tracking enabled: %v", err)
		}
		if !watched.Completed || watched.Version != progress.Version+1 || watched.PositionSeconds != watched.DurationSeconds {
			t.Fatalf("variant watched state = %+v after progress %+v", watched, progress)
		}
		assertTrackingRows(t, pool, trackingVariantEpisodeID, 0, 0)
	})

	t.Run("mixed canonical and variant watched batch", func(t *testing.T) {
		pool, service, principal := newTrackingVariantWatchstateFixture(t)

		result, err := service.SetWatchedBatch(t.Context(), principal, []SetWatchedBatchItem{
			{TitleID: trackingCanonicalEpisodeID, Completed: true},
			{TitleID: trackingVariantEpisodeID, Completed: true},
		})
		if err != nil {
			t.Fatalf("store mixed canonical and variant watched batch: %v", err)
		}
		if len(result.Items) != 2 || result.Items[0].Progress == nil || result.Items[1].Progress == nil ||
			!result.Items[0].Progress.Completed || !result.Items[1].Progress.Completed {
			t.Fatalf("mixed watched batch result = %+v", result)
		}
		var canonicalLocal, variantLocal int
		if err := pool.QueryRow(t.Context(), `
			SELECT
				(SELECT count(*) FROM profile_progress WHERE profile_id = $1::uuid AND title_id = $2::uuid AND completed),
				(SELECT count(*) FROM profile_progress WHERE profile_id = $1::uuid AND title_id = $3::uuid AND completed)
		`, trackingVariantProfileID, trackingCanonicalEpisodeID, trackingVariantEpisodeID).Scan(&canonicalLocal, &variantLocal); err != nil {
			t.Fatalf("query mixed local watched state: %v", err)
		}
		if canonicalLocal != 1 || variantLocal != 1 {
			t.Fatalf("mixed local watched rows canonical=%d variant=%d", canonicalLocal, variantLocal)
		}
		assertTrackingRows(t, pool, trackingCanonicalEpisodeID, 1, 1)
		assertTrackingRows(t, pool, trackingVariantEpisodeID, 0, 0)
		var eventType string
		var completed bool
		if err := pool.QueryRow(t.Context(), `
			SELECT event_type, (payload->>'completed')::boolean
			FROM profile_tracking_outbox
			WHERE title_id = $1::uuid
		`, trackingCanonicalEpisodeID).Scan(&eventType, &completed); err != nil {
			t.Fatalf("query canonical mixed tracking event: %v", err)
		}
		if eventType != "watched" || !completed {
			t.Fatalf("canonical mixed tracking event type=%q completed=%t", eventType, completed)
		}
	})
}

func newTrackingVariantWatchstateFixture(t *testing.T) (*pgxpool.Pool, *Service, auth.Principal) {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run tracking variant watchstate tests")
	}
	schema := fmt.Sprintf("watchstate_tracking_variant_%d", time.Now().UnixNano())
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse tracking variant database URL: %v", err)
	}
	config.MaxConns = 1
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open tracking variant database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`
		CREATE SCHEMA %[1]s;
		SET search_path TO %[1]s, public;
		CREATE TABLE profiles (id uuid PRIMARY KEY, category_id uuid);
		CREATE TABLE user_profile_access (
			user_id uuid NOT NULL, profile_id uuid NOT NULL, can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		CREATE TABLE titles (
			id uuid PRIMARY KEY, media_type text NOT NULL, parent_id uuid, hierarchy_variant text NOT NULL DEFAULT '',
			is_current boolean NOT NULL DEFAULT true, source_addon_id uuid
		);
		CREATE TABLE profile_title_external_ids (profile_id uuid NOT NULL, title_id uuid NOT NULL);
		CREATE TABLE profile_addons (id uuid PRIMARY KEY, enabled boolean NOT NULL DEFAULT true);
		CREATE TABLE addon_profile_access (addon_id uuid NOT NULL, profile_id uuid NOT NULL);
		CREATE TABLE addon_category_access (addon_id uuid NOT NULL, category_id uuid NOT NULL);
		CREATE TABLE profile_progress (
			profile_id uuid NOT NULL, title_id uuid NOT NULL, position_seconds integer NOT NULL,
			duration_seconds integer NOT NULL, completed boolean NOT NULL DEFAULT false,
			version bigint NOT NULL DEFAULT 1, last_watched_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (profile_id, title_id)
		);
		CREATE TABLE profile_continue_dismissals (
			profile_id uuid NOT NULL, title_id uuid NOT NULL, dismissed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (profile_id, title_id)
		);
		CREATE TABLE profile_tracking_accounts (
			profile_id uuid NOT NULL, provider text NOT NULL,
			sync_watched boolean NOT NULL DEFAULT true, sync_progress boolean NOT NULL DEFAULT true,
			sync_library boolean NOT NULL DEFAULT true, PRIMARY KEY (profile_id, provider)
		);
		CREATE TABLE profile_tracking_event_heads (
			profile_id uuid NOT NULL, provider text NOT NULL, title_id uuid NOT NULL, event_type text NOT NULL,
			idempotency_key text NOT NULL, affects_watched boolean NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
			PRIMARY KEY (profile_id, provider, title_id, event_type)
		);
		CREATE TABLE profile_tracking_outbox (
			enqueue_sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), profile_id uuid NOT NULL, provider text NOT NULL,
			title_id uuid NOT NULL, event_type text NOT NULL, payload jsonb NOT NULL, idempotency_key text NOT NULL,
			attempt_count integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(),
			leased_until timestamptz, last_error text, created_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE UNIQUE INDEX profile_tracking_outbox_pending_event_idx
			ON profile_tracking_outbox (profile_id, provider, title_id, event_type) WHERE leased_until IS NULL;
		CREATE INDEX profile_tracking_outbox_predecessor_idx
			ON profile_tracking_outbox (profile_id, provider, title_id, enqueue_sequence);
		INSERT INTO profiles (id) VALUES ('%[2]s');
		INSERT INTO titles (id, media_type, hierarchy_variant) VALUES
			('%[3]s', 'episode', ''),
			('%[4]s', 'episode', 'tvdb:2');
		INSERT INTO profile_tracking_accounts (profile_id, provider) VALUES ('%[2]s', 'trakt');
	`, schema, trackingVariantProfileID, trackingCanonicalEpisodeID, trackingVariantEpisodeID)); err != nil {
		t.Fatalf("create tracking variant watchstate fixture: %v", err)
	}
	principal := captureActiveProfileTestSession(t, t.Context(), pool, auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}, trackingVariantProfileID)
	keyring, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{0x42}, 32)}})
	if err != nil {
		t.Fatalf("create tracking variant encryption keyring: %v", err)
	}
	trackingService, err := tracking.NewService(pool, keyring, "", "", "", nil, nil)
	if err != nil {
		t.Fatalf("create tracking variant service: %v", err)
	}
	return pool, NewService(pool, time.UTC, trackingService), principal
}

func assertTrackingRows(t *testing.T, pool *pgxpool.Pool, titleID string, wantHeads, wantOutbox int) {
	t.Helper()
	var heads, outbox int
	if err := pool.QueryRow(t.Context(), `
		SELECT
			(SELECT count(*) FROM profile_tracking_event_heads WHERE title_id = $1::uuid),
			(SELECT count(*) FROM profile_tracking_outbox WHERE title_id = $1::uuid)
	`, titleID).Scan(&heads, &outbox); err != nil {
		t.Fatalf("query tracking rows for %s: %v", titleID, err)
	}
	if heads != wantHeads || outbox != wantOutbox {
		t.Fatalf("tracking rows for %s heads/outbox=%d/%d, want %d/%d", titleID, heads, outbox, wantHeads, wantOutbox)
	}
}
