package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/database"
)

func TestReloadLinkedPrincipalRevalidatesAvailabilityExpiryAndRevocation(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run linked principal authorization test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open linked principal database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	var userID, profileID, categoryID, deviceID, sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-linked-principal-hash', 'member')
		RETURNING id::text
	`, "linked_principal_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert linked principal user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1::uuid", userID)
		if profileID != "" {
			_, _ = pool.Exec(context.Background(), "DELETE FROM profiles WHERE id = $1::uuid", profileID)
		}
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name) VALUES ($1)
		RETURNING id::text, category_id::text
	`, "Linked principal "+suffix).Scan(&profileID, &categoryID); err != nil {
		t.Fatalf("insert linked principal profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id) VALUES ($1, $2)
	`, userID, profileID); err != nil {
		t.Fatalf("grant linked principal profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1, $2, 'Generic Client', $3, now()) RETURNING id::text
	`, userID, "Linked principal device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert linked principal device: %v", err)
	}
	accessHash := sha256.Sum256([]byte("linked-principal-access-" + suffix))
	contextHash := sha256.Sum256([]byte("linked-principal-context-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at,
			profile_context_hash
		) VALUES (
			$1, $2, $3, now() - interval '1 minute', now() + interval '2 hours',
			'category', $4, $5, now() + interval '2 hours', $6
		) RETURNING id::text
	`, userID, deviceID, accessHash[:], categoryID, profileID, contextHash[:]).Scan(&sessionID); err != nil {
		t.Fatalf("insert linked principal native session: %v", err)
	}

	service := &Service{pool: pool, timezone: "UTC"}
	principal, err := service.ReloadLinkedPrincipal(ctx, sessionID, profileID)
	if err != nil {
		t.Fatalf("reload linked principal beyond access-token TTL: %v", err)
	}
	if principal.SessionID != sessionID || principal.ActiveProfileID == nil || *principal.ActiveProfileID != profileID {
		t.Fatalf("reloaded linked principal = %+v", principal)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin linked principal expiry blocker: %v", err)
	}
	blockerFinished := false
	defer func() {
		if !blockerFinished {
			_ = blocker.Rollback(context.Background())
		}
	}()
	var blockerPID int
	var lockedExpiry, lastSeenBefore time.Time
	if err := blocker.QueryRow(ctx, `
		UPDATE auth_sessions
		SET refresh_expires_at = clock_timestamp() + interval '3 seconds',
		    profile_grant_expires_at = clock_timestamp() + interval '3 seconds'
		WHERE id = $1::uuid
		RETURNING pg_backend_pid(), refresh_expires_at, last_seen_at
	`, sessionID).Scan(&blockerPID, &lockedExpiry, &lastSeenBefore); err != nil {
		t.Fatalf("lock expiring linked authentication session: %v", err)
	}
	reloadDone := make(chan error, 1)
	go func() {
		_, reloadErr := service.ReloadLinkedPrincipal(ctx, sessionID, profileID)
		reloadDone <- reloadErr
	}()
	blockedDeadline := time.Now().Add(2 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE $1 = ANY(pg_blocking_pids(pid))
			)
		`, blockerPID).Scan(&blocked); err != nil {
			t.Fatalf("inspect linked principal expiry wait: %v", err)
		}
		if blocked {
			break
		}
		select {
		case earlyErr := <-reloadDone:
			t.Fatalf("linked principal reload completed before session lock release: %v", earlyErr)
		default:
		}
		if time.Now().After(blockedDeadline) {
			t.Fatal("linked principal reload did not wait for the authentication session lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := pool.Exec(ctx, `
		SELECT pg_sleep((GREATEST(EXTRACT(EPOCH FROM ($1::timestamptz - clock_timestamp())), 0) + 0.05)::double precision)
	`, lockedExpiry); err != nil {
		t.Fatalf("wait for locked linked session expiry: %v", err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release expired linked authentication session: %v", err)
	}
	blockerFinished = true
	if reloadErr := <-reloadDone; !errors.Is(reloadErr, ErrInvalidToken) {
		t.Fatalf("post-lock linked session expiry error = %v, want %v", reloadErr, ErrInvalidToken)
	}
	var lastSeenAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT last_seen_at FROM auth_sessions WHERE id = $1::uuid`, sessionID).Scan(&lastSeenAfter); err != nil {
		t.Fatalf("read expired linked session last-seen time: %v", err)
	}
	if !lastSeenAfter.Equal(lastSeenBefore) {
		t.Fatalf("expired linked session was touched after lock wait: before=%v after=%v", lastSeenBefore, lastSeenAfter)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET refresh_expires_at = clock_timestamp() + interval '2 hours',
		    profile_grant_expires_at = clock_timestamp() + interval '2 hours'
		WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("restore linked session expiry after lock wait: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE profiles
		SET available_until = current_date - 1,
		    access_start_time = NULL,
		    access_end_time = NULL,
		    access_timezone = 'Pacific/Honolulu'
		WHERE id = $1::uuid
	`, profileID); err != nil {
		t.Fatalf("configure expired linked profile schedule: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin linked principal schedule reload: %v", err)
	}
	_, valid, err := ReloadAndLockLinkedPrincipal(ctx, tx, principal, time.Now().UTC().Add(-48*time.Hour), time.UTC)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("reload linked principal against fresh profile schedule: %v", err)
	}
	if valid {
		t.Fatal("linked principal used the caller's stale time instead of the post-lock wall clock")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE profiles
		SET available_until = NULL, access_start_time = NULL, access_end_time = NULL, access_timezone = 'Pacific/Honolulu'
		WHERE id = $1::uuid
	`, profileID); err != nil {
		t.Fatalf("clear linked profile schedule: %v", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE profiles SET enabled = false WHERE id = $1::uuid", profileID); err != nil {
		t.Fatalf("disable linked profile: %v", err)
	}
	if _, err := service.ReloadLinkedPrincipal(ctx, sessionID, profileID); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("disabled linked profile error = %v, want %v", err, ErrInvalidToken)
	}
	if _, err := pool.Exec(ctx, "UPDATE profiles SET enabled = true WHERE id = $1::uuid", profileID); err != nil {
		t.Fatalf("re-enable linked profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions SET refresh_expires_at = now() - interval '1 second' WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("expire linked native session: %v", err)
	}
	if _, err := service.ReloadLinkedPrincipal(ctx, sessionID, profileID); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired linked native session error = %v, want %v", err, ErrInvalidToken)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET refresh_expires_at = now() + interval '2 hours', revoked_at = now(), revoked_reason = 'test'
		WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("revoke linked native session: %v", err)
	}
	if _, err := service.ReloadLinkedPrincipal(ctx, sessionID, profileID); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked linked native session error = %v, want %v", err, ErrInvalidToken)
	}
}
