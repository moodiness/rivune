package auth

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWebAndNativeRefreshChannelsCannotCross(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the PostgreSQL web session test")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	var userID, categoryID, deviceID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role) VALUES ($1, 'unused-test-hash', 'member') RETURNING id::text`, "web-channel-"+suffix).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID) })
	if err := pool.QueryRow(ctx, "SELECT id::text FROM access_categories WHERE is_default").Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO devices (user_id, name, platform, category_id, approved_at) VALUES ($1, $2, 'web', $3, now()) RETURNING id::text`, userID, "Web channel "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}

	service := &Service{pool: pool, accessTTL: 5 * time.Minute, refreshTTL: time.Hour, timezone: "UTC"}
	webAccess, webAccessHash, err := newToken(accessTokenPrefix)
	if err != nil {
		t.Fatal(err)
	}
	webRefresh, webRefreshHash, err := newToken(refreshTokenPrefix)
	if err != nil {
		t.Fatal(err)
	}
	webExpiry := time.Now().UTC().Add(time.Hour)
	var webSessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at, authorization_scope, category_id, client_kind)
		VALUES ($1, $2, $3, now() + interval '5 minutes', $4, 'category', $5, 'web') RETURNING id::text
	`, userID, deviceID, webAccessHash, webExpiry, categoryID).Scan(&webSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO auth_refresh_tokens (token_hash, session_id, expires_at) VALUES ($1, $2, $3)`, webRefreshHash, webSessionID, webExpiry); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO auth_web_access_tokens (token_hash, session_id, expires_at) VALUES ($1, $2, now() + interval '5 minutes')`, webAccessHash, webSessionID); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Refresh(ctx, webRefresh); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("native refresh accepted web credential: %v", err)
	}
	rotatedWeb, err := service.RefreshWeb(ctx, webRefresh)
	if err != nil {
		t.Fatalf("refresh web credential: %v", err)
	}
	if _, err := service.Authenticate(ctx, webAccess); err != nil {
		t.Fatalf("first tab access stopped working after peer rotation: %v", err)
	}
	if _, err := service.Authenticate(ctx, rotatedWeb.AccessToken); err != nil {
		t.Fatalf("rotating tab access is invalid: %v", err)
	}

	nativeAccess, nativeAccessHash, err := newToken(accessTokenPrefix)
	if err != nil {
		t.Fatal(err)
	}
	_ = nativeAccess
	nativeRefresh, nativeRefreshHash, err := newToken(refreshTokenPrefix)
	if err != nil {
		t.Fatal(err)
	}
	var nativeSessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at, authorization_scope, category_id, client_kind)
		VALUES ($1, $2, $3, now() + interval '5 minutes', now() + interval '1 hour', 'category', $4, 'native') RETURNING id::text
	`, userID, deviceID, nativeAccessHash, categoryID).Scan(&nativeSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO auth_refresh_tokens (token_hash, session_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')`, nativeRefreshHash, nativeSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RefreshWeb(ctx, nativeRefresh); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("web refresh accepted native credential: %v", err)
	}
	if _, err := service.Refresh(ctx, nativeRefresh); err != nil {
		t.Fatalf("native refresh regressed: %v", err)
	}
}
