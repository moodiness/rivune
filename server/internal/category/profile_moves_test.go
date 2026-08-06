package category

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type profileMoveFixture struct {
	ctx         context.Context
	service     *Service
	actor       Actor
	sourceID    string
	destination string
	otherID     string
	firstID     string
	secondID    string
	ownerOneID  string
	ownerTwoID  string
}

func newProfileMoveFixture(t *testing.T) profileMoveFixture {
	t.Helper()
	pool := openCategoryDeleteTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	fixture := profileMoveFixture{ctx: ctx, service: NewService(pool)}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'profile-move-test', 'admin')
		RETURNING id::text
	`, "profile-move-"+suffix).Scan(&fixture.actor.UserID); err != nil {
		t.Fatalf("insert profile move actor: %v", err)
	}
	fixture.actor.GlobalAdministrator = true
	var position int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(position), -1) + 1 FROM access_categories`).Scan(&position); err != nil {
		t.Fatalf("select category position: %v", err)
	}
	categoryIDs := []*string{&fixture.sourceID, &fixture.destination, &fixture.otherID}
	for index, id := range categoryIDs {
		name := fmt.Sprintf("Move category %d %s", index, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO access_categories (name, normalized_name, position)
			VALUES ($1, $2, $3)
			RETURNING id::text
		`, name, fmt.Sprintf("move category %d %s", index, suffix), position+index).Scan(id); err != nil {
			t.Fatalf("insert category %d: %v", index, err)
		}
	}
	profiles := []struct {
		id         *string
		categoryID string
		name       string
	}{
		{&fixture.firstID, fixture.sourceID, "Move first " + suffix},
		{&fixture.secondID, fixture.sourceID, "Move second " + suffix},
		{&fixture.ownerOneID, fixture.otherID, "Move owner one " + suffix},
		{&fixture.ownerTwoID, fixture.otherID, "Move owner two " + suffix},
	}
	for _, profile := range profiles {
		if err := pool.QueryRow(ctx, `
			INSERT INTO profiles (name, category_id)
			VALUES ($1, $2::uuid)
			RETURNING id::text
		`, profile.name, profile.categoryID).Scan(profile.id); err != nil {
			t.Fatalf("insert profile %q: %v", profile.name, err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		profileIDs := []string{fixture.firstID, fixture.secondID, fixture.ownerOneID, fixture.ownerTwoID}
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profile_addons WHERE profile_id = ANY($1::uuid[])`, profileIDs)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profile_collections WHERE profile_id = ANY($1::uuid[])`, profileIDs)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_category_audit_events WHERE actor_user_id = $1::uuid`, fixture.actor.UserID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{fixture.firstID, fixture.secondID, fixture.ownerOneID, fixture.ownerTwoID})
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1::uuid`, fixture.actor.UserID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, []string{fixture.sourceID, fixture.destination, fixture.otherID})
	})
	return fixture
}

func (fixture profileMoveFixture) addDestinationCollections(t *testing.T, count int) {
	t.Helper()
	if _, err := fixture.service.pool.Exec(fixture.ctx, `
		WITH inserted AS (
			INSERT INTO profile_collections (profile_id, title, position)
			SELECT $1::uuid, 'Move destination collection ' || value, value
			FROM generate_series(0, $3::integer - 1) value
			RETURNING id, position
		)
		INSERT INTO collection_category_access (collection_id, category_id, position)
		SELECT id, $2::uuid, position FROM inserted
	`, fixture.ownerOneID, fixture.destination, count); err != nil {
		t.Fatalf("insert destination collections: %v", err)
	}
}

func (fixture profileMoveFixture) addExplicitCollection(t *testing.T, profileID string) string {
	t.Helper()
	var collectionID string
	if err := fixture.service.pool.QueryRow(fixture.ctx, `
		INSERT INTO profile_collections (profile_id, title, position)
		VALUES ($1::uuid, 'Move explicit collection', (SELECT count(*) FROM profile_collections WHERE profile_id = $1::uuid))
		RETURNING id::text
	`, fixture.ownerTwoID).Scan(&collectionID); err != nil {
		t.Fatalf("insert explicit collection: %v", err)
	}
	if _, err := fixture.service.pool.Exec(fixture.ctx, `
		INSERT INTO collection_profile_access (collection_id, profile_id, position)
		VALUES ($1::uuid, $2::uuid, (SELECT count(*) FROM collection_profile_access WHERE profile_id = $2::uuid))
	`, collectionID, profileID); err != nil {
		t.Fatalf("grant explicit collection: %v", err)
	}
	return collectionID
}

func (fixture profileMoveFixture) addAddon(t *testing.T, ownerID, transportURL string) string {
	t.Helper()
	var addonID string
	if err := fixture.service.pool.QueryRow(fixture.ctx, `
		INSERT INTO profile_addons (profile_id, transport_url, manifest, manifest_id, manifest_version, position)
		VALUES ($1::uuid, $2, '{"id":"profile-move-test","version":"1"}'::jsonb,
		        'profile-move-test', '1', (SELECT count(*) FROM profile_addons WHERE profile_id = $1::uuid))
		RETURNING id::text
	`, ownerID, transportURL).Scan(&addonID); err != nil {
		t.Fatalf("insert add-on: %v", err)
	}
	return addonID
}

func (fixture profileMoveFixture) assertCategories(t *testing.T, expected string, profileIDs ...string) {
	t.Helper()
	for _, profileID := range profileIDs {
		var categoryID string
		if err := fixture.service.pool.QueryRow(fixture.ctx, `SELECT category_id::text FROM profiles WHERE id = $1::uuid`, profileID).Scan(&categoryID); err != nil {
			t.Fatalf("read profile category: %v", err)
		}
		if categoryID != expected {
			t.Fatalf("profile %s category = %s, want %s", profileID, categoryID, expected)
		}
	}
}

func TestMoveProfilesAllowsSingleAndBatchDestinationOverlap(t *testing.T) {
	fixture := newProfileMoveFixture(t)
	fixture.addDestinationCollections(t, 100)
	var collectionID string
	if err := fixture.service.pool.QueryRow(fixture.ctx, `
		SELECT collection_id::text
		FROM collection_category_access
		WHERE category_id = $1::uuid
		ORDER BY position
		LIMIT 1
	`, fixture.destination).Scan(&collectionID); err != nil {
		t.Fatalf("select overlapping destination collection: %v", err)
	}
	if _, err := fixture.service.pool.Exec(fixture.ctx, `
		INSERT INTO collection_profile_access (collection_id, profile_id, position)
		VALUES ($1::uuid, $2::uuid, 0)
	`, collectionID, fixture.firstID); err != nil {
		t.Fatalf("grant overlapping explicit collection: %v", err)
	}
	addonID := fixture.addAddon(t, fixture.ownerOneID, "https://overlap.example/manifest.json")
	if _, err := fixture.service.pool.Exec(fixture.ctx, `INSERT INTO addon_profile_access (addon_id, profile_id, position) VALUES ($1::uuid, $2::uuid, 0)`, addonID, fixture.firstID); err != nil {
		t.Fatalf("grant overlapping explicit add-on access: %v", err)
	}
	if _, err := fixture.service.pool.Exec(fixture.ctx, `INSERT INTO addon_category_access (addon_id, category_id, position) VALUES ($1::uuid, $2::uuid, 0)`, addonID, fixture.destination); err != nil {
		t.Fatalf("grant overlapping category add-on access: %v", err)
	}
	if err := fixture.service.MoveProfile(fixture.ctx, fixture.actor, fixture.firstID, fixture.destination); err != nil {
		t.Fatalf("move single profile with overlapping access: %v", err)
	}
	fixture.assertCategories(t, fixture.destination, fixture.firstID)
	if err := fixture.service.MoveProfile(fixture.ctx, fixture.actor, fixture.firstID, fixture.sourceID); err != nil {
		t.Fatalf("move single profile back to source: %v", err)
	}
	fixture.assertCategories(t, fixture.sourceID, fixture.firstID)
	if err := fixture.service.MoveProfiles(fixture.ctx, fixture.actor, []string{fixture.firstID, fixture.secondID}, fixture.destination); err != nil {
		t.Fatalf("batch move with overlapping access: %v", err)
	}
	fixture.assertCategories(t, fixture.destination, fixture.firstID, fixture.secondID)
}

func TestMoveProfilesRejectsExplicitPlusDestinationCollectionOverflowAtomically(t *testing.T) {
	fixture := newProfileMoveFixture(t)
	fixture.addDestinationCollections(t, 100)
	fixture.addExplicitCollection(t, fixture.firstID)
	err := fixture.service.MoveProfiles(fixture.ctx, fixture.actor, []string{fixture.firstID, fixture.secondID}, fixture.destination)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("overflowing batch move error = %v, want %v", err, ErrInvalidInput)
	}
	fixture.assertCategories(t, fixture.sourceID, fixture.firstID, fixture.secondID)
	var auditCount int
	if err := fixture.service.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM access_category_audit_events
		WHERE actor_user_id = $1::uuid AND entity_id = ANY($2::uuid[])
	`, fixture.actor.UserID, []string{fixture.firstID, fixture.secondID}).Scan(&auditCount); err != nil {
		t.Fatalf("count rejected move audits: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("rejected batch move wrote %d audit events", auditCount)
	}
}

func TestMoveProfileRejectsDuplicateConfiguredAddonTransport(t *testing.T) {
	fixture := newProfileMoveFixture(t)
	const transportURL = "https://duplicate.example/manifest.json"
	explicitAddonID := fixture.addAddon(t, fixture.ownerOneID, transportURL)
	categoryAddonID := fixture.addAddon(t, fixture.ownerTwoID, transportURL)
	if _, err := fixture.service.pool.Exec(fixture.ctx, `INSERT INTO addon_profile_access (addon_id, profile_id, position) VALUES ($1::uuid, $2::uuid, 0)`, explicitAddonID, fixture.firstID); err != nil {
		t.Fatalf("grant conflicting explicit add-on access: %v", err)
	}
	if _, err := fixture.service.pool.Exec(fixture.ctx, `INSERT INTO addon_category_access (addon_id, category_id, position) VALUES ($1::uuid, $2::uuid, 0)`, categoryAddonID, fixture.destination); err != nil {
		t.Fatalf("grant conflicting category add-on access: %v", err)
	}
	if err := fixture.service.MoveProfile(fixture.ctx, fixture.actor, fixture.firstID, fixture.destination); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate transport move error = %v, want %v", err, ErrInvalidInput)
	}
	fixture.assertCategories(t, fixture.sourceID, fixture.firstID)
}

func TestDeleteRejectsCategoryReassignmentThatConflictsWithAddonTransport(t *testing.T) {
	fixture := newProfileMoveFixture(t)
	const transportURL = "https://delete-conflict.example/manifest.json"
	explicitAddonID := fixture.addAddon(t, fixture.ownerOneID, transportURL)
	categoryAddonID := fixture.addAddon(t, fixture.ownerTwoID, transportURL)
	if _, err := fixture.service.pool.Exec(fixture.ctx, `INSERT INTO addon_profile_access (addon_id, profile_id, position) VALUES ($1::uuid, $2::uuid, 0)`, explicitAddonID, fixture.firstID); err != nil {
		t.Fatalf("grant deletion conflict explicit add-on access: %v", err)
	}
	if _, err := fixture.service.pool.Exec(fixture.ctx, `INSERT INTO addon_category_access (addon_id, category_id, position) VALUES ($1::uuid, $2::uuid, 0)`, categoryAddonID, fixture.destination); err != nil {
		t.Fatalf("grant deletion conflict category add-on access: %v", err)
	}
	destination := fixture.destination
	if err := fixture.service.Delete(fixture.ctx, fixture.actor, fixture.sourceID, &destination); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("conflicting deletion reassignment error = %v, want %v", err, ErrInvalidInput)
	}
	fixture.assertCategories(t, fixture.sourceID, fixture.firstID, fixture.secondID)
	var sourceExists bool
	if err := fixture.service.pool.QueryRow(fixture.ctx, `SELECT EXISTS (SELECT 1 FROM access_categories WHERE id = $1::uuid)`, fixture.sourceID).Scan(&sourceExists); err != nil {
		t.Fatalf("check rejected source deletion: %v", err)
	}
	if !sourceExists {
		t.Fatal("source category was deleted after rejected reassignment")
	}
}

func TestMoveProfilesRejectsStaleGlobalAdministrator(t *testing.T) {
	fixture := newProfileMoveFixture(t)
	if _, err := fixture.service.pool.Exec(fixture.ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, fixture.actor.UserID); err != nil {
		t.Fatalf("demote profile move actor: %v", err)
	}
	var auditCountBefore int
	if err := fixture.service.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM access_category_audit_events WHERE actor_user_id = $1::uuid`, fixture.actor.UserID).Scan(&auditCountBefore); err != nil {
		t.Fatalf("count audit events before stale profile move: %v", err)
	}
	if err := fixture.service.MoveProfiles(fixture.ctx, fixture.actor, []string{fixture.firstID, fixture.secondID}, fixture.destination); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale global profile move error = %v, want %v", err, ErrForbidden)
	}
	fixture.assertCategories(t, fixture.sourceID, fixture.firstID, fixture.secondID)
	var auditCountAfter int
	if err := fixture.service.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM access_category_audit_events WHERE actor_user_id = $1::uuid`, fixture.actor.UserID).Scan(&auditCountAfter); err != nil {
		t.Fatalf("count audit events after stale profile move: %v", err)
	}
	if auditCountAfter != auditCountBefore {
		t.Fatalf("stale global profile move audit count = %d, want unchanged %d", auditCountAfter, auditCountBefore)
	}
}

func TestDeleteRejectsStaleGlobalAdministrator(t *testing.T) {
	fixture := newProfileMoveFixture(t)
	if _, err := fixture.service.pool.Exec(fixture.ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, fixture.actor.UserID); err != nil {
		t.Fatalf("demote category deletion actor: %v", err)
	}
	var auditCountBefore int
	if err := fixture.service.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM access_category_audit_events WHERE actor_user_id = $1::uuid`, fixture.actor.UserID).Scan(&auditCountBefore); err != nil {
		t.Fatalf("count audit events before stale category deletion: %v", err)
	}
	destination := fixture.destination
	if err := fixture.service.Delete(fixture.ctx, fixture.actor, fixture.sourceID, &destination); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale global category deletion error = %v, want %v", err, ErrForbidden)
	}
	fixture.assertCategories(t, fixture.sourceID, fixture.firstID, fixture.secondID)
	var sourceExists bool
	if err := fixture.service.pool.QueryRow(fixture.ctx, `SELECT EXISTS (SELECT 1 FROM access_categories WHERE id = $1::uuid)`, fixture.sourceID).Scan(&sourceExists); err != nil {
		t.Fatalf("check source category after stale deletion: %v", err)
	}
	if !sourceExists {
		t.Fatal("stale global actor deleted the source category")
	}
	var auditCountAfter int
	if err := fixture.service.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM access_category_audit_events WHERE actor_user_id = $1::uuid`, fixture.actor.UserID).Scan(&auditCountAfter); err != nil {
		t.Fatalf("count audit events after stale category deletion: %v", err)
	}
	if auditCountAfter != auditCountBefore {
		t.Fatalf("stale global category deletion audit count = %d, want unchanged %d", auditCountAfter, auditCountBefore)
	}
}
