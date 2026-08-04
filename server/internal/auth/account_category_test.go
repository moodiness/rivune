package auth

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGlobalAdministratorAccountProfilesStayInDeviceCategory(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run account category tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	position := int(time.Now().UnixNano()%100_000_000) + 1_600_000_000
	var categoryAID, categoryBID, userID, managerProfileID, viewerProfileID, otherProfileID, deviceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Account device A "+suffix, "account-device-a-"+suffix, position).Scan(&categoryAID); err != nil {
		t.Fatalf("create first category: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Account device B "+suffix, "account-device-b-"+suffix, position+1).Scan(&categoryBID); err != nil {
		t.Fatalf("create second category: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-test-hash', 'admin')
		RETURNING id::text
	`, "account_device_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("create administrator account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO profiles (name, category_id)
			VALUES ($1, $4::uuid), ($2, $4::uuid), ($3, $5::uuid)
			RETURNING id, name
		)
		SELECT
			max(id::text) FILTER (WHERE name = $1),
			max(id::text) FILTER (WHERE name = $2),
			max(id::text) FILTER (WHERE name = $3)
		FROM inserted
	`, "Manager "+suffix, "Viewer "+suffix, "Other "+suffix, categoryAID, categoryBID).Scan(&managerProfileID, &viewerProfileID, &otherProfileID); err != nil {
		t.Fatalf("create account profiles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, true), ($1::uuid, $3::uuid, false), ($1::uuid, $4::uuid, true)
	`, userID, managerProfileID, viewerProfileID, otherProfileID); err != nil {
		t.Fatalf("create account profile grants: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'test', $3::uuid, now())
		RETURNING id::text
	`, userID, "Account device "+suffix, categoryAID).Scan(&deviceID); err != nil {
		t.Fatalf("create account device: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext := context.Background()
		_, _ = pool.Exec(cleanupContext, `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{managerProfileID, viewerProfileID, otherProfileID})
		_, _ = pool.Exec(cleanupContext, `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, []string{categoryAID, categoryBID})
	})

	service := &Service{pool: pool, timezone: "UTC"}
	account, err := service.Account(ctx, Principal{
		UserID: userID, DeviceID: deviceID, Role: "admin",
		AuthorizationScope: AuthorizationScopeGlobalAdministrator,
	})
	if err != nil {
		t.Fatalf("read global administrator account: %v", err)
	}
	if len(account.Profiles) != 2 {
		t.Fatalf("device-category profiles = %+v", account.Profiles)
	}
	managementByID := make(map[string]bool, len(account.Profiles))
	for _, profile := range account.Profiles {
		managementByID[profile.ID] = profile.CanManage
	}
	if !managementByID[managerProfileID] || managementByID[viewerProfileID] {
		t.Fatalf("profile management flags = %#v", managementByID)
	}
	if _, leaked := managementByID[otherProfileID]; leaked {
		t.Fatalf("account leaked cross-category profile %s", otherProfileID)
	}
}
