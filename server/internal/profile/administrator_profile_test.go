package profile

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestGlobalAdministratorProfileCreationAndSelectionFollowDeviceCategory(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run administrator profile tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	position := int(time.Now().UnixNano()%100_000_000) + 1_500_000_000
	var categoryAID, categoryBID, userID, administratorProfileID, deviceID, sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Administrator profile A "+suffix, "administrator-profile-a-"+suffix, position).Scan(&categoryAID); err != nil {
		t.Fatalf("create first category: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Administrator profile B "+suffix, "administrator-profile-b-"+suffix, position+1).Scan(&categoryBID); err != nil {
		t.Fatalf("create second category: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-test-hash', 'admin')
		RETURNING id::text
	`, "administrator_profile_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("create administrator account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id, access_timezone)
		VALUES ($1, $2::uuid, 'UTC')
		RETURNING id::text
	`, "Owner "+suffix, categoryAID).Scan(&administratorProfileID); err != nil {
		t.Fatalf("create administrator profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH access AS (
			INSERT INTO user_profile_access (user_id, profile_id, can_manage)
			VALUES ($1::uuid, $2::uuid, true)
			RETURNING profile_id
		)
		INSERT INTO profile_settings (profile_id) SELECT profile_id FROM access
	`, userID, administratorProfileID); err != nil {
		t.Fatalf("seed administrator profile access: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'test', $3::uuid, now())
		RETURNING id::text
	`, userID, "Administrator device "+suffix, categoryAID).Scan(&deviceID); err != nil {
		t.Fatalf("create administrator device: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope
		)
		VALUES (
			$1::uuid, $2::uuid, decode(repeat('ac', 32), 'hex'),
			now() + interval '1 hour', now() + interval '2 hours', 'global_admin'
		)
		RETURNING id::text
	`, userID, deviceID).Scan(&sessionID); err != nil {
		t.Fatalf("create administrator session: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext := context.Background()
		_, _ = pool.Exec(cleanupContext, `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM profiles WHERE category_id = ANY($1::uuid[])`, []string{categoryAID, categoryBID})
		_, _ = pool.Exec(cleanupContext, `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, []string{categoryAID, categoryBID})
	})

	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "admin",
		AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}
	service := NewService(pool, time.Hour, "UTC")
	standardProfile, err := service.Create(ctx, principal, CreateInput{Name: "Friend " + suffix, CategoryID: categoryBID})
	if err != nil {
		t.Fatalf("create standard profile: %v", err)
	}
	if standardProfile.CanManage {
		t.Fatal("global administrator creation promoted the new profile")
	}
	var storedCanManage bool
	if err := pool.QueryRow(ctx, `
		SELECT can_manage FROM user_profile_access
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, standardProfile.ID).Scan(&storedCanManage); err != nil {
		t.Fatalf("read created profile grant: %v", err)
	}
	if storedCanManage {
		t.Fatal("global administrator creation stored a management grant")
	}

	listed, err := service.List(ctx, principal)
	if err != nil {
		t.Fatalf("list administrator profiles: %v", err)
	}
	managementByID := make(map[string]bool, len(listed))
	for _, item := range listed {
		managementByID[item.ID] = item.CanManage
	}
	if !managementByID[administratorProfileID] || managementByID[standardProfile.ID] {
		t.Fatalf("profile management flags = %#v", managementByID)
	}
	if _, err := service.Select(ctx, principal, standardProfile.ID, nil, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-device-category selection error = %v, want %v", err, ErrNotFound)
	}
	selected, err := service.Select(ctx, principal, administratorProfileID, nil, false)
	if err != nil {
		t.Fatalf("select administrator profile: %v", err)
	}
	if !selected.Profile.CanManage {
		t.Fatal("administrator profile lost its explicit management grant")
	}
}
