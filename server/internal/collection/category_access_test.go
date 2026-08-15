package collection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestCollectionCategoryAccessTracksProfilesAndKeepsExplicitAccessAdditive(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run collection category access tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open collection category test database: %v", err)
	}
	t.Cleanup(pool.Close)

	const (
		categoryAID     = "7c000000-0000-4000-8000-000000000001"
		categoryBID     = "7c000000-0000-4000-8000-000000000002"
		categoryCID     = "7c000000-0000-4000-8000-000000000009"
		addonID         = "7c000000-0000-4000-8000-000000000008"
		adminUserID     = "7c000000-0000-4000-8000-000000000003"
		memberUserID    = "7c000000-0000-4000-8000-000000000004"
		profileAID      = "7c000000-0000-4000-8000-000000000005"
		profileBID      = "7c000000-0000-4000-8000-000000000006"
		profileFutureID = "7c000000-0000-4000-8000-000000000007"
		adminDeviceID   = "7c000000-0000-4000-8000-000000000010"
		memberDeviceID  = "7c000000-0000-4000-8000-000000000011"
		profileASession = "7c000000-0000-4000-8000-000000000012"
		profileBSession = "7c000000-0000-4000-8000-000000000013"
		futureSession   = "7c000000-0000-4000-8000-000000000014"
		memberSession   = "7c000000-0000-4000-8000-000000000015"
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cleanup := func() {
		cleanupCtx := context.Background()
		profileIDs := []string{profileAID, profileBID, profileFutureID}
		categoryIDs := []string{categoryAID, categoryBID, categoryCID}
		sessionIDs := []string{profileASession, profileBSession, futureSession, memberSession}
		deviceIDs := []string{adminDeviceID, memberDeviceID}
		_, _ = pool.Exec(cleanupCtx, `
			DELETE FROM profile_addons
			WHERE id = $1::uuid OR profile_id = ANY($2::uuid[])
		`, addonID, profileIDs)
		_, _ = pool.Exec(cleanupCtx, `
			DELETE FROM profile_collections pc
			WHERE pc.profile_id = ANY($1::uuid[])
			   OR EXISTS (SELECT 1 FROM collection_profile_access access WHERE access.collection_id = pc.id AND access.profile_id = ANY($1::uuid[]))
			   OR EXISTS (SELECT 1 FROM collection_category_access access WHERE access.collection_id = pc.id AND access.category_id = ANY($2::uuid[]))
			   OR pc.title IN ('Category first', 'Category overlap', 'Explicit only', 'Category second',
			                   'Category source allowed', 'Explicit source through category addon', 'Empty category exact limit')
			   OR pc.title LIKE 'Category limit %'
			   OR pc.title LIKE 'Empty category limit %'
		`, profileIDs, categoryIDs)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth_sessions WHERE id = ANY($1::uuid[])`, sessionIDs)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM devices WHERE id = ANY($1::uuid[])`, deviceIDs)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, profileIDs)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{adminUserID, memberUserID})
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, categoryIDs)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_categories (id, name, normalized_name, position) VALUES
			($1::uuid, 'Collection category access A', 'collection category access a', 970001),
			($2::uuid, 'Collection category access B', 'collection category access b', 970002);
		INSERT INTO users (id, username, password_hash, role) VALUES
			($3::uuid, 'collection-category-admin', 'unused-test-hash', 'admin'),
			($4::uuid, 'collection-category-member', 'unused-test-hash', 'member');
		INSERT INTO profiles (id, category_id, name) VALUES
			($5::uuid, $1::uuid, 'Category profile A'),
			($6::uuid, $1::uuid, 'Category profile B');
		INSERT INTO user_profile_access (user_id, profile_id, can_manage) VALUES
			($4::uuid, $5::uuid, true),
			($4::uuid, $6::uuid, true);
		INSERT INTO devices (id, user_id, name, platform, category_id, approved_at) VALUES
			($7::uuid, $3::uuid, 'Collection category admin device', 'test', $1::uuid, now()),
			($8::uuid, $4::uuid, 'Collection category member device', 'test', $1::uuid, now());
		INSERT INTO auth_sessions (
			id, user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at, profile_context_hash
		) VALUES
			($9::uuid, $3::uuid, $7::uuid, decode(repeat('a1', 32), 'hex'), now() + interval '1 hour', now() + interval '2 hours', 'global_admin', NULL, $5::uuid, now() + interval '1 hour', decode(repeat('b1', 32), 'hex')),
			($10::uuid, $3::uuid, $7::uuid, decode(repeat('a2', 32), 'hex'), now() + interval '1 hour', now() + interval '2 hours', 'global_admin', NULL, $6::uuid, now() + interval '1 hour', decode(repeat('b2', 32), 'hex')),
			($11::uuid, $4::uuid, $8::uuid, decode(repeat('a3', 32), 'hex'), now() + interval '1 hour', now() + interval '2 hours', 'category', $1::uuid, $5::uuid, now() + interval '1 hour', decode(repeat('b3', 32), 'hex'));
	`, pgx.QueryExecModeSimpleProtocol, categoryAID, categoryBID, adminUserID, memberUserID, profileAID, profileBID,
		adminDeviceID, memberDeviceID, profileASession, profileBSession, memberSession); err != nil {
		t.Fatalf("seed collection category access fixtures: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_categories (id, name, normalized_name, position)
		VALUES ($1::uuid, 'Collection category access empty', 'collection category access empty', 970003)
	`, categoryCID); err != nil {
		t.Fatalf("seed empty collection category: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	principalFor := func(profileID string) auth.Principal {
		sessionID := profileASession
		hashByte := byte(0xb1)
		if profileID == profileBID {
			sessionID = profileBSession
			hashByte = 0xb2
		} else if profileID == profileFutureID {
			sessionID = futureSession
			hashByte = 0xb4
		}
		return auth.Principal{
			SessionID: sessionID, UserID: adminUserID, DeviceID: adminDeviceID,
			Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
			ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
			ProfileContextHash: bytes.Repeat([]byte{hashByte}, sha256.Size),
		}
	}
	input := func(title string) SaveInput {
		return SaveInput{
			Title: title,
			Folders: []Folder{{Title: "Featured", Sources: []Source{{
				Kind: SourceKindTMDB, Title: "Popular",
				TMDB: &TMDBSource{SourceType: "discover", MediaType: MediaTypeMovie, Sort: "popularity.desc"},
			}}}},
		}
	}
	service := NewService(pool, nil, nil, nil, nil)
	firstInput := input("Category first")
	firstInput.ProfileIDs = []string{}
	firstInput.CategoryIDs = []string{categoryAID}
	first, err := service.Create(ctx, principalFor(profileAID), firstInput)
	if err != nil {
		t.Fatalf("create category collection: %v", err)
	}
	if len(first.ProfileIDs) != 0 || len(first.CategoryIDs) != 1 || first.CategoryIDs[0] != categoryAID {
		t.Fatalf("category assignment was materialized or lost: %+v", first)
	}
	visible, err := service.List(ctx, principalFor(profileBID))
	if err != nil || len(visible) != 1 || visible[0].ID != first.ID {
		t.Fatalf("current category profile visibility = %+v, err %v", visible, err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO profiles (id, category_id, name) VALUES ($1::uuid, $2::uuid, 'Future category profile')`, profileFutureID, categoryAID); err != nil {
		t.Fatalf("insert future category profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO auth_sessions (
			id, user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id, active_profile_id, profile_grant_expires_at, profile_context_hash
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, decode(repeat('a4', 32), 'hex'), now() + interval '1 hour',
			now() + interval '2 hours', 'global_admin', NULL, $4::uuid, now() + interval '1 hour', decode(repeat('b4', 32), 'hex')
		)
	`, futureSession, adminUserID, adminDeviceID, profileFutureID); err != nil {
		t.Fatalf("seed future profile selection: %v", err)
	}
	visible, err = service.List(ctx, principalFor(profileFutureID))
	if err != nil || len(visible) != 1 || visible[0].ID != first.ID {
		t.Fatalf("future category profile visibility = %+v, err %v", visible, err)
	}

	overlap := input("Category overlap")
	overlap.ExpectedVersion = first.Version
	overlap.ProfileIDs = []string{profileBID}
	first, err = service.Update(ctx, principalFor(profileAID), first.ID, overlap)
	if err != nil {
		t.Fatalf("add overlapping explicit assignment: %v", err)
	}
	visible, err = service.List(ctx, principalFor(profileBID))
	if err != nil || len(visible) != 1 {
		t.Fatalf("overlap was not deduplicated: %+v, err %v", visible, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE profiles SET category_id = $2::uuid WHERE id = $1::uuid`, profileBID, categoryBID); err != nil {
		t.Fatalf("move explicit overlap profile: %v", err)
	}
	visible, err = service.List(ctx, principalFor(profileBID))
	if err != nil || len(visible) != 1 || visible[0].ID != first.ID {
		t.Fatalf("explicit profile lost access after moving out: %+v, err %v", visible, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE profiles SET category_id = $2::uuid WHERE id = $1::uuid`, profileFutureID, categoryBID); err != nil {
		t.Fatalf("move category-only profile out: %v", err)
	}
	visible, err = service.List(ctx, principalFor(profileFutureID))
	if err != nil || len(visible) != 0 {
		t.Fatalf("moved category-only profile retained access: %+v, err %v", visible, err)
	}

	secondInput := input("Category second")
	secondInput.ProfileIDs = []string{}
	secondInput.CategoryIDs = []string{categoryAID}
	second, err := service.Create(ctx, principalFor(profileAID), secondInput)
	if err != nil {
		t.Fatalf("create second category collection: %v", err)
	}
	reordered, err := service.Reorder(ctx, principalFor(profileAID), ReorderInput{CollectionIDs: []string{second.ID, first.ID}})
	if err != nil || len(reordered) != 2 || reordered[0].ID != second.ID || reordered[1].ID != first.ID {
		t.Fatalf("effective category reorder = %+v, err %v", reordered, err)
	}
	var categoryGrantCount, profileOrderCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM collection_profile_access WHERE collection_id = $1::uuid AND profile_id = $2::uuid),
			(SELECT count(*) FROM collection_profile_order WHERE profile_id = $2::uuid AND collection_id = ANY($3::uuid[]))
	`, second.ID, profileAID, []string{first.ID, second.ID}).Scan(&categoryGrantCount, &profileOrderCount); err != nil {
		t.Fatalf("query effective reorder persistence: %v", err)
	}
	if categoryGrantCount != 0 || profileOrderCount != 2 {
		t.Fatalf("reorder grants=%d orderRows=%d, want 0 and 2", categoryGrantCount, profileOrderCount)
	}

	category := categoryAID
	active := profileAID
	member := auth.Principal{
		SessionID: memberSession, UserID: memberUserID, DeviceID: memberDeviceID,
		Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &category,
		ActiveProfileID: &active, ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: bytes.Repeat([]byte{0xb3}, sha256.Size),
	}
	if _, err := service.Management(ctx, member, first.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("category policy management error = %v, want %v", err, ErrForbidden)
	}

	clearCategory := input("Explicit only")
	clearCategory.ExpectedVersion = first.Version
	clearCategory.CategoryIDs = []string{}
	first, err = service.Update(ctx, principalFor(profileAID), first.ID, clearCategory)
	if err != nil {
		t.Fatalf("clear category while preserving omitted explicit profiles: %v", err)
	}
	if len(first.ProfileIDs) != 1 || first.ProfileIDs[0] != profileBID || len(first.CategoryIDs) != 0 {
		t.Fatalf("omitted explicit assignment was not preserved: %+v", first)
	}
	emptyUnion := input("Invalid empty")
	emptyUnion.ExpectedVersion = first.Version
	emptyUnion.ProfileIDs = []string{}
	if _, err := service.Update(ctx, principalFor(profileBID), first.ID, emptyUnion); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty resulting assignment union error = %v, want %v", err, ErrInvalidInput)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profile_addons (
			id, profile_id, transport_url, manifest, manifest_id, manifest_version, position, enabled
		) VALUES (
			$1::uuid, $2::uuid, 'https://collection-category-addon.example/manifest.json',
			'{"id":"org.example.collection-category","version":"1.0.0","name":"Collection category addon","types":["movie"],"resources":["catalog"],"catalogs":[{"type":"movie","id":"popular"}]}'::jsonb,
			'org.example.collection-category', '1.0.0', 0, true
		);
		INSERT INTO addon_category_access (addon_id, category_id, position) VALUES ($1::uuid, $3::uuid, 0);
	`, pgx.QueryExecModeSimpleProtocol, addonID, profileAID, categoryAID); err != nil {
		t.Fatalf("seed category addon source policy: %v", err)
	}
	addonSourceInput := func(title string) SaveInput {
		return SaveInput{
			Title: title,
			Folders: []Folder{{Title: "Addon", Sources: []Source{{
				Kind: SourceKindAddonCatalog, Title: "Popular addon catalog",
				AddonCatalog: &AddonCatalogSource{AddonID: addonID, Type: MediaTypeMovie, CatalogID: "popular"},
			}}}},
		}
	}
	categorySource := addonSourceInput("Category source allowed")
	categorySource.ProfileIDs = []string{}
	categorySource.CategoryIDs = []string{categoryAID}
	if _, err := service.Create(ctx, principalFor(profileAID), categorySource); err != nil {
		t.Fatalf("category source with matching durable addon policy: %v", err)
	}
	wrongCategorySource := addonSourceInput("Category source denied")
	wrongCategorySource.ProfileIDs = []string{}
	wrongCategorySource.CategoryIDs = []string{categoryBID}
	if _, err := service.Create(ctx, principalFor(profileAID), wrongCategorySource); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("mismatched category source policy error = %v, want %v", err, ErrInvalidInput)
	}
	explicitSource := addonSourceInput("Explicit source through category addon")
	explicitSource.ProfileIDs = []string{profileAID}
	if _, err := service.Create(ctx, principalFor(profileAID), explicitSource); err != nil {
		t.Fatalf("explicit profile source through matching addon category: %v", err)
	}
	wrongExplicitSource := addonSourceInput("Explicit source denied")
	wrongExplicitSource.ProfileIDs = []string{profileBID}
	if _, err := service.Create(ctx, principalFor(profileAID), wrongExplicitSource); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("uncovered explicit source policy error = %v, want %v", err, ErrInvalidInput)
	}
	if _, err := pool.Exec(ctx, `UPDATE profile_addons SET enabled = false WHERE id = $1::uuid`, addonID); err != nil {
		t.Fatalf("disable category addon source: %v", err)
	}
	disabledSource := addonSourceInput("Disabled source denied")
	disabledSource.ProfileIDs = []string{profileAID}
	if _, err := service.Create(ctx, principalFor(profileAID), disabledSource); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("disabled addon source error = %v, want %v", err, ErrInvalidInput)
	}
	if _, err := pool.Exec(ctx, `
		WITH owner_base AS (
			SELECT COALESCE(max(position) + 1, 0) AS value FROM profile_collections WHERE profile_id = $1::uuid
		), inserted AS (
			INSERT INTO profile_collections (profile_id, title, folders, position)
			SELECT $1::uuid, 'Empty category limit ' || generated.value, '[]'::jsonb,
			       owner_base.value + generated.value - 1
			FROM generate_series(1, 99) AS generated(value)
			CROSS JOIN owner_base
			RETURNING id, position
		)
		INSERT INTO collection_category_access (collection_id, category_id, position)
		SELECT inserted.id, $2::uuid, row_number() OVER (ORDER BY inserted.position) - 1
		FROM inserted
	`, profileAID, categoryCID); err != nil {
		t.Fatalf("seed empty category collection policies: %v", err)
	}
	exactLimit := input("Empty category exact limit")
	exactLimit.ProfileIDs = []string{}
	exactLimit.CategoryIDs = []string{categoryCID}
	if _, err := service.Create(ctx, principalFor(profileAID), exactLimit); err != nil {
		t.Fatalf("create 100th empty-category collection: %v", err)
	}
	overEmptyCategoryLimit := input("Empty category over limit")
	overEmptyCategoryLimit.ProfileIDs = []string{}
	overEmptyCategoryLimit.CategoryIDs = []string{categoryCID}
	if _, err := service.Create(ctx, principalFor(profileAID), overEmptyCategoryLimit); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("101st empty-category collection error = %v, want %v", err, ErrInvalidInput)
	}
	current, err := service.List(ctx, principalFor(profileAID))
	if err != nil {
		t.Fatalf("list collections before category limit fixture: %v", err)
	}
	remaining := maximumCollections - len(current)
	if remaining < 0 {
		t.Fatalf("category test already exceeded collection limit: %d", len(current))
	}
	if remaining > 0 {
		if _, err := pool.Exec(ctx, `
			WITH owner_base AS (
				SELECT COALESCE(max(position) + 1, 0) AS value FROM profile_collections WHERE profile_id = $1::uuid
			), category_base AS (
				SELECT COALESCE(max(position) + 1, 0) AS value FROM collection_category_access WHERE category_id = $2::uuid
			), inserted AS (
				INSERT INTO profile_collections (profile_id, title, folders, position)
				SELECT $1::uuid, 'Category limit ' || generated.value, '[]'::jsonb, owner_base.value + generated.value - 1
				FROM generate_series(1, $3::integer) AS generated(value)
				CROSS JOIN owner_base
				RETURNING id, position
			)
			INSERT INTO collection_category_access (collection_id, category_id, position)
			SELECT inserted.id, $2::uuid,
			       category_base.value + row_number() OVER (ORDER BY inserted.position) - 1
			FROM inserted
			CROSS JOIN category_base
		`, profileAID, categoryAID, remaining); err != nil {
			t.Fatalf("seed category collection limit: %v", err)
		}
	}
	overLimit := input("Category over limit")
	overLimit.ProfileIDs = []string{}
	overLimit.CategoryIDs = []string{categoryAID}
	if _, err := service.Create(ctx, principalFor(profileAID), overLimit); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("category profile limit error = %v, want %v", err, ErrInvalidInput)
	}
}
