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

func TestSharedCollectionManagementRequiresEveryAssignedProfile(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL collection authorization test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	const (
		categoryID      = "11111111-1111-4111-8111-111111111111"
		userID          = "22222222-2222-4222-8222-222222222222"
		profileAID      = "33333333-3333-4333-8333-333333333333"
		profileBID      = "44444444-4444-4444-8444-444444444444"
		collectionID    = "55555555-5555-4555-8555-555555555555"
		memberUserID    = "66666666-6666-4666-8666-666666666666"
		deviceID        = "77777777-7777-4777-8777-777777777777"
		memberDeviceID  = "88888888-8888-4888-8888-888888888888"
		sessionID       = "99999999-9999-4999-8999-999999999999"
		memberSessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		backdropSource  = "https://art.example/private/collection/backdrop.jpg?token=collection-secret&size=original"
		coverSource     = "https://art.example/private/folder/cover.jpg?token=cover-secret&size=original"
		logoSource      = "https://art.example/private/folder/logo.png?token=logo-secret&size=original"
		heroSource      = "https://art.example/private/folder/hero.jpg?token=hero-secret&size=original"
	)
	foldersJSON := `[{"title":"Featured","tileShape":"poster","sourceView":"merged","coverImageUrl":"` + coverSource + `","titleLogoUrl":"` + logoSource + `","heroBackdropUrl":"` + heroSource + `","focusGifEnabled":false,"hideTitle":false,"sources":[]}]`
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE users (id uuid PRIMARY KEY);
		CREATE TEMPORARY TABLE devices (id uuid PRIMARY KEY, user_id uuid NOT NULL);
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY,
			category_id uuid,
			name text
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		CREATE TEMPORARY TABLE auth_sessions (
			id uuid PRIMARY KEY, user_id uuid NOT NULL, device_id uuid NOT NULL,
			access_expires_at timestamptz NOT NULL, active_profile_id uuid,
			profile_grant_expires_at timestamptz, profile_context_hash bytea, revoked_at timestamptz
		);
		CREATE TEMPORARY TABLE profile_collections (
			id uuid PRIMARY KEY,
			profile_id uuid NOT NULL,
			title text NOT NULL,
			backdrop_image_url text,
			hero_enabled boolean NOT NULL DEFAULT false,
			pin_to_top boolean NOT NULL DEFAULT false,
			focus_glow_enabled boolean NOT NULL DEFAULT false,
			view_mode text NOT NULL,
			folder_cover_shape text NOT NULL,
			folders jsonb NOT NULL,
			position integer NOT NULL DEFAULT 0,
			version integer NOT NULL DEFAULT 1,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE collection_profile_access (
			collection_id uuid NOT NULL REFERENCES profile_collections(id) ON DELETE CASCADE,
			profile_id uuid NOT NULL,
			position integer NOT NULL,
			PRIMARY KEY (collection_id, profile_id)
		);
		CREATE TEMPORARY TABLE collection_category_access (
			collection_id uuid NOT NULL, category_id uuid NOT NULL, position integer NOT NULL,
			PRIMARY KEY (collection_id, category_id)
		);
		CREATE TEMPORARY TABLE collection_profile_order (
			collection_id uuid NOT NULL, profile_id uuid NOT NULL, position integer NOT NULL,
			PRIMARY KEY (collection_id, profile_id)
		);
	`); err != nil {
		t.Fatalf("create shared collection authorization fixtures: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id) VALUES ($4::uuid), ($6::uuid);
		INSERT INTO devices (id, user_id) VALUES
			($10::uuid, $4::uuid), ($12::uuid, $6::uuid);

		WITH inserted_profiles AS (
			INSERT INTO profiles (id, category_id, name) VALUES
				($1::uuid, $3::uuid, 'Profile A'), ($2::uuid, $3::uuid, 'Profile B')
			RETURNING id
		), inserted_access AS (
			INSERT INTO user_profile_access (user_id, profile_id, can_manage) VALUES
				($4::uuid, $1::uuid, true), ($4::uuid, $2::uuid, false),
				($6::uuid, $1::uuid, false), ($6::uuid, $2::uuid, false)
			RETURNING profile_id
		), inserted_collection AS (
			INSERT INTO profile_collections (
				id, profile_id, title, backdrop_image_url, view_mode, folder_cover_shape, folders
			) VALUES ($5::uuid, $1::uuid, 'Shared', $7, 'tabbed_grid', 'poster', $8::jsonb)
			RETURNING id
		)
		INSERT INTO collection_profile_access (collection_id, profile_id, position)
		SELECT inserted_collection.id, target.profile_id, 0
		FROM inserted_collection
		CROSS JOIN (VALUES ($1::uuid), ($2::uuid)) target(profile_id)
		;
		INSERT INTO auth_sessions (
			id, user_id, device_id, access_expires_at, active_profile_id,
			profile_grant_expires_at, profile_context_hash
		) VALUES
			($9::uuid, $4::uuid, $10::uuid, now() + interval '1 hour', $1::uuid, now() + interval '1 hour', decode(repeat('c1', 32), 'hex')),
			($11::uuid, $6::uuid, $12::uuid, now() + interval '1 hour', $1::uuid, now() + interval '1 hour', decode(repeat('c2', 32), 'hex'))
	`, pgx.QueryExecModeSimpleProtocol, profileAID, profileBID, categoryID, userID, collectionID, memberUserID, backdropSource, foldersJSON,
		sessionID, deviceID, memberSessionID, memberDeviceID); err != nil {
		t.Fatalf("seed shared collection authorization boundary: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	activeProfileID := profileAID
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: stringPointer(categoryID),
		ActiveProfileID: &activeProfileID, ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: bytes.Repeat([]byte{0xc1}, sha256.Size),
	}
	input := SaveInput{
		Title: "Changed", ExpectedVersion: 1,
		Folders: []Folder{{
			Title: "Featured",
			Sources: []Source{{
				Kind: SourceKindTMDB, Title: "Popular",
				TMDB: &TMDBSource{SourceType: "discover", MediaType: MediaTypeMovie, Sort: "popularity.desc"},
			}},
		}},
	}
	service := NewService(pool, nil, nil, nil, nil)
	memberPrincipal := principal
	memberPrincipal.UserID = memberUserID
	memberPrincipal.SessionID = memberSessionID
	memberPrincipal.DeviceID = memberDeviceID
	memberPrincipal.ProfileContextHash = bytes.Repeat([]byte{0xc2}, sha256.Size)
	if _, err := service.Management(ctx, memberPrincipal, collectionID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member collection management read: got %v, want ErrForbidden", err)
	}
	if _, err := service.Export(ctx, memberPrincipal); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member collection export: got %v, want ErrForbidden", err)
	}
	if _, err := service.Management(ctx, principal, collectionID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("partial category-manager collection read: got %v, want ErrForbidden", err)
	}
	if _, err := service.Export(ctx, principal); !errors.Is(err, ErrForbidden) {
		t.Fatalf("partial category-manager collection export: got %v, want ErrForbidden", err)
	}
	if _, err := service.Update(ctx, principal, collectionID, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("mixed-authority shared update: got %v, want ErrForbidden", err)
	}
	if err := service.Delete(ctx, principal, collectionID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("mixed-authority shared delete: got %v, want ErrForbidden", err)
	}
	var title string
	var version, assignmentCount int
	if err := pool.QueryRow(ctx, `
		SELECT title, version,
		       (SELECT count(*) FROM collection_profile_access WHERE collection_id = $1::uuid)
		FROM profile_collections
		WHERE id = $1::uuid
	`, collectionID).Scan(&title, &version, &assignmentCount); err != nil {
		t.Fatalf("query collection after denied mutations: %v", err)
	}
	if title != "Shared" || version != 1 || assignmentCount != 2 {
		t.Fatalf("denied shared mutations were not atomic: title=%q version=%d assignments=%d", title, version, assignmentCount)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access
		SET can_manage = true
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileBID); err != nil {
		t.Fatalf("grant second profile management: %v", err)
	}
	managed, err := service.Management(ctx, principal, collectionID)
	if err != nil {
		t.Fatalf("fully authorized collection management read: %v", err)
	}
	if managed.BackdropImageURL != backdropSource || len(managed.Folders) != 1 ||
		managed.Folders[0].CoverImageURL != coverSource || managed.Folders[0].TitleLogoURL != logoSource ||
		managed.Folders[0].HeroBackdropURL != heroSource {
		t.Fatalf("management read did not preserve exact stored artwork sources: %+v", managed)
	}
	updated, err := service.Update(ctx, principal, collectionID, input)
	if err != nil {
		t.Fatalf("fully authorized shared update: %v", err)
	}
	if updated.Title != "Changed" || updated.Version != 2 {
		t.Fatalf("unexpected authorized shared update: %+v", updated)
	}
	if err := service.Delete(ctx, principal, collectionID); err != nil {
		t.Fatalf("fully authorized shared delete: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_collections WHERE id = $1::uuid`, collectionID).Scan(&remaining); err != nil {
		t.Fatalf("count deleted shared collection: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("authorized shared delete left %d collection rows", remaining)
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestReorderRequiresManagementAndSerializesGrantRevocation(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run collection reorder authorization tests")
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
		userID              = "6d000000-0000-4000-8000-000000000001"
		categoryID          = "6d000000-0000-4000-8000-000000000002"
		profileID           = "6d000000-0000-4000-8000-000000000003"
		collectionAID       = "6d000000-0000-4000-8000-000000000004"
		collectionBID       = "6d000000-0000-4000-8000-000000000005"
		foreignProfileID    = "6d000000-0000-4000-8000-000000000006"
		foreignCollectionID = "6d000000-0000-4000-8000-000000000007"
		deviceID            = "6d000000-0000-4000-8000-000000000008"
		sessionID           = "6d000000-0000-4000-8000-000000000009"
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cleanup := func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth_sessions WHERE id = $1::uuid`, sessionID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profile_collections WHERE id = ANY($1::uuid[])`, []string{collectionAID, collectionBID, foreignCollectionID})
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{profileID, foreignProfileID})
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_categories WHERE id = $1::uuid`, categoryID)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := pool.Exec(ctx, `
		WITH inserted_category AS (
			INSERT INTO access_categories (id, name, normalized_name, position)
			VALUES ($1::uuid, 'Collection reorder authorization', 'collection reorder authorization', 920002)
			RETURNING id
		), inserted_user AS (
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($2::uuid, 'collection-reorder-authorization-user', 'unused-test-hash', 'member')
			RETURNING id
		), inserted_profiles AS (
			INSERT INTO profiles (id, category_id, name)
			VALUES
				($3::uuid, $1::uuid, 'Collection reorder profile'),
				($6::uuid, $1::uuid, 'Foreign collection reorder profile')
			RETURNING id
		), inserted_access AS (
			INSERT INTO user_profile_access (user_id, profile_id, can_manage)
			VALUES
				($2::uuid, $3::uuid, false),
				($2::uuid, $6::uuid, false)
			RETURNING profile_id
		), inserted_device AS (
			INSERT INTO devices (id, user_id, name, platform, category_id, approved_at)
			VALUES ($8::uuid, $2::uuid, 'Collection reorder device', 'test', $1::uuid, now())
			RETURNING id
		), inserted_session AS (
			INSERT INTO auth_sessions (
				id, user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
				authorization_scope, category_id, active_profile_id, profile_grant_expires_at, profile_context_hash
			) VALUES (
				$9::uuid, $2::uuid, $8::uuid, decode(repeat('d1', 32), 'hex'), now() + interval '1 hour',
				now() + interval '2 hours', 'category', $1::uuid, $3::uuid, now() + interval '1 hour', decode(repeat('d2', 32), 'hex')
			)
			RETURNING id
		), inserted_collections AS (
			INSERT INTO profile_collections (id, profile_id, title, folders, position)
			VALUES
				($4::uuid, $3::uuid, 'Reorder A', '[]'::jsonb, 0),
				($5::uuid, $3::uuid, 'Reorder B', '[]'::jsonb, 1),
				($7::uuid, $6::uuid, 'Foreign reorder', '[]'::jsonb, 0)
			RETURNING id
		), inserted_collection_access AS (
			INSERT INTO collection_profile_access (collection_id, profile_id, position)
			VALUES
				($4::uuid, $3::uuid, 0),
				($5::uuid, $3::uuid, 1),
				($7::uuid, $6::uuid, 0)
			RETURNING collection_id
		)
		INSERT INTO collection_profile_order (collection_id, profile_id, position)
		VALUES
			($4::uuid, $3::uuid, 0),
			($5::uuid, $3::uuid, 1),
			($7::uuid, $6::uuid, 0)
	`, categoryID, userID, profileID, collectionAID, collectionBID, foreignProfileID, foreignCollectionID, deviceID, sessionID); err != nil {
		t.Fatalf("seed collection reorder authorization boundary: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	category := categoryID
	activeProfile := profileID
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID, Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &category,
		ActiveProfileID: &activeProfile, ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: bytes.Repeat([]byte{0xd2}, sha256.Size),
	}
	service := NewService(pool, nil, nil, nil, nil)
	reversed := ReorderInput{CollectionIDs: []string{collectionBID, collectionAID}}
	if _, err := service.Reorder(ctx, principal, reversed); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer collection reorder error = %v, want %v", err, ErrForbidden)
	}
	assertPositions := func(wantA, wantB int) {
		t.Helper()
		var positionA, positionB int
		if err := pool.QueryRow(ctx, `
			SELECT
				max(position) FILTER (WHERE collection_id = $2::uuid),
				max(position) FILTER (WHERE collection_id = $3::uuid)
			FROM collection_profile_order
			WHERE profile_id = $1::uuid
		`, profileID, collectionAID, collectionBID).Scan(&positionA, &positionB); err != nil {
			t.Fatalf("query collection reorder positions: %v", err)
		}
		if positionA != wantA || positionB != wantB {
			t.Fatalf("collection positions = A:%d B:%d, want A:%d B:%d", positionA, positionB, wantA, wantB)
		}
	}
	assertPositions(0, 1)

	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access
		SET can_manage = true
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileID); err != nil {
		t.Fatalf("grant collection reorder management: %v", err)
	}
	if _, err := service.Reorder(ctx, principal, ReorderInput{CollectionIDs: []string{collectionBID, foreignCollectionID}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("mixed collection reorder error = %v, want %v", err, ErrInvalidInput)
	}
	assertPositions(0, 1)
	reordered, err := service.Reorder(ctx, principal, reversed)
	if err != nil {
		t.Fatalf("manager collection reorder: %v", err)
	}
	if len(reordered) != 2 || reordered[0].ID != collectionBID || reordered[1].ID != collectionAID {
		t.Fatalf("manager collection reorder result = %+v", reordered)
	}
	assertPositions(1, 0)
	var explicitPositionA, explicitPositionB int
	if err := pool.QueryRow(ctx, `
		SELECT
			max(position) FILTER (WHERE collection_id = $2::uuid),
			max(position) FILTER (WHERE collection_id = $3::uuid)
		FROM collection_profile_access
		WHERE profile_id = $1::uuid
	`, profileID, collectionAID, collectionBID).Scan(&explicitPositionA, &explicitPositionB); err != nil {
		t.Fatalf("query explicit collection positions: %v", err)
	}
	if explicitPositionA != 0 || explicitPositionB != 1 {
		t.Fatalf("reorder changed access positions: A:%d B:%d", explicitPositionA, explicitPositionB)
	}

	if _, err := pool.Exec(ctx, `
		WITH changed_grant AS (
			UPDATE user_profile_access SET can_manage = false
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
			RETURNING profile_id
		)
		UPDATE collection_profile_order SET position = CASE collection_id
			WHEN $3::uuid THEN 0
			WHEN $4::uuid THEN 1
		END
		WHERE profile_id = (SELECT profile_id FROM changed_grant)
	`, userID, profileID, collectionAID, collectionBID); err != nil {
		t.Fatalf("prepare global collection reorder: %v", err)
	}
	globalPrincipal := principal
	globalPrincipal.Role = "admin"
	globalPrincipal.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	if _, err := service.Reorder(ctx, globalPrincipal, reversed); err != nil {
		t.Fatalf("global administrator collection reorder: %v", err)
	}
	assertPositions(1, 0)

	if _, err := pool.Exec(ctx, `
		WITH changed_grant AS (
			UPDATE user_profile_access SET can_manage = true
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
			RETURNING profile_id
		)
		UPDATE collection_profile_order SET position = CASE collection_id
			WHEN $3::uuid THEN 0
			WHEN $4::uuid THEN 1
		END
		WHERE profile_id = (SELECT profile_id FROM changed_grant)
	`, userID, profileID, collectionAID, collectionBID); err != nil {
		t.Fatalf("prepare concurrent collection reorder: %v", err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin collection reorder blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `
		SELECT id
		FROM profile_collections
		WHERE id = ANY($1::uuid[])
		ORDER BY id
		FOR UPDATE
	`, []string{collectionAID, collectionBID}); err != nil {
		t.Fatalf("lock collections: %v", err)
	}
	reorderDone := make(chan error, 1)
	go func() {
		_, reorderErr := service.Reorder(ctx, principal, reversed)
		reorderDone <- reorderErr
	}()
	waitForBlockedQuery := func(fragment string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var blocked bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM pg_stat_activity
					WHERE pid <> pg_backend_pid()
					  AND wait_event_type = 'Lock'
					  AND query LIKE '%' || $1 || '%'
				)
			`, fragment).Scan(&blocked); err != nil {
				t.Fatalf("inspect blocked collection reorder query: %v", err)
			}
			if blocked {
				return
			}
			select {
			case reorderErr := <-reorderDone:
				t.Fatalf("collection reorder returned before assignment lock release: %v", reorderErr)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("query containing %q did not block", fragment)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForBlockedQuery("collection.lock_effective_collections_for_reorder")
	revokeDone := make(chan error, 1)
	go func() {
		_, revokeErr := pool.Exec(ctx, `
			UPDATE user_profile_access SET can_manage = false
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
		`, userID, profileID)
		revokeDone <- revokeErr
	}()
	waitForBlockedQuery("UPDATE user_profile_access SET can_manage = false")
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release collection assignment lock: %v", err)
	}
	if err := <-reorderDone; err != nil {
		t.Fatalf("collection reorder lost to concurrent revocation: %v", err)
	}
	if err := <-revokeDone; err != nil {
		t.Fatalf("concurrent collection management revocation: %v", err)
	}
	assertPositions(1, 0)
	var canManage bool
	if err := pool.QueryRow(ctx, `
		SELECT can_manage FROM user_profile_access
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileID).Scan(&canManage); err != nil {
		t.Fatalf("query revoked collection management grant: %v", err)
	}
	if canManage {
		t.Fatal("concurrent collection management revocation did not commit")
	}
}
