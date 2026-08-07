package profile

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
	"github.com/moodiness/rivune/server/internal/password"
)

func TestSelectForLinkedSessionRequiresPINAndUsesRefreshBound(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run linked profile selection test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open linked profile selection database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	pin := "2468"
	pinHash, err := password.Hash(pin)
	if err != nil {
		t.Fatalf("hash linked profile PIN: %v", err)
	}
	var userID, profileID, categoryID, deviceID, sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-linked-selection-hash', 'member')
		RETURNING id::text
	`, "linked_selection_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert linked selection user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1::uuid", userID)
		if profileID != "" {
			_, _ = pool.Exec(context.Background(), "DELETE FROM profiles WHERE id = $1::uuid", profileID)
		}
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, pin_hash) VALUES ($1, $2)
		RETURNING id::text, category_id::text
	`, "Linked selection "+suffix, pinHash).Scan(&profileID, &categoryID); err != nil {
		t.Fatalf("insert linked selection profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id) VALUES ($1, $2)
	`, userID, profileID); err != nil {
		t.Fatalf("grant linked selection profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1, $2, 'test', $3, now()) RETURNING id::text
	`, userID, "Linked selection device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert linked selection device: %v", err)
	}
	refreshExpiresAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	accessHash := sha256.Sum256([]byte("linked-selection-access-" + suffix))
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id
		) VALUES ($1, $2, $3, now() + interval '1 hour', $4, 'category', $5)
		RETURNING id::text
	`, userID, deviceID, accessHash[:], refreshExpiresAt, categoryID).Scan(&sessionID); err != nil {
		t.Fatalf("insert linked selection native session: %v", err)
	}
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID,
		Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
	}
	service := NewService(pool, 5*time.Minute, "UTC")
	if _, err := service.SelectForLinkedSession(ctx, principal, profileID, nil, false); !errors.Is(err, ErrInvalidPIN) {
		t.Fatalf("missing linked profile PIN error = %v, want %v", err, ErrInvalidPIN)
	}
	var activeAfterMissingPIN *string
	if err := pool.QueryRow(ctx, `
		SELECT active_profile_id::text FROM auth_sessions WHERE id = $1::uuid
	`, sessionID).Scan(&activeAfterMissingPIN); err != nil {
		t.Fatalf("read session after missing PIN: %v", err)
	}
	if activeAfterMissingPIN != nil {
		t.Fatalf("missing PIN selected profile %q", *activeAfterMissingPIN)
	}

	selection, err := service.SelectForLinkedSession(ctx, principal, profileID, &pin, false)
	if err != nil {
		t.Fatalf("select linked profile with PIN: %v", err)
	}
	if !selection.ExpiresAt.Equal(refreshExpiresAt) {
		t.Fatalf("linked grant expiry = %v, want native refresh bound %v", selection.ExpiresAt, refreshExpiresAt)
	}
	var storedProfileID string
	var storedExpiry time.Time
	if err := pool.QueryRow(ctx, `
		SELECT active_profile_id::text, profile_grant_expires_at
		FROM auth_sessions WHERE id = $1::uuid
	`, sessionID).Scan(&storedProfileID, &storedExpiry); err != nil {
		t.Fatalf("read linked profile grant: %v", err)
	}
	if storedProfileID != profileID || !storedExpiry.Equal(refreshExpiresAt) {
		t.Fatalf("stored linked grant = %s until %v, want %s until %v", storedProfileID, storedExpiry, profileID, refreshExpiresAt)
	}
}
