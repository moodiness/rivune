package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/database"
)

func TestLockActiveProfileSelectionRejectsAuthoritativeExpiry(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run active profile authorization expiry tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open active profile authorization database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID, profileID, categoryID, deviceID, sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-active-profile-authorization-hash', 'member')
		RETURNING id::text
	`, "active_profile_authorization_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert active profile authorization user: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id = $1::uuid`, userID)
		if profileID != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM profiles WHERE id = $1::uuid`, profileID)
		}
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name)
		VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Active profile authorization "+suffix).Scan(&profileID, &categoryID); err != nil {
		t.Fatalf("insert active profile authorization profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id)
		VALUES ($1::uuid, $2::uuid)
	`, userID, profileID); err != nil {
		t.Fatalf("grant active profile authorization profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'authorization-expiry-test', $3::uuid, now())
		RETURNING id::text
	`, userID, "Active profile authorization device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert active profile authorization device: %v", err)
	}
	accessHash := sha256.Sum256([]byte("active-profile-access-" + suffix))
	contextHash := sha256.Sum256([]byte("active-profile-context-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash
		) VALUES (
			$1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '2 hours',
			'category', $4::uuid, $5::uuid, now() + interval '1 hour', $6
		)
		RETURNING id::text
	`, userID, deviceID, accessHash[:], categoryID, profileID, contextHash[:]).Scan(&sessionID); err != nil {
		t.Fatalf("insert active profile authorization session: %v", err)
	}
	grantExpiresAt := time.Now().UTC().Add(time.Hour)
	captured := Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID,
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
		ProfileContextHash: contextHash[:],
	}
	selectionValid := func() bool {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin active profile authorization check: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		valid, err := LockActiveProfileSelection(ctx, tx, captured)
		if err != nil {
			t.Fatalf("lock active profile selection: %v", err)
		}
		return valid
	}
	if !selectionValid() {
		t.Fatal("unexpired authoritative active profile selection was rejected")
	}
	waitForSessionLock := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var waiting int
			if err := pool.QueryRow(ctx, `
				SELECT count(*)
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND pid <> pg_backend_pid()
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%FROM auth_sessions%'
			`).Scan(&waiting); err != nil {
				t.Fatalf("observe deferred authorization lock: %v", err)
			}
			if waiting > 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("deferred authorization did not wait for the session lock")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, expiryColumn := range []string{"access_expires_at", "profile_grant_expires_at"} {
		t.Run("rejects "+expiryColumn+" expiring while session lock waits", func(t *testing.T) {
			if _, err := pool.Exec(ctx, `
				UPDATE auth_sessions
				SET access_expires_at = clock_timestamp() + interval '1 hour',
				    profile_grant_expires_at = clock_timestamp() + interval '1 hour'
				WHERE id = $1::uuid
			`, sessionID); err != nil {
				t.Fatalf("reset deferred authorization expiry: %v", err)
			}
			gateTx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin deferred authorization gate: %v", err)
			}
			gateFinished := false
			defer func() {
				if !gateFinished {
					_ = gateTx.Rollback(ctx)
				}
			}()
			if _, err := gateTx.Exec(ctx, `
				SELECT id FROM auth_sessions WHERE id = $1::uuid FOR UPDATE
			`, sessionID); err != nil {
				t.Fatalf("lock deferred authorization session: %v", err)
			}

			result := make(chan struct {
				authorized bool
				err        error
			}, 1)
			preWait := time.Now().UTC()
			go func() {
				tx, beginErr := pool.Begin(ctx)
				if beginErr != nil {
					result <- struct {
						authorized bool
						err        error
					}{err: beginErr}
					return
				}
				defer func() { _ = tx.Rollback(ctx) }()
				_, authorized, reloadErr := ReloadAndLockPrincipal(ctx, tx, captured, preWait, time.UTC)
				result <- struct {
					authorized bool
					err        error
				}{authorized: authorized, err: reloadErr}
			}()
			waitForSessionLock()
			if _, err := gateTx.Exec(ctx, fmt.Sprintf(`
				UPDATE auth_sessions
				SET %s = clock_timestamp() + interval '250 milliseconds'
				WHERE id = $1::uuid
			`, expiryColumn), sessionID); err != nil {
				t.Fatalf("stage blocked %s expiry: %v", expiryColumn, err)
			}
			if _, err := gateTx.Exec(ctx, `SELECT pg_sleep(0.35)`); err != nil {
				t.Fatalf("wait past deferred %s expiry: %v", expiryColumn, err)
			}
			if err := gateTx.Commit(ctx); err != nil {
				t.Fatalf("release deferred authorization gate: %v", err)
			}
			gateFinished = true
			got := <-result
			if got.err != nil {
				t.Fatalf("reload after deferred %s expiry: %v", expiryColumn, got.err)
			}
			if got.authorized {
				t.Fatalf("authorization committed after %s expired while waiting", expiryColumn)
			}
		})
	}
	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET access_expires_at = clock_timestamp() + interval '1 hour',
		    profile_grant_expires_at = clock_timestamp() + interval '1 hour'
		WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("restore authoritative expiry after lock tests: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET access_expires_at = now() - interval '1 second'
		WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("expire authoritative access grant: %v", err)
	}
	if selectionValid() {
		t.Fatal("expired authoritative access grant passed active profile selection authorization")
	}

	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET access_expires_at = now() + interval '1 hour',
		    profile_grant_expires_at = now() - interval '1 second'
		WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("expire authoritative profile grant: %v", err)
	}
	if selectionValid() {
		t.Fatal("expired authoritative profile grant passed active profile selection authorization")
	}
}
