package auth

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConcurrentRefreshDoesNotRevokeRotatedSession(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the PostgreSQL refresh concurrency test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 4
	config.ConnConfig.RuntimeParams["application_name"] = "rivune-auth-refresh-concurrency-test"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	var userID, categoryID, deviceID, sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-test-hash', 'member')
		RETURNING id::text
	`, "refresh-race-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert refresh test user: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupContext, "DELETE FROM users WHERE id = $1", userID); err != nil {
			t.Errorf("delete refresh test user: %v", err)
		}
	})
	if err := pool.QueryRow(ctx, "SELECT id::text FROM access_categories WHERE is_default").Scan(&categoryID); err != nil {
		t.Fatalf("select default category: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1, $2, 'web', $3, now())
		RETURNING id::text
	`, userID, "Refresh race "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert refresh test device: %v", err)
	}

	_, accessHash, err := newToken(accessTokenPrefix)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	refreshToken, refreshHash, err := newToken(refreshTokenPrefix)
	if err != nil {
		t.Fatalf("generate refresh token: %v", err)
	}
	refreshExpiresAt := time.Now().UTC().Add(time.Hour)
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id
		)
		VALUES ($1, $2, $3, now() + interval '5 minutes', $4, 'category', $5)
		RETURNING id::text
	`, userID, deviceID, accessHash, refreshExpiresAt, categoryID).Scan(&sessionID); err != nil {
		t.Fatalf("insert refresh test session: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO auth_refresh_tokens (token_hash, session_id, expires_at)
		VALUES ($1, $2, $3)
	`, refreshHash, sessionID, refreshExpiresAt); err != nil {
		t.Fatalf("insert refresh test token: %v", err)
	}

	service := &Service{pool: pool, accessTTL: 5 * time.Minute, refreshTTL: time.Hour, timezone: "UTC"}
	type refreshResult struct {
		tokens TokenPair
		err    error
	}
	start := make(chan struct{})
	results := make(chan refreshResult, 2)
	var callers sync.WaitGroup
	callers.Add(2)
	for range 2 {
		go func() {
			defer callers.Done()
			<-start
			tokens, err := service.Refresh(ctx, refreshToken)
			results <- refreshResult{tokens: tokens, err: err}
		}()
	}
	close(start)
	callers.Wait()
	close(results)

	var rotated TokenPair
	successes := 0
	rejections := 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			rotated = result.tokens
		case errors.Is(result.err, ErrInvalidToken):
			rejections++
		default:
			t.Fatalf("concurrent refresh returned unexpected error: %v", result.err)
		}
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("concurrent refresh outcomes = %d success, %d rejection; want one of each", successes, rejections)
	}

	var revokedAt *time.Time
	var revokedReason *string
	if err := pool.QueryRow(ctx, "SELECT revoked_at, revoked_reason FROM auth_sessions WHERE id = $1", sessionID).Scan(&revokedAt, &revokedReason); err != nil {
		t.Fatalf("query refreshed session: %v", err)
	}
	if revokedAt != nil || revokedReason != nil {
		t.Fatalf("concurrent stale refresh revoked session at %v with reason %v", revokedAt, revokedReason)
	}
	if _, err := service.Refresh(ctx, rotated.RefreshToken); err != nil {
		t.Fatalf("refresh with the rotated token after concurrent replay: %v", err)
	}
}
