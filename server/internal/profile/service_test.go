package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	profilecollection "github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/password"
	"github.com/moodiness/rivune/server/internal/secretcrypto"
	"github.com/moodiness/rivune/server/internal/settings"
	"github.com/moodiness/rivune/server/internal/tracking"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

func TestHashPINUsesPasswordHasher(t *testing.T) {
	pin := "2468"
	hash, err := hashPIN(&pin)
	if err != nil {
		t.Fatalf("hash PIN: %v", err)
	}
	if hash == nil || *hash == pin {
		t.Fatal("PIN was not hashed")
	}
	matches, err := password.Verify(pin, *hash)
	if err != nil {
		t.Fatalf("verify PIN hash: %v", err)
	}
	if !matches {
		t.Fatal("valid PIN did not match its hash")
	}
}

func TestHashPINRejectsInvalidFormats(t *testing.T) {

	for _, pin := range []string{"123", "123456789", "12ab", "１２３４"} {
		t.Run(pin, func(t *testing.T) {
			if _, err := hashPIN(&pin); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected invalid PIN error for %q, got %v", pin, err)
			}
		})
	}
}
func TestNormalizeDescription(t *testing.T) {
	description := "  Profile details  "
	normalized, err := normalizeDescription(&description)
	if err != nil || normalized == nil || *normalized != "Profile details" {
		t.Fatalf("normalize description = %v, %v", normalized, err)
	}
	blank := "   "
	normalized, err = normalizeDescription(&blank)
	if err != nil || normalized != nil {
		t.Fatalf("normalize blank description = %v, %v", normalized, err)
	}
	tooLong := strings.Repeat("a", 501)
	if _, err := normalizeDescription(&tooLong); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("long description error = %v, want %v", err, ErrInvalidInput)
	}
}

func TestHashPINAllowsNoPIN(t *testing.T) {
	hash, err := hashPIN(nil)
	if err != nil || hash != nil {
		t.Fatalf("expected nil PIN to remain unset, got hash %v and error %v", hash, err)
	}
}

func TestProfileAccessibleHonorsDisabledState(t *testing.T) {
	value := Profile{Enabled: false, AccessTimezone: "UTC"}
	if profileAccessible(value, time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("disabled profile was accessible")
	}
}

func TestValidateAccessAllowsOvernightHours(t *testing.T) {
	value := Profile{Enabled: true, AccessStartTime: new("20:00"), AccessEndTime: new("08:00"), AccessTimezone: "America/Los_Angeles"}
	if err := validateAccess(value); err != nil {
		t.Fatalf("overnight hours were rejected: %v", err)
	}
}

func TestAccessScheduleDetectsDateAndHourRestrictions(t *testing.T) {
	if hasAccessSchedule(Profile{}) {
		t.Fatal("unrestricted profile was treated as scheduled")
	}
	for _, value := range []Profile{
		{AvailableFrom: new("2026-08-01")},
		{AvailableUntil: new("2026-08-31")},
		{AccessStartTime: new("08:00"), AccessEndTime: new("20:00")},
	} {
		if !hasAccessSchedule(value) {
			t.Fatalf("profile restriction was ignored: %+v", value)
		}
	}
}

func TestEnsureUnrestrictedProfilePreventsLockout(t *testing.T) {
	if err := ensureUnrestrictedProfile(1); err != nil {
		t.Fatalf("existing unrestricted profile was rejected: %v", err)
	}
	if err := ensureUnrestrictedProfile(0); !errors.Is(err, ErrLastUnrestrictedProfile) {
		t.Fatalf("expected lockout prevention error, got %v", err)
	}
}

func TestProfileCategoryInputCannotEscapePrincipalScope(t *testing.T) {
	categoryID := "11111111-1111-4111-8111-111111111111"
	otherCategoryID := "22222222-2222-4222-8222-222222222222"
	principal := auth.Principal{
		UserID: "user-id", Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
	}
	service := &Service{}

	if _, err := service.Create(context.Background(), principal, CreateInput{
		Name: "Other", CategoryID: otherCategoryID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-category profile creation error = %v, want %v", err, ErrForbidden)
	}
	if _, err := service.Update(context.Background(), principal, "33333333-3333-4333-8333-333333333333", UpdateInput{
		CategoryID: &otherCategoryID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-category profile move error = %v, want opaque %v", err, ErrNotFound)
	}
}

type categoryBoundaryAddonTransport struct{}

func (categoryBoundaryAddonTransport) Manifest(context.Context, string) (addon.Manifest, json.RawMessage, error) {
	raw := json.RawMessage(`{"id":"org.rivune.category-boundary","version":"1.0.0","name":"Boundary","types":["movie"],"resources":["stream"],"catalogs":[]}`)
	return addon.Manifest{
		ID: "org.rivune.category-boundary", Version: "1.0.0", Name: "Boundary",
		Types: []string{"movie"}, Resources: []addon.ManifestResource{{Name: "stream", Short: true}},
	}, raw, nil
}

func (categoryBoundaryAddonTransport) Resource(context.Context, string, addon.ResourcePath) (json.RawMessage, addon.CachePolicy, error) {
	return nil, addon.CachePolicy{}, errors.New("unexpected resource request")
}

func TestCategoryBoundariesRejectDirectAndBatchProfileTampering(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run profile category boundary tests")
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
		userID        = "10000000-0000-4000-8000-000000000001"
		categoryAID   = "1a000000-0000-4000-8000-000000000002"
		categoryBID   = "1b000000-0000-4000-8000-000000000003"
		profileAID    = "10000000-0000-4000-8000-000000000004"
		profileBID    = "10000000-0000-4000-8000-000000000005"
		noGrantUserID = "10000000-0000-4000-8000-000000000006"
		deviceID      = "10000000-0000-4000-8000-000000000007"
		sessionID     = "10000000-0000-4000-8000-000000000008"
	)
	ctx := context.Background()
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		_, _ = pool.Exec(ctx, `DELETE FROM profiles WHERE category_id = ANY($1::uuid[])`, []string{categoryAID, categoryBID})
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{userID, noGrantUserID})
		_, _ = pool.Exec(ctx, `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, []string{categoryAID, categoryBID})
	}
	cleanup()
	t.Cleanup(cleanup)

	pinHash, err := hashPIN(new("2468"))
	if err != nil {
		t.Fatalf("hash boundary PIN: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH inserted_categories AS (
			INSERT INTO access_categories (id, name, normalized_name, position)
			VALUES ($1::uuid, 'Boundary A', 'boundary a', 900001),
			       ($2::uuid, 'Boundary B', 'boundary b', 900002)
			RETURNING id
		), inserted_user AS (
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($3::uuid, 'category-boundary-admin', 'unused-test-hash', 'admin'),
			       ($7::uuid, 'category-boundary-no-grant', 'unused-test-hash', 'admin')
			RETURNING id
		), inserted_profiles AS (
			INSERT INTO profiles (id, category_id, name, pin_hash)
			VALUES ($4::uuid, $1::uuid, 'Boundary profile A', NULL),
			       ($5::uuid, $2::uuid, 'Boundary profile B', $6)
			RETURNING id
		), inserted_access AS (
			INSERT INTO user_profile_access (user_id, profile_id, can_manage)
			VALUES ($3::uuid, $4::uuid, true), ($3::uuid, $5::uuid, true)
			RETURNING profile_id
		), inserted_settings AS (
			INSERT INTO profile_settings (profile_id) VALUES ($4::uuid), ($5::uuid)
			RETURNING profile_id
		)
		INSERT INTO profile_avatar_images (profile_id, content_type, image_data)
		VALUES ($5::uuid, 'image/png', decode('89504e47', 'hex'))
	`, categoryAID, categoryBID, userID, profileAID, profileBID, *pinHash, noGrantUserID); err != nil {
		t.Fatalf("seed profile category boundaries: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH inserted_device AS (
			INSERT INTO devices (id, user_id, name, platform, category_id, approved_at)
			VALUES ($1::uuid, $2::uuid, 'Boundary device', 'test', $3::uuid, now())
			RETURNING id
		)
		INSERT INTO auth_sessions (
			id, user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id
		)
		VALUES (
			$4::uuid, $2::uuid, $1::uuid, decode(repeat('a1', 32), 'hex'),
			now() + interval '1 hour', now() + interval '2 hours', 'category', $3::uuid
		)
	`, deviceID, userID, categoryAID, sessionID); err != nil {
		t.Fatalf("seed category-scoped auth session: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	categoryA := categoryAID
	principal := auth.Principal{
		UserID: userID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryA,
		ActiveProfileID: new(profileAID), ProfileGrantExpiresAt: &expiresAt,
	}
	profiles := NewService(pool, time.Hour, "UTC")
	listed, err := profiles.List(ctx, principal)
	if err != nil || len(listed) != 1 || listed[0].ID != profileAID {
		t.Fatalf("category-filtered profiles = %+v, error %v", listed, err)
	}
	listMoveTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent profile move for list: %v", err)
	}
	listMoveCommitted := false
	defer func() {
		if !listMoveCommitted {
			_ = listMoveTx.Rollback(ctx)
		}
	}()
	if _, err := listMoveTx.Exec(ctx, `
		SELECT id
		FROM profiles
		WHERE id = $1::uuid
		FOR UPDATE
	`, profileAID); err != nil {
		t.Fatalf("lock profile for concurrent list move: %v", err)
	}
	if _, err := listMoveTx.Exec(ctx, `
		UPDATE profiles
		SET category_id = $2::uuid
		WHERE id = $1::uuid
	`, profileAID, categoryBID); err != nil {
		t.Fatalf("stage concurrent profile move for list: %v", err)
	}
	type profileListResult struct {
		profiles []Profile
		err      error
	}
	listResult := make(chan profileListResult, 1)
	listContext, cancelList := context.WithTimeout(ctx, 5*time.Second)
	defer cancelList()
	go func() {
		values, listErr := profiles.List(listContext, principal)
		listResult <- profileListResult{profiles: values, err: listErr}
	}()
	listWaitDeadline := time.Now().Add(3 * time.Second)
	for {
		var waitingLists int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM profiles%'
			  AND query LIKE '%FOR SHARE%'
		`).Scan(&waitingLists); err != nil {
			t.Fatalf("observe concurrent profile list: %v", err)
		}
		if waitingLists > 0 {
			break
		}
		if time.Now().After(listWaitDeadline) {
			t.Fatal("profile list did not wait on the concurrent category move")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := listMoveTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent profile move for list: %v", err)
	}
	listMoveCommitted = true
	concurrentList := <-listResult
	if concurrentList.err != nil {
		t.Fatalf("list concurrent with category move: %v", concurrentList.err)
	}
	if len(concurrentList.profiles) != 0 {
		t.Fatalf("list returned profiles after their category move committed: %+v", concurrentList.profiles)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE profiles
		SET category_id = $2::uuid
		WHERE id = $1::uuid
	`, profileAID, categoryAID); err != nil {
		t.Fatalf("restore profile category after concurrent list test: %v", err)
	}
	wrongPIN := "0000"
	if _, err := profiles.Select(ctx, principal, profileBID, &wrongPIN, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-category selection error = %v, want opaque %v", err, ErrNotFound)
	}
	var pinFailures int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM profile_pin_failures WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileBID).Scan(&pinFailures); err != nil || pinFailures != 0 {
		t.Fatalf("cross-category selection reached PIN state: count=%d err=%v", pinFailures, err)
	}
	if _, err := profiles.AvatarImage(ctx, principal, profileBID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-category avatar error = %v, want opaque %v", err, ErrNotFound)
	}
	persistedCategorySession := auth.Principal{
		UserID: userID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		SessionID: sessionID,
	}
	correctPIN := "2468"
	if _, err := profiles.Select(ctx, persistedCategorySession, profileBID, &correctPIN, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("selection through mismatched persisted session scope error = %v, want %v", err, ErrForbidden)
	}
	var activeProfileID *string
	if err := pool.QueryRow(ctx, `
		SELECT active_profile_id::text FROM auth_sessions WHERE id = $1::uuid
	`, sessionID).Scan(&activeProfileID); err != nil || activeProfileID != nil {
		t.Fatalf("mismatched persisted session gained active profile %v: %v", activeProfileID, err)
	}
	moveTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent profile move: %v", err)
	}
	moveCommitted := false
	defer func() {
		if !moveCommitted {
			_ = moveTx.Rollback(ctx)
		}
	}()
	if _, err := moveTx.Exec(ctx, `
		SELECT id
		FROM profiles
		WHERE id = $1::uuid
		FOR UPDATE
	`, profileAID); err != nil {
		t.Fatalf("lock profile for concurrent move: %v", err)
	}
	if _, err := moveTx.Exec(ctx, `
		UPDATE profiles
		SET category_id = $2::uuid
		WHERE id = $1::uuid
	`, profileAID, categoryBID); err != nil {
		t.Fatalf("stage concurrent profile move: %v", err)
	}
	racePrincipal := principal
	racePrincipal.SessionID = sessionID
	racePrincipal.ActiveProfileID = nil
	selectionResult := make(chan error, 1)
	selectionContext, cancelSelection := context.WithTimeout(ctx, 5*time.Second)
	defer cancelSelection()
	go func() {
		_, selectionErr := profiles.Select(selectionContext, racePrincipal, profileAID, nil, false)
		selectionResult <- selectionErr
	}()
	waitDeadline := time.Now().Add(3 * time.Second)
	for {
		var waitingSelections int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM profiles%'
		`).Scan(&waitingSelections); err != nil {
			t.Fatalf("observe concurrent profile selection: %v", err)
		}
		if waitingSelections > 0 {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("profile selection did not wait on the concurrent category move")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := moveTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent profile move: %v", err)
	}
	moveCommitted = true
	if err := <-selectionResult; !errors.Is(err, ErrNotFound) {
		t.Fatalf("selection concurrent with category move error = %v, want opaque %v", err, ErrNotFound)
	}
	if err := pool.QueryRow(ctx, `
		SELECT active_profile_id::text
		FROM auth_sessions
		WHERE id = $1::uuid
	`, sessionID).Scan(&activeProfileID); err != nil || activeProfileID != nil {
		t.Fatalf("concurrent category move left active profile %v: %v", activeProfileID, err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE profiles
		SET category_id = $2::uuid
		WHERE id = $1::uuid
	`, profileAID, categoryAID); err != nil {
		t.Fatalf("restore profile category after concurrent selection test: %v", err)
	}
	if _, err := settings.NewService(pool).Profile(ctx, principal, profileBID); !errors.Is(err, settings.ErrProfileNotFound) {
		t.Fatalf("cross-category settings error = %v, want opaque %v", err, settings.ErrProfileNotFound)
	}
	trackingKeys, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{1}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	trackingService, err := tracking.NewService(
		pool, trackingKeys, "", "", "", nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("create tracking service: %v", err)
	}
	if _, err := trackingService.Statuses(ctx, principal, profileBID); !errors.Is(err, tracking.ErrForbidden) {
		t.Fatalf("cross-category tracking error = %v, want %v", err, tracking.ErrForbidden)
	}

	mismatched := principal
	mismatched.ActiveProfileID = new(profileBID)
	if _, err := watchstate.NewService(pool, time.UTC).Library(ctx, mismatched, "movie", 1, 20); !errors.Is(err, watchstate.ErrProfileRequired) {
		t.Fatalf("cross-category active profile error = %v, want %v", err, watchstate.ErrProfileRequired)
	}

	var addonsBefore, collectionsBefore int
	manager := principal
	manager.Role = "member"
	t.Run("category manager cannot delete their last manageable profile", func(t *testing.T) {
		if err := profiles.Delete(ctx, manager, profileAID); !errors.Is(err, ErrLastProfile) {
			t.Fatalf("delete last manageable category profile error = %v, want %v", err, ErrLastProfile)
		}
	})
	description := "  Manager-created profile description  "
	created, err := profiles.Create(ctx, manager, CreateInput{
		Name: "Manager-created profile", Description: &description, CategoryID: categoryAID,
	})
	if err != nil {
		t.Fatalf("category manager with authoritative grant could not create profile: %v", err)
	}
	if created.Description == nil || *created.Description != "Manager-created profile description" {
		t.Fatalf("created profile description = %v", created.Description)
	}
	updatedDescription := "Updated manager profile description"
	updated, err := profiles.Update(ctx, manager, created.ID, UpdateInput{
		DescriptionSet: true,
		Description:    &updatedDescription,
	})
	if err != nil {
		t.Fatalf("update profile description: %v", err)
	}
	if updated.Description == nil || *updated.Description != updatedDescription {
		t.Fatalf("updated profile description = %v", updated.Description)
	}
	cleared, err := profiles.Update(ctx, manager, created.ID, UpdateInput{DescriptionSet: true})
	if err != nil {
		t.Fatalf("clear profile description: %v", err)
	}
	if cleared.Description != nil {
		t.Fatalf("cleared profile description = %q", *cleared.Description)
	}
	var createdCanManage bool
	if err := pool.QueryRow(ctx, `
		SELECT can_manage
		FROM user_profile_access
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, created.ID).Scan(&createdCanManage); err != nil || !createdCanManage {
		t.Fatalf("created profile management grant = %t, error %v", createdCanManage, err)
	}
	noGrantCategory := categoryAID
	pairedAdministrator := auth.Principal{
		UserID: noGrantUserID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &noGrantCategory,
	}
	if _, err := profiles.Create(ctx, pairedAdministrator, CreateInput{
		Name: "Unauthorized self-grant", CategoryID: categoryAID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("paired administrator without management grant create error = %v, want %v", err, ErrForbidden)
	}
	var auditsBefore int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM access_category_audit_events
		WHERE entity_type = 'profile' AND entity_id = $1::uuid
	`, profileAID).Scan(&auditsBefore); err != nil {
		t.Fatalf("count profile category audits before equivalent update: %v", err)
	}
	equivalentCategoryID := strings.ToUpper(categoryAID)
	if _, err := profiles.Update(ctx, principal, profileAID, UpdateInput{
		CategoryID: &equivalentCategoryID,
	}); err != nil {
		t.Fatalf("equivalent uppercase category update: %v", err)
	}
	var auditsAfter int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM access_category_audit_events
		WHERE entity_type = 'profile' AND entity_id = $1::uuid
	`, profileAID).Scan(&auditsAfter); err != nil || auditsAfter != auditsBefore {
		t.Fatalf("equivalent category spelling emitted move audit: before=%d after=%d err=%v", auditsBefore, auditsAfter, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_addons WHERE profile_id = $1::uuid`, profileAID).Scan(&addonsBefore); err != nil {
		t.Fatalf("count profile addons: %v", err)
	}
	addonService := addon.NewService(pool, categoryBoundaryAddonTransport{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := addonService.Install(ctx, principal, addon.InstallInput{
		TransportURL: "https://category-boundary.invalid/manifest.json",
		ProfileIDs:   []string{profileAID, profileBID},
	}); !errors.Is(err, addon.ErrForbidden) {
		t.Fatalf("mixed-category addon assignment error = %v, want %v", err, addon.ErrForbidden)
	}
	var addonsAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_addons WHERE profile_id = $1::uuid`, profileAID).Scan(&addonsAfter); err != nil || addonsAfter != addonsBefore {
		t.Fatalf("mixed-category addon assignment persisted: before=%d after=%d err=%v", addonsBefore, addonsAfter, err)
	}

	if err := pool.QueryRow(ctx, `SELECT count(*) FROM collection_profile_access WHERE profile_id = $1::uuid`, profileAID).Scan(&collectionsBefore); err != nil {
		t.Fatalf("count profile collections: %v", err)
	}
	collectionService := profilecollection.NewService(pool, nil, nil, nil, nil)
	if _, err := collectionService.Create(ctx, principal, profilecollection.SaveInput{
		Title: "Boundary collection",
		Folders: []profilecollection.Folder{{
			Title: "Boundary folder",
			Sources: []profilecollection.Source{{
				Kind: profilecollection.SourceKindTMDB, Title: "Popular movies", TMDB: &profilecollection.TMDBSource{},
			}},
		}},
		ProfileIDs: []string{profileAID, profileBID},
	}); !errors.Is(err, profilecollection.ErrForbidden) {
		t.Fatalf("mixed-category collection assignment error = %v, want %v", err, profilecollection.ErrForbidden)
	}
	var collectionsAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM collection_profile_access WHERE profile_id = $1::uuid`, profileAID).Scan(&collectionsAfter); err != nil || collectionsAfter != collectionsBefore {
		t.Fatalf("mixed-category collection assignment persisted: before=%d after=%d err=%v", collectionsBefore, collectionsAfter, err)
	}

	global := auth.Principal{
		UserID: userID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: new(profileBID), ProfileGrantExpiresAt: &expiresAt,
	}
	globalProfiles, err := profiles.List(ctx, global)
	foundProfileB := false
	for _, item := range globalProfiles {
		if item.ID == profileBID {
			foundProfileB = true
			break
		}
	}
	if err != nil || !foundProfileB {
		t.Fatalf("global administrator profiles omitted %s: %+v, error %v", profileBID, globalProfiles, err)
	}
	if _, err := profiles.AvatarImage(ctx, global, profileBID); err != nil {
		t.Fatalf("global administrator avatar bypass: %v", err)
	}
	if _, err := settings.NewService(pool).Profile(ctx, global, profileBID); err != nil {
		t.Fatalf("global administrator settings bypass: %v", err)
	}
}
