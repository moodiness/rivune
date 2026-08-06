package profile

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

type profileInvariantFixture struct {
	ctx             context.Context
	pool            *pgxpool.Pool
	categoryIDs     [2]string
	administratorID string
	nameSuffix      string
}

type storedProfileMutationState struct {
	categoryID string
	enabled    bool
	updatedAt  time.Time
	version    string
}

func newProfileInvariantFixture(t *testing.T) profileInvariantFixture {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run profile invariant tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	position := int(time.Now().UnixNano()%500_000_000) + 1_000_000_000
	fixture := profileInvariantFixture{ctx: ctx, pool: pool, nameSuffix: suffix}
	for index := range fixture.categoryIDs {
		if err := pool.QueryRow(ctx, `
			INSERT INTO access_categories (name, normalized_name, position)
			VALUES ($1, $2, $3)
			RETURNING id::text
		`, "Profile invariant "+suffix+" "+strconv.Itoa(index), "profile-invariant-"+suffix+"-"+strconv.Itoa(index), position+index).Scan(&fixture.categoryIDs[index]); err != nil {
			t.Fatalf("create profile invariant category %d: %v", index, err)
		}
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-test-hash', 'admin')
		RETURNING id::text
	`, "profile-invariant-admin-"+suffix).Scan(&fixture.administratorID); err != nil {
		t.Fatalf("create profile invariant administrator: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext := context.Background()
		_, _ = pool.Exec(cleanupContext, `DELETE FROM profile_addons WHERE profile_id IN (SELECT id FROM profiles WHERE category_id = ANY($1::uuid[]))`, fixture.categoryIDs[:])
		_, _ = pool.Exec(cleanupContext, `DELETE FROM profile_collections WHERE profile_id IN (SELECT id FROM profiles WHERE category_id = ANY($1::uuid[]))`, fixture.categoryIDs[:])
		_, _ = pool.Exec(cleanupContext, `DELETE FROM profiles WHERE category_id = ANY($1::uuid[])`, fixture.categoryIDs[:])
		_, _ = pool.Exec(cleanupContext, `DELETE FROM users WHERE id = $1::uuid`, fixture.administratorID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, fixture.categoryIDs[:])
	})
	return fixture
}

func (fixture profileInvariantFixture) insertProfile(t *testing.T, categoryID, label string) string {
	t.Helper()
	var profileID string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO profiles (name, category_id)
		VALUES ($1, $2::uuid)
		RETURNING id::text
	`, label+" "+fixture.nameSuffix, categoryID).Scan(&profileID); err != nil {
		t.Fatalf("create profile %q: %v", label, err)
	}
	return profileID
}

func (fixture profileInvariantFixture) profileState(t *testing.T, profileID string) storedProfileMutationState {
	t.Helper()
	var state storedProfileMutationState
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT category_id::text, enabled, updated_at, xmin::text
		FROM profiles
		WHERE id = $1::uuid
	`, profileID).Scan(&state.categoryID, &state.enabled, &state.updatedAt, &state.version); err != nil {
		t.Fatalf("read stored profile state: %v", err)
	}
	return state
}

func assertStoredProfileStateEqual(t *testing.T, got, want storedProfileMutationState) {
	t.Helper()
	if got.categoryID != want.categoryID || got.enabled != want.enabled || !got.updatedAt.Equal(want.updatedAt) || got.version != want.version {
		t.Fatalf("stored profile state changed after refusal: got %+v, want %+v", got, want)
	}
}

func globalProfileAdministrator(fixture profileInvariantFixture) auth.Principal {
	return auth.Principal{UserID: fixture.administratorID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}
}

func TestUpdateProtectsUnrestrictedProfilePerCategoryAndRollsBack(t *testing.T) {
	fixture := newProfileInvariantFixture(t)
	service := NewService(fixture.pool, time.Hour, "UTC")
	targetID := fixture.insertProfile(t, fixture.categoryIDs[0], "Target")
	fixture.insertProfile(t, fixture.categoryIDs[1], "Other category")
	original := fixture.profileState(t, targetID)
	disabled := false

	if _, err := service.Update(fixture.ctx, globalProfileAdministrator(fixture), targetID, UpdateInput{Enabled: &disabled}); !errors.Is(err, ErrLastUnrestrictedProfile) {
		t.Fatalf("disable final unrestricted category profile error = %v, want %v", err, ErrLastUnrestrictedProfile)
	}
	assertStoredProfileStateEqual(t, fixture.profileState(t, targetID), original)

	destinationCategoryID := fixture.categoryIDs[1]
	if _, err := service.Update(fixture.ctx, globalProfileAdministrator(fixture), targetID, UpdateInput{CategoryID: &destinationCategoryID}); !errors.Is(err, ErrLastUnrestrictedProfile) {
		t.Fatalf("move final unrestricted category profile error = %v, want %v", err, ErrLastUnrestrictedProfile)
	}
	assertStoredProfileStateEqual(t, fixture.profileState(t, targetID), original)

	fixture.insertProfile(t, fixture.categoryIDs[0], "Same category")
	updated, err := service.Update(fixture.ctx, globalProfileAdministrator(fixture), targetID, UpdateInput{Enabled: &disabled})
	if err != nil {
		t.Fatalf("disable profile with same-category unrestricted peer: %v", err)
	}
	if updated.Enabled {
		t.Fatal("accepted profile update did not disable the target")
	}
	stored := fixture.profileState(t, targetID)
	if stored.enabled {
		t.Fatal("accepted profile update was not persisted")
	}
}

func TestConcurrentUpdatesCannotRemoveEveryUnrestrictedCategoryProfile(t *testing.T) {
	fixture := newProfileInvariantFixture(t)
	service := NewService(fixture.pool, time.Hour, "UTC")
	profileIDs := [2]string{
		fixture.insertProfile(t, fixture.categoryIDs[0], "Concurrent A"),
		fixture.insertProfile(t, fixture.categoryIDs[0], "Concurrent B"),
	}
	original := map[string]storedProfileMutationState{
		profileIDs[0]: fixture.profileState(t, profileIDs[0]),
		profileIDs[1]: fixture.profileState(t, profileIDs[1]),
	}

	type updateResult struct {
		profileID string
		err       error
	}
	start := make(chan struct{})
	results := make(chan updateResult, len(profileIDs))
	var ready sync.WaitGroup
	ready.Add(len(profileIDs))
	for _, profileID := range profileIDs {
		go func(profileID string) {
			ready.Done()
			<-start
			disabled := false
			_, err := service.Update(fixture.ctx, globalProfileAdministrator(fixture), profileID, UpdateInput{Enabled: &disabled})
			results <- updateResult{profileID: profileID, err: err}
		}(profileID)
	}
	ready.Wait()
	close(start)

	succeeded := 0
	refused := 0
	for range profileIDs {
		result := <-results
		stored := fixture.profileState(t, result.profileID)
		switch {
		case result.err == nil:
			succeeded++
			if stored.enabled {
				t.Fatalf("successful concurrent update left profile %s enabled", result.profileID)
			}
			if stored.version == original[result.profileID].version {
				t.Fatalf("successful concurrent update did not replace profile row version %s", result.profileID)
			}
		case errors.Is(result.err, ErrLastUnrestrictedProfile):
			refused++
			assertStoredProfileStateEqual(t, stored, original[result.profileID])
		default:
			t.Fatalf("concurrent profile update %s failed unexpectedly: %v", result.profileID, result.err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("concurrent update results: succeeded=%d refused=%d, want one of each", succeeded, refused)
	}

	var unrestrictedCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM profiles
		WHERE category_id = $1::uuid
		  AND enabled
		  AND available_from IS NULL AND available_until IS NULL
		  AND access_start_time IS NULL AND access_end_time IS NULL
	`, fixture.categoryIDs[0]).Scan(&unrestrictedCount); err != nil {
		t.Fatalf("count unrestricted profiles after concurrent updates: %v", err)
	}
	if unrestrictedCount != 1 {
		t.Fatalf("unrestricted profile count after concurrent updates = %d, want 1", unrestrictedCount)
	}
}

func TestUpdateRejectsCategoryMoveThatWouldOverflowEffectiveCollections(t *testing.T) {
	fixture := newProfileInvariantFixture(t)
	service := NewService(fixture.pool, time.Hour, "UTC")
	targetID := fixture.insertProfile(t, fixture.categoryIDs[0], "Move target")
	fixture.insertProfile(t, fixture.categoryIDs[0], "Source peer")
	ownerID := fixture.insertProfile(t, fixture.categoryIDs[1], "Destination owner")
	if _, err := fixture.pool.Exec(fixture.ctx, `
		WITH inserted AS (
			INSERT INTO profile_collections (profile_id, title, position)
			SELECT $1::uuid, 'Profile move category collection ' || value, value
			FROM generate_series(0, 99) value
			RETURNING id, position
		)
		INSERT INTO collection_category_access (collection_id, category_id, position)
		SELECT id, $2::uuid, position FROM inserted
	`, ownerID, fixture.categoryIDs[1]); err != nil {
		t.Fatalf("insert destination category collections: %v", err)
	}
	var explicitCollectionID string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO profile_collections (profile_id, title, position)
		VALUES ($1::uuid, 'Profile move explicit collection', 100)
		RETURNING id::text
	`, ownerID).Scan(&explicitCollectionID); err != nil {
		t.Fatalf("insert explicit collection: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO collection_profile_access (collection_id, profile_id, position)
		VALUES ($1::uuid, $2::uuid, 0)
	`, explicitCollectionID, targetID); err != nil {
		t.Fatalf("grant explicit collection: %v", err)
	}
	original := fixture.profileState(t, targetID)
	destinationCategoryID := fixture.categoryIDs[1]
	if _, err := service.Update(fixture.ctx, globalProfileAdministrator(fixture), targetID, UpdateInput{CategoryID: &destinationCategoryID}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("overflowing direct category move error = %v, want %v", err, ErrInvalidInput)
	}
	assertStoredProfileStateEqual(t, fixture.profileState(t, targetID), original)
}

func TestDeleteRetainsAssignedResourcesAndRemovesOrphans(t *testing.T) {
	fixture := newProfileInvariantFixture(t)
	service := NewService(fixture.pool, time.Hour, "UTC")
	targetID := fixture.insertProfile(t, fixture.categoryIDs[0], "Delete resource owner")
	peerID := fixture.insertProfile(t, fixture.categoryIDs[0], "Delete resource peer")
	var addonIDs, collectionIDs []string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		WITH inserted AS (
			INSERT INTO profile_addons (
				profile_id, transport_url, manifest, manifest_id, manifest_version, position
			)
			SELECT $1::uuid, 'https://profile-delete.example/' || label, '{}'::jsonb, label, '1.0.0', position
			FROM (VALUES ('category', 0), ('explicit', 1), ('orphan', 2)) resources(label, position)
			RETURNING id, manifest_id
		)
		SELECT array_agg(id::text ORDER BY manifest_id) FROM inserted
	`, targetID).Scan(&addonIDs); err != nil {
		t.Fatalf("seed profile deletion add-ons: %v", err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `
		WITH inserted AS (
			INSERT INTO profile_collections (profile_id, title, position)
			SELECT $1::uuid, label, position
			FROM (VALUES ('category', 0), ('explicit', 1), ('orphan', 2)) resources(label, position)
			RETURNING id, title
		)
		SELECT array_agg(id::text ORDER BY title) FROM inserted
	`, targetID).Scan(&collectionIDs); err != nil {
		t.Fatalf("seed profile deletion collections: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), `DELETE FROM profile_addons WHERE id = ANY($1::uuid[])`, addonIDs)
		_, _ = fixture.pool.Exec(context.Background(), `DELETE FROM profile_collections WHERE id = ANY($1::uuid[])`, collectionIDs)
	})
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO addon_category_access (addon_id, category_id, position)
		VALUES ($1::uuid, $2::uuid, 0)
	`, addonIDs[0], fixture.categoryIDs[0]); err != nil {
		t.Fatalf("grant profile deletion category add-on: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO addon_profile_access (addon_id, profile_id, position)
		VALUES ($1::uuid, $3::uuid, 0), ($2::uuid, $4::uuid, 0)
	`, addonIDs[1], addonIDs[2], peerID, targetID); err != nil {
		t.Fatalf("grant profile deletion explicit add-ons: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO collection_category_access (collection_id, category_id, position)
		VALUES ($1::uuid, $2::uuid, 0)
	`, collectionIDs[0], fixture.categoryIDs[0]); err != nil {
		t.Fatalf("grant profile deletion category collection: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO collection_profile_access (collection_id, profile_id, position)
		VALUES ($1::uuid, $3::uuid, 0), ($2::uuid, $4::uuid, 0)
	`, collectionIDs[1], collectionIDs[2], peerID, targetID); err != nil {
		t.Fatalf("grant profile deletion explicit collections: %v", err)
	}

	if err := service.Delete(fixture.ctx, globalProfileAdministrator(fixture), targetID); err != nil {
		t.Fatalf("delete resource owner profile: %v", err)
	}
	for _, resource := range []struct {
		label string
		table string
		ids   []string
	}{
		{label: "add-on", table: "profile_addons", ids: addonIDs},
		{label: "collection", table: "profile_collections", ids: collectionIDs},
	} {
		var retainedOwners, orphanCount int
		query := "SELECT count(*) FROM " + resource.table + " WHERE id = ANY($1::uuid[]) AND profile_id IS NULL"
		if err := fixture.pool.QueryRow(fixture.ctx, query, resource.ids[:2]).Scan(&retainedOwners); err != nil {
			t.Fatalf("count retained %s resources: %v", resource.label, err)
		}
		if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM "+resource.table+" WHERE id = $1::uuid", resource.ids[2]).Scan(&orphanCount); err != nil {
			t.Fatalf("count orphaned %s resource: %v", resource.label, err)
		}
		if retainedOwners != 2 || orphanCount != 0 {
			t.Errorf("%s deletion state = retained null owners %d, orphan rows %d; want 2 and 0", resource.label, retainedOwners, orphanCount)
		}
	}
	var survivingAssignments int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(*) FROM addon_category_access WHERE addon_id = $1::uuid) +
			(SELECT count(*) FROM addon_profile_access WHERE addon_id = $2::uuid AND profile_id = $5::uuid) +
			(SELECT count(*) FROM collection_category_access WHERE collection_id = $3::uuid) +
			(SELECT count(*) FROM collection_profile_access WHERE collection_id = $4::uuid AND profile_id = $5::uuid)
	`, addonIDs[0], addonIDs[1], collectionIDs[0], collectionIDs[1], peerID).Scan(&survivingAssignments); err != nil {
		t.Fatalf("count surviving resource assignments: %v", err)
	}
	if survivingAssignments != 4 {
		t.Errorf("surviving resource assignment count = %d, want 4", survivingAssignments)
	}
}

func TestCreateRejectsPreexistingInvalidCategoryResourceInheritance(t *testing.T) {
	fixture := newProfileInvariantFixture(t)
	ownerProfileID := fixture.insertProfile(t, fixture.categoryIDs[1], "Resource owner")
	var administratorID string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'unused-test-hash', 'admin')
		RETURNING id::text
	`, "profile-create-integrity-"+fixture.nameSuffix).Scan(&administratorID); err != nil {
		t.Fatalf("create profile integrity administrator: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, administratorID)
	})
	if _, err := fixture.pool.Exec(fixture.ctx, `
		WITH inserted AS (
			INSERT INTO profile_collections (profile_id, title, folders, position)
			SELECT $1::uuid, 'Invalid inherited collection ' || generated.value, '[]'::jsonb, generated.value - 1
			FROM generate_series(1, 101) AS generated(value)
			RETURNING id, position
		)
		INSERT INTO collection_category_access (collection_id, category_id, position)
		SELECT inserted.id, $2::uuid, row_number() OVER (ORDER BY inserted.position) - 1
		FROM inserted
	`, ownerProfileID, fixture.categoryIDs[0]); err != nil {
		t.Fatalf("seed invalid inherited collection policy: %v", err)
	}
	principal := auth.Principal{
		UserID: administratorID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}
	name := "Rejected inherited resources " + fixture.nameSuffix
	if _, err := NewService(fixture.pool, time.Hour, "UTC").Create(fixture.ctx, principal, CreateInput{
		Name: name, CategoryID: fixture.categoryIDs[0],
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("profile creation with invalid inherited resources error = %v, want %v", err, ErrInvalidInput)
	}
	var persisted int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM profiles WHERE name = $1`, name).Scan(&persisted); err != nil {
		t.Fatalf("count rejected profile creation: %v", err)
	}
	if persisted != 0 {
		t.Fatalf("rejected profile creation persisted %d profile rows", persisted)
	}
}

func TestStaleGlobalPrincipalCannotMoveOrCreateProfiles(t *testing.T) {
	fixture := newProfileInvariantFixture(t)
	targetID := fixture.insertProfile(t, fixture.categoryIDs[0], "Stale global target")
	fixture.insertProfile(t, fixture.categoryIDs[0], "Stale global peer")
	fixture.insertProfile(t, fixture.categoryIDs[1], "Stale global destination peer")
	principal := globalProfileAdministrator(fixture)
	before := fixture.profileState(t, targetID)
	var auditBefore int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM access_category_audit_events
		WHERE entity_type = 'profile' AND entity_id = $1::uuid
	`, targetID).Scan(&auditBefore); err != nil {
		t.Fatalf("count profile audit before stale-global mutations: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, fixture.administratorID); err != nil {
		t.Fatalf("demote profile invariant administrator: %v", err)
	}
	destinationCategoryID := fixture.categoryIDs[1]
	if _, err := NewService(fixture.pool, time.Hour, "UTC").Update(fixture.ctx, principal, targetID, UpdateInput{
		CategoryID: &destinationCategoryID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale-global profile move error = %v, want %v", err, ErrForbidden)
	}
	assertStoredProfileStateEqual(t, fixture.profileState(t, targetID), before)
	var auditAfter int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM access_category_audit_events
		WHERE entity_type = 'profile' AND entity_id = $1::uuid
	`, targetID).Scan(&auditAfter); err != nil {
		t.Fatalf("count profile audit after stale-global move: %v", err)
	}
	if auditAfter != auditBefore {
		t.Fatalf("stale-global move wrote audit events: before=%d after=%d", auditBefore, auditAfter)
	}
	name := "Stale global create " + fixture.nameSuffix
	if _, err := NewService(fixture.pool, time.Hour, "UTC").Create(fixture.ctx, principal, CreateInput{
		Name: name, CategoryID: destinationCategoryID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale-global profile create error = %v, want %v", err, ErrForbidden)
	}
	var created int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM profiles WHERE name = $1`, name).Scan(&created); err != nil {
		t.Fatalf("count stale-global profile creation: %v", err)
	}
	if created != 0 {
		t.Fatalf("stale-global profile creation persisted %d rows", created)
	}
}
