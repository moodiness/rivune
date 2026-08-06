package category

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeleteLocksEveryReassignedProfileBeforeUpdatingSessions(t *testing.T) {
	pool := openCategoryDeleteTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var tokenHash [32]byte
	if _, err := rand.Read(tokenHash[:]); err != nil {
		t.Fatalf("generate session token hash: %v", err)
	}
	suffix := fmt.Sprintf("%x", tokenHash[:6])

	fixture, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin category deletion fixture: %v", err)
	}
	defer func() { _ = fixture.Rollback(context.Background()) }()
	if _, err := fixture.Exec(ctx, `LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock categories for fixture: %v", err)
	}

	var userID, sourceID, destinationID, firstProfileID, secondProfileID, deviceID, sessionID string
	if err := fixture.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'category-delete-lock-test', 'admin')
		RETURNING id::text
	`, "category-delete-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	var firstPosition int
	if err := fixture.QueryRow(ctx, `SELECT COALESCE(max(position), -1) + 1 FROM access_categories`).Scan(&firstPosition); err != nil {
		t.Fatalf("select fixture category position: %v", err)
	}
	if err := fixture.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Delete source "+suffix, "delete source "+suffix, firstPosition).Scan(&sourceID); err != nil {
		t.Fatalf("insert source category: %v", err)
	}
	if err := fixture.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Delete destination "+suffix, "delete destination "+suffix, firstPosition+1).Scan(&destinationID); err != nil {
		t.Fatalf("insert destination category: %v", err)
	}
	if err := fixture.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id)
		VALUES ($1, $2::uuid)
		RETURNING id::text
	`, "Delete first "+suffix, sourceID).Scan(&firstProfileID); err != nil {
		t.Fatalf("insert first profile: %v", err)
	}
	if err := fixture.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id)
		VALUES ($1, $2::uuid)
		RETURNING id::text
	`, "Delete second "+suffix, sourceID).Scan(&secondProfileID); err != nil {
		t.Fatalf("insert second profile: %v", err)
	}
	if err := fixture.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id)
		VALUES ($1::uuid, $2, 'test', $3::uuid)
		RETURNING id::text
	`, userID, "Delete device "+suffix, destinationID).Scan(&deviceID); err != nil {
		t.Fatalf("insert fixture device: %v", err)
	}
	if err := fixture.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			active_profile_id, profile_grant_expires_at, profile_context_hash, authorization_scope, category_id
		) VALUES (
			$1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '2 hours',
			$4::uuid, now() + interval '1 hour', decode(repeat('a2', 32), 'hex'), 'global_admin', NULL
		)
		RETURNING id::text
	`, userID, deviceID, tokenHash[:], firstProfileID).Scan(&sessionID); err != nil {
		t.Fatalf("insert fixture session: %v", err)
	}
	if err := fixture.Commit(ctx); err != nil {
		t.Fatalf("commit category deletion fixture: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth_sessions WHERE id = $1::uuid`, sessionID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_category_audit_events WHERE entity_id = ANY($1::uuid[])`, []string{sourceID, firstProfileID, secondProfileID, deviceID})
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{firstProfileID, secondProfileID})
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, []string{sourceID, destinationID})
	})

	sessionBlocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session blocker: %v", err)
	}
	defer func() { _ = sessionBlocker.Rollback(context.Background()) }()
	var lockedSessionID string
	if err := sessionBlocker.QueryRow(ctx, `SELECT id::text FROM auth_sessions WHERE id = $1::uuid FOR UPDATE`, sessionID).Scan(&lockedSessionID); err != nil {
		t.Fatalf("lock fixture session: %v", err)
	}

	deleteResult := make(chan error, 1)
	go func() {
		destination := destinationID
		deleteResult <- NewService(pool).Delete(ctx, Actor{UserID: userID, GlobalAdministrator: true}, sourceID, &destination)
	}()

	lockedProfiles := map[string]bool{firstProfileID: false, secondProfileID: false}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (!lockedProfiles[firstProfileID] || !lockedProfiles[secondProfileID]) {
		for profileID := range lockedProfiles {
			if lockedProfiles[profileID] {
				continue
			}
			locked, lockErr := profileLockedByAnotherTransaction(ctx, pool, profileID)
			if lockErr != nil {
				_ = sessionBlocker.Rollback(context.Background())
				t.Fatalf("probe reassigned profile lock: %v", lockErr)
			}
			lockedProfiles[profileID] = locked
		}
		if !lockedProfiles[firstProfileID] || !lockedProfiles[secondProfileID] {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !lockedProfiles[firstProfileID] || !lockedProfiles[secondProfileID] {
		_ = sessionBlocker.Rollback(context.Background())
		deleteErr := <-deleteResult
		t.Fatalf("Delete reached a locked auth session without first locking every affected profile (locks=%v, delete error=%v)", lockedProfiles, deleteErr)
	}

	if err := sessionBlocker.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatalf("release session blocker: %v", err)
	}
	if err := <-deleteResult; err != nil {
		t.Fatalf("delete category: %v", err)
	}

	for _, profileID := range []string{firstProfileID, secondProfileID} {
		var categoryID string
		if err := pool.QueryRow(ctx, `SELECT category_id::text FROM profiles WHERE id = $1::uuid`, profileID).Scan(&categoryID); err != nil {
			t.Fatalf("read reassigned profile: %v", err)
		}
		if categoryID != destinationID {
			t.Fatalf("profile %s category = %s, want %s", profileID, categoryID, destinationID)
		}
	}
	var activeProfileID *string
	var profileGrantExpiresAt *time.Time
	var revokedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT active_profile_id::text, profile_grant_expires_at, revoked_at
		FROM auth_sessions
		WHERE id = $1::uuid
	`, sessionID).Scan(&activeProfileID, &profileGrantExpiresAt, &revokedAt); err != nil {
		t.Fatalf("read cleared session selection: %v", err)
	}
	if activeProfileID != nil || profileGrantExpiresAt != nil {
		t.Fatalf("profile grant was not fully cleared: active=%v expires=%v", activeProfileID, profileGrantExpiresAt)
	}
	if revokedAt != nil {
		t.Fatalf("unaffected global administrator session was revoked at %v", *revokedAt)
	}
	var sourceExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM access_categories WHERE id = $1::uuid)`, sourceID).Scan(&sourceExists); err != nil {
		t.Fatalf("check deleted category: %v", err)
	}
	if sourceExists {
		t.Fatal("source category still exists after Delete")
	}
}

func TestUpdatePromotesExactlyOneDefaultCategory(t *testing.T) {
	pool := openCategoryDeleteTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var oldDefaultID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_categories WHERE is_default`).Scan(&oldDefaultID); err != nil {
		t.Fatalf("select original default category: %v", err)
	}
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate default category suffix: %v", err)
	}
	suffix := fmt.Sprintf("%x", random[:])
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'default-category-test-hash', 'admin')
		RETURNING id::text
	`, "default-category-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert category update actor: %v", err)
	}
	var position int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(position), -1) + 1 FROM access_categories`).Scan(&position); err != nil {
		t.Fatalf("select promoted category position: %v", err)
	}
	var promotedID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Promoted "+suffix, "promoted "+suffix, position).Scan(&promotedID); err != nil {
		t.Fatalf("insert promoted category: %v", err)
	}
	actor := Actor{UserID: userID, GlobalAdministrator: true}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = NewService(pool).Update(cleanupCtx, actor, oldDefaultID, UpdateInput{MakeDefault: true})
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_category_audit_events WHERE actor_user_id = $1::uuid`, userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_categories WHERE id = $1::uuid`, promotedID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})

	promoted, err := NewService(pool).Update(ctx, actor, promotedID, UpdateInput{MakeDefault: true})
	if err != nil {
		t.Fatalf("promote default category: %v", err)
	}
	if !promoted.IsDefault {
		t.Fatal("updated category was not returned as default")
	}
	var count int
	var currentDefaultID string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), min(id::text)
		FROM access_categories
		WHERE is_default
	`).Scan(&count, &currentDefaultID); err != nil {
		t.Fatalf("read promoted default category: %v", err)
	}
	if count != 1 || currentDefaultID != promotedID {
		t.Fatalf("default category state = count %d, id %s; want count 1, id %s", count, currentDefaultID, promotedID)
	}
}

func profileLockedByAnotherTransaction(ctx context.Context, pool *pgxpool.Pool, profileID string) (bool, error) {
	var selectedID string
	err := pool.QueryRow(ctx, `SELECT id::text FROM profiles WHERE id = $1::uuid FOR UPDATE NOWAIT`, profileID).Scan(&selectedID)
	if err == nil {
		return false, nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "55P03" {
		return true, nil
	}
	return false, err
}

func openCategoryDeleteTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run PostgreSQL category deletion tests")
	}
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse category deletion test database URL: %v", err)
	}
	configuration.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), configuration)
	if err != nil {
		t.Fatalf("open category deletion test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestDeleteRetainsExplicitResourcesAndRemovesCategoryOrphans(t *testing.T) {
	pool := openCategoryDeleteTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate category resource deletion suffix: %v", err)
	}
	suffix := fmt.Sprintf("%x", random[:])
	var userID, sourceID, ownerCategoryID, ownerProfileID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_categories WHERE is_default`).Scan(&ownerCategoryID); err != nil {
		t.Fatalf("select resource owner category: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'category-resource-delete-test', 'admin')
		RETURNING id::text
	`, "category-resource-delete-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert category resource deletion actor: %v", err)
	}
	var position int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(position), -1) + 1 FROM access_categories`).Scan(&position); err != nil {
		t.Fatalf("select category resource deletion position: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, position)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "Resource delete "+suffix, "resource-delete-"+suffix, position).Scan(&sourceID); err != nil {
		t.Fatalf("insert category resource deletion source: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO profiles (name, category_id)
		VALUES ($1, $2::uuid)
		RETURNING id::text
	`, "Resource owner "+suffix, ownerCategoryID).Scan(&ownerProfileID); err != nil {
		t.Fatalf("insert category resource owner: %v", err)
	}
	var addonIDs, collectionIDs []string
	if err := pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO profile_addons (profile_id, transport_url, manifest, manifest_id, manifest_version, position)
			SELECT $1::uuid, 'https://category-delete.example/' || $2 || '/' || label,
			       '{}'::jsonb, label || '-' || $2, '1.0.0', position
			FROM (VALUES ('explicit', 0), ('orphan', 1)) resources(label, position)
			RETURNING id, position
		)
		SELECT array_agg(id::text ORDER BY position) FROM inserted
	`, ownerProfileID, suffix).Scan(&addonIDs); err != nil {
		t.Fatalf("seed category deletion add-ons: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO profile_collections (profile_id, title, position)
			SELECT $1::uuid, label || '-' || $2, position
			FROM (VALUES ('explicit', 0), ('orphan', 1)) resources(label, position)
			RETURNING id, position
		)
		SELECT array_agg(id::text ORDER BY position) FROM inserted
	`, ownerProfileID, suffix).Scan(&collectionIDs); err != nil {
		t.Fatalf("seed category deletion collections: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profile_addons WHERE id = ANY($1::uuid[])`, addonIDs)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profile_collections WHERE id = ANY($1::uuid[])`, collectionIDs)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_category_audit_events WHERE actor_user_id = $1::uuid`, userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profiles WHERE id = $1::uuid`, ownerProfileID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_categories WHERE id = $1::uuid`, sourceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO addon_category_access (addon_id, category_id, position)
		VALUES ($1::uuid, $3::uuid, 0), ($2::uuid, $3::uuid, 1)
	`, addonIDs[0], addonIDs[1], sourceID); err != nil {
		t.Fatalf("grant category deletion category add-ons: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO addon_profile_access (addon_id, profile_id, position)
		VALUES ($1::uuid, $2::uuid, 0)
	`, addonIDs[0], ownerProfileID); err != nil {
		t.Fatalf("grant category deletion explicit add-on: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO collection_category_access (collection_id, category_id, position)
		VALUES ($1::uuid, $3::uuid, 0), ($2::uuid, $3::uuid, 1)
	`, collectionIDs[0], collectionIDs[1], sourceID); err != nil {
		t.Fatalf("grant category deletion category collections: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO collection_profile_access (collection_id, profile_id, position)
		VALUES ($1::uuid, $2::uuid, 0)
	`, collectionIDs[0], ownerProfileID); err != nil {
		t.Fatalf("grant category deletion explicit collection: %v", err)
	}

	if err := NewService(pool).Delete(ctx, Actor{UserID: userID, GlobalAdministrator: true}, sourceID, nil); err != nil {
		t.Fatalf("delete resource category: %v", err)
	}
	for _, resource := range []struct {
		label string
		table string
		ids   []string
	}{
		{label: "add-on", table: "profile_addons", ids: addonIDs},
		{label: "collection", table: "profile_collections", ids: collectionIDs},
	} {
		var retainedCount, orphanCount int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+resource.table+" WHERE id = $1::uuid", resource.ids[0]).Scan(&retainedCount); err != nil {
			t.Fatalf("count retained explicit %s: %v", resource.label, err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+resource.table+" WHERE id = $1::uuid", resource.ids[1]).Scan(&orphanCount); err != nil {
			t.Fatalf("count category-only orphan %s: %v", resource.label, err)
		}
		if retainedCount != 1 || orphanCount != 0 {
			t.Errorf("%s category deletion state = retained %d, orphan %d; want 1 and 0", resource.label, retainedCount, orphanCount)
		}
	}
	var survivingExplicitAssignments int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM addon_profile_access WHERE addon_id = $1::uuid AND profile_id = $3::uuid) +
			(SELECT count(*) FROM collection_profile_access WHERE collection_id = $2::uuid AND profile_id = $3::uuid)
	`, addonIDs[0], collectionIDs[0], ownerProfileID).Scan(&survivingExplicitAssignments); err != nil {
		t.Fatalf("count surviving explicit assignments: %v", err)
	}
	if survivingExplicitAssignments != 2 {
		t.Errorf("surviving explicit assignment count = %d, want 2", survivingExplicitAssignments)
	}
}
