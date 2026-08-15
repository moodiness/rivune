package addon

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

type addonSelectionListResult struct {
	addons []InstalledAddon
	err    error
}

func TestOrdinaryListRejectsCapturedProfileAfterSelectionOrClearWins(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the PostgreSQL addon selection race test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open addon selection race database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate addon selection race database: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	position := int(time.Now().UnixNano()%1_000_000_000) + 1_000_000_000
	var categoryID, userID, profileAID, profileBID, deviceID, sessionID, addonID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Addon selection race "+suffix, "addon-selection-race-"+suffix, position).Scan(&categoryID); err != nil {
		t.Fatalf("insert addon selection race category: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		if sessionID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM auth_sessions WHERE id = $1::uuid`, sessionID)
		}
		if addonID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM profile_addons WHERE id = $1::uuid`, addonID)
		}
		if deviceID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		}
		if profileAID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM profiles WHERE id = $1::uuid`, profileAID)
		}
		if profileBID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM profiles WHERE id = $1::uuid`, profileBID)
		}
		if userID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id = $1::uuid`, userID)
		}
		_, _ = pool.Exec(cleanup, `DELETE FROM access_categories WHERE id = $1::uuid`, categoryID)
	})

	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-addon-selection-race-hash', 'member')
		RETURNING id::text
	`, "addon_selection_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert addon selection race user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id)
		VALUES ($1, $2::uuid)
		RETURNING id::text
	`, "Addon selection A "+suffix, categoryID).Scan(&profileAID); err != nil {
		t.Fatalf("insert addon selection profile A: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id)
		VALUES ($1, $2::uuid)
		RETURNING id::text
	`, "Addon selection B "+suffix, categoryID).Scan(&profileBID); err != nil {
		t.Fatalf("insert addon selection profile B: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, false), ($1::uuid, $3::uuid, false)
	`, userID, profileAID, profileBID); err != nil {
		t.Fatalf("grant addon selection profiles: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'addon-selection-test', $3::uuid, now())
		RETURNING id::text
	`, userID, "Addon selection device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert addon selection race device: %v", err)
	}

	accessHash := sha256.Sum256([]byte("addon-selection-access-" + suffix))
	profileAContextHash := sha256.Sum256([]byte("addon-selection-context-a-" + suffix))
	profileBContextHash := sha256.Sum256([]byte("addon-selection-context-b-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash
		) VALUES (
			$1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '2 hours',
			'category', $4::uuid, $5::uuid, now() + interval '2 hours', $6
		)
		RETURNING id::text
	`, userID, deviceID, accessHash[:], categoryID, profileAID, profileAContextHash[:]).Scan(&sessionID); err != nil {
		t.Fatalf("insert addon selection race session: %v", err)
	}
	const manifest = `{"id":"org.rivune.selection-race","version":"1.0.0","name":"Selection Race","types":["movie"],"catalogs":[],"resources":["stream"]}`
	if err := pool.QueryRow(ctx, `
		INSERT INTO profile_addons (
			profile_id, transport_url, manifest, manifest_id, manifest_version, position, enabled
		) VALUES (
			$1::uuid, $2, $3::jsonb, 'org.rivune.selection-race', '1.0.0', 0, true
		)
		RETURNING id::text
	`, profileAID, "https://selection-race-"+suffix+".example/manifest.json", manifest).Scan(&addonID); err != nil {
		t.Fatalf("insert profile A addon: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO addon_profile_access (addon_id, profile_id, position)
		VALUES ($1::uuid, $2::uuid, 0)
	`, addonID, profileAID); err != nil {
		t.Fatalf("assign addon only to profile A: %v", err)
	}

	grantExpiry := time.Now().UTC().Add(2 * time.Hour)
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &profileAID, ProfileGrantExpiresAt: &grantExpiry,
		ProfileContextHash: profileAContextHash[:],
	}
	service := NewService(pool, nil, nil)
	visible, err := service.loadAddonList(ctx, principal, false)
	if err != nil {
		t.Fatalf("list profile A addon before selection races: %v", err)
	}
	if len(visible) != 1 || visible[0].ID != addonID {
		t.Fatalf("profile A addon visibility = %+v, want only %s", visible, addonID)
	}

	cases := []struct {
		name   string
		mutate func(context.Context, pgx.Tx) error
	}{
		{
			name: "profile B selection wins",
			mutate: func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `
					UPDATE auth_sessions
					SET active_profile_id = $2::uuid,
					    profile_grant_expires_at = now() + interval '2 hours',
					    profile_context_hash = $3
					WHERE id = $1::uuid
				`, sessionID, profileBID, profileBContextHash[:])
				return err
			},
		},
		{
			name: "clear wins",
			mutate: func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `
					UPDATE auth_sessions
					SET active_profile_id = NULL,
					    profile_grant_expires_at = NULL,
					    profile_context_hash = NULL
					WHERE id = $1::uuid
				`, sessionID)
				return err
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `
				UPDATE auth_sessions
				SET active_profile_id = $2::uuid,
				    profile_grant_expires_at = now() + interval '2 hours',
				    profile_context_hash = $3
				WHERE id = $1::uuid
			`, sessionID, profileAID, profileAContextHash[:]); err != nil {
				t.Fatalf("reset authoritative profile A selection: %v", err)
			}

			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin profile A blocker: %v", err)
			}
			blockerFinished := false
			defer func() {
				if !blockerFinished {
					_ = blocker.Rollback(context.Background())
				}
			}()
			var blockerPID int
			if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
				t.Fatalf("query profile A blocker PID: %v", err)
			}
			if _, err := blocker.Exec(ctx, `
				SELECT id FROM profiles WHERE id = $1::uuid FOR UPDATE
			`, profileAID); err != nil {
				t.Fatalf("lock profile A: %v", err)
			}
			result := make(chan addonSelectionListResult, 1)
			go func() {
				addons, listErr := service.loadAddonList(ctx, principal, false)
				result <- addonSelectionListResult{addons: addons, err: listErr}
			}()
			waitForAddonProfileAuthorizationBlock(t, ctx, pool, blockerPID, result)

			selection, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin winning profile selection mutation: %v", err)
			}
			if err := testCase.mutate(ctx, selection); err != nil {
				_ = selection.Rollback(context.Background())
				t.Fatalf("mutate authoritative profile selection: %v", err)
			}
			if err := selection.Commit(ctx); err != nil {
				t.Fatalf("commit winning profile selection mutation: %v", err)
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatalf("release profile A blocker: %v", err)
			}
			blockerFinished = true

			listed := <-result
			if !errors.Is(listed.err, ErrActiveProfileRequired) {
				t.Fatalf("stale profile A list error = %v, want %v", listed.err, ErrActiveProfileRequired)
			}
			if len(listed.addons) != 0 {
				t.Fatalf("stale profile A list returned protected addons: %+v", listed.addons)
			}
		})
	}
}

func waitForAddonProfileAuthorizationBlock(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	blockerPID int,
	result <-chan addonSelectionListResult,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity activity
				WHERE activity.datname = current_database()
				  AND activity.wait_event_type = 'Lock'
				  AND $1::integer = ANY(pg_blocking_pids(activity.pid))
				  AND activity.query LIKE '%WITH locked_profiles AS MATERIALIZED%'
			)
		`, blockerPID).Scan(&blocked); err != nil {
			t.Fatalf("inspect blocked addon authorization: %v", err)
		}
		if blocked {
			return
		}
		select {
		case listed := <-result:
			t.Fatalf("addon list returned before profile A blocker release: addons=%+v error=%v", listed.addons, listed.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("addon list did not block acquiring profile A authorization lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
