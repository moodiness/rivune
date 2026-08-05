package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeviceUserCodesUseUnambiguousAlphabet(t *testing.T) {
	for range 100 {
		code, err := newDeviceUserCode()
		if err != nil {
			t.Fatalf("generate user code: %v", err)
		}
		if len(code) != deviceUserCodeLength {
			t.Fatalf("expected %d characters, got %q", deviceUserCodeLength, code)
		}
		for _, character := range code {
			if !containsRune(deviceUserCodeAlphabet, character) {
				t.Fatalf("generated ambiguous character %q in %q", character, code)
			}
		}
	}
}

func TestDeviceUserCodeFormattingAndNormalization(t *testing.T) {
	if formatted := formatDeviceUserCode("ABCDEFGH"); formatted != "ABCD-EFGH" {
		t.Fatalf("unexpected formatted code %q", formatted)
	}
	for _, input := range []string{"abcd-efgh", "ABCD EFGH", "  ABCDEFGH  "} {
		if normalized := normalizeDeviceUserCode(input); normalized != "ABCDEFGH" {
			t.Fatalf("unexpected normalization %q for %q", normalized, input)
		}
	}
}

func TestAdministratorAuthorityRequiresGlobalPasswordScope(t *testing.T) {
	categoryID := "category-id"
	global := Principal{Role: "admin", AuthorizationScope: AuthorizationScopeGlobalAdministrator}
	if !global.IsGlobalAdministrator() {
		t.Fatal("expected administrator global session to have global authority")
	}
	for _, principal := range []Principal{
		{Role: "admin", AuthorizationScope: AuthorizationScopeCategory, CategoryID: &categoryID},
		{Role: "member", AuthorizationScope: AuthorizationScopeGlobalAdministrator},
		{Role: "admin"},
	} {
		if principal.IsGlobalAdministrator() {
			t.Fatalf("paired or invalid principal inherited global authority: %+v", principal)
		}
	}
}

func TestAuthenticateAndRefreshScopeValidationRejectsCategoryMismatch(t *testing.T) {
	categoryID := "category-a"
	otherCategoryID := "category-b"
	if !validSessionScope("admin", AuthorizationScopeCategory, &categoryID, &categoryID, nil) {
		t.Fatal("expected paired administrator session to remain valid in its category")
	}
	if !validSessionScope("member", AuthorizationScopeCategory, &categoryID, &categoryID, &categoryID) {
		t.Fatal("expected same-category active profile to remain valid")
	}
	for _, test := range []struct {
		name                        string
		role                        string
		scope                       AuthorizationScope
		session, device, profileCat *string
	}{
		{name: "paired admin device moved", role: "admin", scope: AuthorizationScopeCategory, session: &categoryID, device: &otherCategoryID},
		{name: "active profile moved", role: "member", scope: AuthorizationScopeCategory, session: &categoryID, device: &categoryID, profileCat: &otherCategoryID},
		{name: "category omitted", role: "member", scope: AuthorizationScopeCategory, device: &categoryID},
		{name: "member claims global", role: "member", scope: AuthorizationScopeGlobalAdministrator},
		{name: "unknown scope", role: "admin", scope: AuthorizationScope("unknown")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if validSessionScope(test.role, test.scope, test.session, test.device, test.profileCat) {
				t.Fatal("expected mismatched or escalated scope to be rejected")
			}
		})
	}
}

func TestApproveDeviceAuthorizationSerializesManagedProfileCategoryMoves(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the PostgreSQL device approval race test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 4
	config.ConnConfig.RuntimeParams["application_name"] = "rivune-auth-device-approval-test"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	firstCode, err := newDeviceUserCode()
	if err != nil {
		t.Fatalf("generate first user code: %v", err)
	}
	secondCode, err := newDeviceUserCode()
	if err != nil {
		t.Fatalf("generate second user code: %v", err)
	}
	for secondCode == firstCode {
		secondCode, err = newDeviceUserCode()
		if err != nil {
			t.Fatalf("regenerate second user code: %v", err)
		}
	}
	suffix := strings.ToLower(firstCode)
	position := int(time.Now().UnixNano()%1_000_000_000) + 1_000_000_000

	setupTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin device approval race setup: %v", err)
	}
	defer func() { _ = setupTx.Rollback(ctx) }()
	var categoryAID, categoryBID, userID, profileID string
	if err := setupTx.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Approval A "+firstCode, "approval-a-"+suffix, position).Scan(&categoryAID); err != nil {
		t.Fatalf("insert first category: %v", err)
	}
	if err := setupTx.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Approval B "+firstCode, "approval-b-"+suffix, position+1).Scan(&categoryBID); err != nil {
		t.Fatalf("insert second category: %v", err)
	}
	if err := setupTx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'test-password-hash', 'admin')
		RETURNING id::text
	`, "approval_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert category manager: %v", err)
	}
	if err := setupTx.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id)
		VALUES ($1, $2::uuid)
		RETURNING id::text
	`, "Approval profile "+firstCode, categoryAID).Scan(&profileID); err != nil {
		t.Fatalf("insert managed profile: %v", err)
	}
	if _, err := setupTx.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, true)
	`, userID, profileID); err != nil {
		t.Fatalf("insert management grant: %v", err)
	}
	legacySourceHash := deviceAuthorizationSourceHash("")
	for index, code := range []string{firstCode, secondCode} {
		deviceCodeHash := make([]byte, 32)
		if _, err := rand.Read(deviceCodeHash); err != nil {
			t.Fatalf("generate device code hash %d: %v", index, err)
		}
		if _, err := setupTx.Exec(ctx, `
			INSERT INTO device_authorizations (
				device_code_hash, user_code, device_name, platform, source_hash, expires_at
			) VALUES ($1, $2, $3, 'test', $4, now() + interval '10 minutes')
		`, deviceCodeHash, code, fmt.Sprintf("Approval device %d", index), legacySourceHash[:]); err != nil {
			t.Fatalf("insert device authorization %d: %v", index, err)
		}
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("commit device approval race setup: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext := context.Background()
		if _, err := pool.Exec(cleanupContext, `
			DELETE FROM device_authorizations WHERE user_code = ANY($1::text[])
		`, []string{firstCode, secondCode}); err != nil {
			t.Errorf("delete test device authorizations: %v", err)
		}
		if _, err := pool.Exec(cleanupContext, `
			DELETE FROM profiles WHERE id = $1::uuid
		`, profileID); err != nil {
			t.Errorf("delete test profile: %v", err)
		}
		if _, err := pool.Exec(cleanupContext, `
			DELETE FROM users WHERE id = $1::uuid
		`, userID); err != nil {
			t.Errorf("delete test user: %v", err)
		}
		if _, err := pool.Exec(cleanupContext, `
			DELETE FROM access_categories WHERE id = ANY($1::uuid[])
		`, []string{categoryAID, categoryBID}); err != nil {
			t.Errorf("delete test categories: %v", err)
		}
	})

	service := &Service{pool: pool}
	principal := Principal{
		UserID: userID, Role: "admin", AuthorizationScope: AuthorizationScopeCategory,
		CategoryID: &categoryAID,
	}

	t.Run("committed move wins before authorization", func(t *testing.T) {
		moveTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin winning profile move: %v", err)
		}
		moveFinished := false
		defer func() {
			if !moveFinished {
				_ = moveTx.Rollback(ctx)
			}
		}()
		if _, err := moveTx.Exec(ctx, `
			UPDATE profiles SET category_id = $2::uuid WHERE id = $1::uuid
		`, profileID, categoryBID); err != nil {
			t.Fatalf("stage winning profile move: %v", err)
		}

		approvalContext, cancelApproval := context.WithTimeout(ctx, 5*time.Second)
		defer cancelApproval()
		approvalResult := make(chan error, 1)
		go func() {
			approvalResult <- service.ApproveDeviceAuthorization(
				approvalContext, principal, DeviceAuthorizationApproval{
					UserCode: firstCode, CategoryID: categoryAID,
				},
			)
		}()
		waitForDeviceApprovalLock(t, pool, "SELECT profile.id::text")
		if err := moveTx.Commit(ctx); err != nil {
			t.Fatalf("commit winning profile move: %v", err)
		}
		moveFinished = true
		if err := <-approvalResult; !errors.Is(err, ErrForbidden) {
			t.Fatalf("approval after managed profile move error = %v, want %v", err, ErrForbidden)
		}
		var approvedUserID, approvedCategoryID *string
		if err := pool.QueryRow(ctx, `
			SELECT approved_user_id::text, approved_category_id::text
			FROM device_authorizations
			WHERE user_code = $1
		`, firstCode).Scan(&approvedUserID, &approvedCategoryID); err != nil {
			t.Fatalf("read rejected approval: %v", err)
		}
		if approvedUserID != nil || approvedCategoryID != nil {
			t.Fatalf("category move raced into stale approval: user=%v category=%v", approvedUserID, approvedCategoryID)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE profiles SET category_id = $2::uuid WHERE id = $1::uuid
		`, profileID, categoryAID); err != nil {
			t.Fatalf("restore managed profile category: %v", err)
		}
	})

	t.Run("authorization locks survive until approval commit", func(t *testing.T) {
		gateTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin approval gate: %v", err)
		}
		gateFinished := false
		defer func() {
			if !gateFinished {
				_ = gateTx.Rollback(ctx)
			}
		}()
		if _, err := gateTx.Exec(ctx, `
			SELECT id FROM device_authorizations WHERE user_code = $1 FOR UPDATE
		`, secondCode); err != nil {
			t.Fatalf("lock pending device authorization: %v", err)
		}

		approvalContext, cancelApproval := context.WithTimeout(ctx, 5*time.Second)
		defer cancelApproval()
		approvalResult := make(chan error, 1)
		go func() {
			approvalResult <- service.ApproveDeviceAuthorization(
				approvalContext, principal, DeviceAuthorizationApproval{
					UserCode: secondCode, CategoryID: categoryAID,
				},
			)
		}()
		waitForDeviceApprovalLock(t, pool, "FROM device_authorizations")

		moveTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin blocked profile move: %v", err)
		}
		moveResult := make(chan error, 1)
		go func() {
			if _, moveErr := moveTx.Exec(ctx, `
				UPDATE profiles SET category_id = $2::uuid WHERE id = $1::uuid
			`, profileID, categoryBID); moveErr != nil {
				_ = moveTx.Rollback(ctx)
				moveResult <- moveErr
				return
			}
			moveResult <- moveTx.Commit(ctx)
		}()
		waitForDeviceApprovalLock(t, pool, "UPDATE profiles SET category_id")

		if err := gateTx.Commit(ctx); err != nil {
			t.Fatalf("release approval gate: %v", err)
		}
		gateFinished = true
		if err := <-approvalResult; err != nil {
			t.Fatalf("approve while profile remains locked: %v", err)
		}
		if err := <-moveResult; err != nil {
			t.Fatalf("move profile after approval commit: %v", err)
		}
		var approvedUserID, approvedCategoryID string
		if err := pool.QueryRow(ctx, `
			SELECT approved_user_id::text, approved_category_id::text
			FROM device_authorizations
			WHERE user_code = $1
		`, secondCode).Scan(&approvedUserID, &approvedCategoryID); err != nil {
			t.Fatalf("read serialized approval: %v", err)
		}
		if approvedUserID != userID || approvedCategoryID != categoryAID {
			t.Fatalf("serialized approval = user %q category %q, want user %q category %q", approvedUserID, approvedCategoryID, userID, categoryAID)
		}
	})
}

func waitForDeviceApprovalLock(t *testing.T, pool *pgxpool.Pool, queryFragment string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND application_name = 'rivune-auth-device-approval-test'
			  AND pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%' || $1 || '%'
		`, queryFragment).Scan(&waiting); err != nil {
			t.Fatalf("observe device approval lock: %v", err)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("query containing %q did not wait on the expected lock", queryFragment)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func containsRune(value string, target rune) bool {
	for _, character := range value {
		if character == target {
			return true
		}
	}
	return false
}
