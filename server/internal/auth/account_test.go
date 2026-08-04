package auth

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/category"
)

func TestAccountRejectsProfileMovedBeforeSharedLock(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the PostgreSQL account profile race test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 4
	config.ConnConfig.RuntimeParams["application_name"] = "rivune-auth-account-test"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	position := int(time.Now().UnixNano()%1_000_000_000) + 1_000_000_000
	setupTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin account race setup: %v", err)
	}
	defer func() { _ = setupTx.Rollback(ctx) }()

	var categoryAID, categoryBID, userID, profileID string
	if err := setupTx.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Account A "+suffix, "account-a-"+suffix, position).Scan(&categoryAID); err != nil {
		t.Fatalf("insert first account category: %v", err)
	}
	if err := setupTx.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Account B "+suffix, "account-b-"+suffix, position+1).Scan(&categoryBID); err != nil {
		t.Fatalf("insert second account category: %v", err)
	}
	if err := setupTx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'test-password-hash', 'admin')
		RETURNING id::text
	`, "account_"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert account user: %v", err)
	}
	if err := setupTx.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id)
		VALUES ($1, $2::uuid)
		RETURNING id::text
	`, "Account profile "+suffix, categoryAID).Scan(&profileID); err != nil {
		t.Fatalf("insert account profile: %v", err)
	}
	if _, err := setupTx.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, true)
	`, userID, profileID); err != nil {
		t.Fatalf("insert account profile grant: %v", err)
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("commit account race setup: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext := context.Background()
		if _, err := pool.Exec(cleanupContext, `DELETE FROM profiles WHERE id = $1::uuid`, profileID); err != nil {
			t.Errorf("delete account profile: %v", err)
		}
		if _, err := pool.Exec(cleanupContext, `DELETE FROM users WHERE id = $1::uuid`, userID); err != nil {
			t.Errorf("delete account user: %v", err)
		}
		if _, err := pool.Exec(cleanupContext, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, []string{categoryAID, categoryBID}); err != nil {
			t.Errorf("delete account categories: %v", err)
		}
	})

	moveTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin winning account profile move: %v", err)
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
		t.Fatalf("stage winning account profile move: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := Principal{
		SessionID: "account-session", UserID: userID, DeviceID: "account-device",
		Username: "account_" + suffix, Role: "admin",
		AuthorizationScope: AuthorizationScopeCategory, CategoryID: &categoryAID,
		Category:        &category.CategoryRef{ID: categoryAID, Name: "Account A " + suffix},
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
		ActiveProfileCanManage: true,
	}
	service := &Service{pool: pool, timezone: "UTC"}
	accountContext, cancelAccount := context.WithTimeout(ctx, 5*time.Second)
	defer cancelAccount()
	accountResult := make(chan struct {
		account Account
		err     error
	}, 1)
	go func() {
		account, accountErr := service.Account(accountContext, principal)
		accountResult <- struct {
			account Account
			err     error
		}{account: account, err: accountErr}
	}()

	waitForAccountProfileLock(t, pool)
	if err := moveTx.Commit(ctx); err != nil {
		t.Fatalf("commit winning account profile move: %v", err)
	}
	moveFinished = true

	result := <-accountResult
	if result.err != nil {
		t.Fatalf("read account after profile move: %v", result.err)
	}
	if len(result.account.Profiles) != 0 {
		t.Fatalf("account leaked moved profile: %+v", result.account.Profiles)
	}
	if result.account.Principal.ActiveProfileID != nil || result.account.Principal.ProfileGrantExpiresAt != nil || result.account.Principal.ActiveProfileCanManage {
		t.Fatalf("account retained moved active profile: %+v", result.account.Principal)
	}
	if result.account.Principal.SessionID != principal.SessionID ||
		result.account.Principal.UserID != principal.UserID ||
		result.account.Principal.DeviceID != principal.DeviceID ||
		result.account.Principal.Username != principal.Username ||
		result.account.Principal.Role != principal.Role ||
		result.account.Principal.AuthorizationScope != principal.AuthorizationScope ||
		result.account.Principal.CategoryID == nil || *result.account.Principal.CategoryID != categoryAID ||
		result.account.Principal.Category == nil || result.account.Principal.Category.ID != categoryAID {
		t.Fatalf("account changed unrelated principal fields: got %+v want %+v", result.account.Principal, principal)
	}
}

func waitForAccountProfileLock(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND application_name = 'rivune-auth-account-test'
			  AND pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%WITH locked_profiles AS MATERIALIZED%'
		`).Scan(&waiting); err != nil {
			t.Fatalf("observe account profile lock: %v", err)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("account profile query did not wait on the staged category move")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
