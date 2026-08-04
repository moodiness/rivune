package user

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestValidateUserInputs(t *testing.T) {
	if err := validateUsername("alice"); err != nil {
		t.Fatalf("valid username rejected: %v", err)
	}
	if err := validatePassword("long-enough-password"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	for _, role := range []string{"admin", "member"} {
		if err := validateRole(role); err != nil {
			t.Fatalf("valid role %q rejected: %v", role, err)
		}
	}
}

func TestValidateUserInputsRejectsInvalidValues(t *testing.T) {
	if err := validateUsername("ab"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected short username rejection, got %v", err)
	}
	if err := validatePassword("short"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected short password rejection, got %v", err)
	}
	if err := validateRole("owner"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unknown role rejection, got %v", err)
	}
}

func TestProfileAccessWaitsForCategoryMoveAndReturnsCommittedScope(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run profile access locking tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	const (
		actorID     = "7a000000-0000-4000-8000-000000000001"
		targetID    = "7a000000-0000-4000-8000-000000000002"
		categoryAID = "7a000000-0000-4000-8000-000000000003"
		categoryBID = "7a000000-0000-4000-8000-000000000004"
		profileAID  = "7a000000-0000-4000-8000-000000000005"
		profileBID  = "7a000000-0000-4000-8000-000000000006"
	)
	ctx := context.Background()
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{profileAID, profileBID})
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{actorID, targetID})
		_, _ = pool.Exec(ctx, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, []string{categoryAID, categoryBID})
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := pool.Exec(ctx, `
		WITH inserted_categories AS (
			INSERT INTO access_categories (id, name, normalized_name, position)
			VALUES ($1::uuid, 'Profile access lock A', 'profile access lock a', 970001),
			       ($2::uuid, 'Profile access lock B', 'profile access lock b', 970002)
			RETURNING id
		), inserted_users AS (
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($3::uuid, 'profile-access-lock-actor', 'unused-test-hash', 'admin'),
			       ($4::uuid, 'profile-access-lock-target', 'unused-test-hash', 'member')
			RETURNING id
		), inserted_profiles AS (
			INSERT INTO profiles (id, category_id, name)
			VALUES ($5::uuid, $1::uuid, 'Profile access lock A'),
			       ($6::uuid, $2::uuid, 'Profile access lock B')
			RETURNING id
		)
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($3::uuid, $5::uuid, true),
		       ($3::uuid, $6::uuid, true),
		       ($4::uuid, $5::uuid, false),
		       ($4::uuid, $6::uuid, true)
	`, categoryAID, categoryBID, actorID, targetID, profileAID, profileBID); err != nil {
		t.Fatalf("seed profile access locking boundary: %v", err)
	}

	categoryID := categoryAID
	categoryPrincipal := auth.Principal{
		UserID: actorID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
	}
	service := NewService(pool)
	initial, err := service.ProfileAccess(ctx, categoryPrincipal, targetID)
	if err != nil {
		t.Fatalf("list initial category profile access: %v", err)
	}
	if len(initial) != 1 || initial[0].ProfileID != profileAID || !initial[0].HasAccess || initial[0].CanManage {
		t.Fatalf("initial category profile access = %+v", initial)
	}

	moveTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent category move: %v", err)
	}
	moveCommitted := false
	defer func() {
		if !moveCommitted {
			_ = moveTx.Rollback(ctx)
		}
	}()
	if _, err := moveTx.Exec(ctx, `
		UPDATE profiles
		SET category_id = $2::uuid
		WHERE id = $1::uuid
	`, profileAID, categoryBID); err != nil {
		t.Fatalf("stage concurrent category move: %v", err)
	}

	type accessResult struct {
		access []ProfileAccess
		err    error
	}
	result := make(chan accessResult, 1)
	listContext, cancelList := context.WithTimeout(ctx, 5*time.Second)
	defer cancelList()
	go func() {
		access, listErr := service.ProfileAccess(listContext, categoryPrincipal, targetID)
		result <- accessResult{access: access, err: listErr}
	}()

	waitDeadline := time.Now().Add(3 * time.Second)
	for {
		var waitingLists int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%ProfileAccess: lock profiles before grants%'
		`).Scan(&waitingLists); err != nil {
			t.Fatalf("observe concurrent profile access list: %v", err)
		}
		if waitingLists > 0 {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("profile access list did not wait on the concurrent category move")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := moveTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent category move: %v", err)
	}
	moveCommitted = true
	listed := <-result
	if listed.err != nil {
		t.Fatalf("list profile access after concurrent move: %v", listed.err)
	}
	if len(listed.access) != 0 {
		t.Fatalf("category list returned profile after committed move: %+v", listed.access)
	}

	globalPrincipal := auth.Principal{
		UserID: actorID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}
	global, err := service.ProfileAccess(ctx, globalPrincipal, targetID)
	if err != nil {
		t.Fatalf("list global profile access: %v", err)
	}
	if len(global) != 2 {
		t.Fatalf("global profile access count = %d, want 2: %+v", len(global), global)
	}
	byID := make(map[string]ProfileAccess, len(global))
	for _, item := range global {
		byID[item.ProfileID] = item
	}
	if item := byID[profileAID]; !item.HasAccess || item.CanManage {
		t.Fatalf("global profile A access = %+v", item)
	}
	if item := byID[profileBID]; !item.HasAccess || !item.CanManage {
		t.Fatalf("global profile B access = %+v", item)
	}
}
