package collection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

type collectionQueryCounter struct {
	count                    atomic.Int64
	addonValidationShareLock atomic.Int64
	importIdentityShareLock  atomic.Int64
}

func (counter *collectionQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	counter.count.Add(1)
	if strings.Contains(data.SQL, "collection.lock_addon_catalog_references") && strings.Contains(data.SQL, "FOR SHARE OF pa") {
		counter.addonValidationShareLock.Add(1)
	}
	if strings.Contains(data.SQL, "collection.lock_import_addon_identities") && strings.Contains(data.SQL, "FOR SHARE OF pa") {
		counter.importIdentityShareLock.Add(1)
	}
	return ctx
}

func (*collectionQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type collectionImportArtworkGate struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (*collectionImportArtworkGate) PresentResolvedFolder(context.Context, *ResolvedFolder) {}

func (gate *collectionImportArtworkGate) RestoreCollectionSaveInputs(_ context.Context, _ []SaveInput) {
	gate.calls.Add(1)
	if gate.entered == nil {
		return
	}
	close(gate.entered)
	<-gate.release
}

func TestCollectionImportUsesBoundedDatabaseRoundTripsAndPreservesOrder(t *testing.T) {
	oneCollectionQueries := measureCollectionImportQueries(t, 1)
	manyCollectionQueries := measureCollectionImportQueries(t, maximumCollections)
	if manyCollectionQueries != oneCollectionQueries {
		t.Fatalf("collection import query count grew with input: one=%d many=%d", oneCollectionQueries, manyCollectionQueries)
	}
	// Import validates and locks the authoritative active-profile session in both
	// its preflight and write transactions. Each authorization now reads a fresh
	// post-lock wall clock before accepting expiry-sensitive authority. Those fixed
	// authorization round trips raise the bound to 15 without making it depend on
	// the collection count.
	if manyCollectionQueries > 15 {
		t.Fatalf("collection import used %d database queries, want at most 15", manyCollectionQueries)
	}
}

func measureCollectionImportQueries(t *testing.T, collectionCount int) int64 {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL collection import round-trip test")
	}
	counter := &collectionQueryCounter{}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse collection test database URL: %v", err)
	}
	config.MaxConns = 1
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open collection test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	const (
		profileID       = "12345678-1234-4234-8234-123456789abc"
		categoryID      = "22345678-1234-4234-8234-123456789abc"
		userID          = "32345678-1234-4234-8234-123456789abc"
		deviceID        = "62345678-1234-4234-8234-123456789abc"
		adminSessionID  = "72345678-1234-4234-8234-123456789abc"
		viewerSessionID = "82345678-1234-4234-8234-123456789abc"
	)
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE users (id uuid PRIMARY KEY);
		CREATE TEMPORARY TABLE devices (id uuid PRIMARY KEY, user_id uuid NOT NULL);
		CREATE TEMPORARY TABLE auth_sessions (
			id uuid PRIMARY KEY, user_id uuid NOT NULL, device_id uuid NOT NULL,
			access_expires_at timestamptz NOT NULL, active_profile_id uuid,
			profile_grant_expires_at timestamptz, profile_context_hash bytea, revoked_at timestamptz
		);
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY, category_id uuid, name text NOT NULL DEFAULT ''
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL, profile_id uuid NOT NULL, can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		CREATE TEMPORARY TABLE profile_collections (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), profile_id uuid NOT NULL,
			title text NOT NULL, backdrop_image_url text, hero_enabled boolean NOT NULL DEFAULT false,
			pin_to_top boolean NOT NULL DEFAULT false, focus_glow_enabled boolean NOT NULL DEFAULT false,
			view_mode text NOT NULL, folder_cover_shape text NOT NULL, folders jsonb NOT NULL,
			position integer NOT NULL DEFAULT 0, version integer NOT NULL DEFAULT 1,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE collection_profile_access (
			collection_id uuid NOT NULL REFERENCES profile_collections(id) ON DELETE CASCADE,
			profile_id uuid NOT NULL, position integer NOT NULL,
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
		CREATE TEMPORARY TABLE profile_addons (
			id uuid PRIMARY KEY, manifest_id text NOT NULL
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL REFERENCES profile_addons(id), profile_id uuid NOT NULL, position integer NOT NULL,
			PRIMARY KEY (addon_id, profile_id)
		);
		CREATE TEMPORARY TABLE addon_category_access (
			addon_id uuid NOT NULL, category_id uuid NOT NULL, position integer NOT NULL,
			PRIMARY KEY (addon_id, category_id)
		);
		CREATE TEMPORARY TABLE addon_profile_order (
			addon_id uuid NOT NULL, profile_id uuid NOT NULL, position integer NOT NULL,
			PRIMARY KEY (addon_id, profile_id)
		);
		INSERT INTO users (id) VALUES ($3::uuid);
		INSERT INTO devices (id, user_id) VALUES ($4::uuid, $3::uuid);
		INSERT INTO profiles (id, category_id, name) VALUES ($1::uuid, $2::uuid, 'Import owner');
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($3::uuid, $1::uuid, false);
		INSERT INTO auth_sessions (
			id, user_id, device_id, access_expires_at, active_profile_id,
			profile_grant_expires_at, profile_context_hash
		) VALUES
			($5::uuid, $3::uuid, $4::uuid, now() + interval '1 hour', $1::uuid, now() + interval '1 hour', decode(repeat('e1', 32), 'hex')),
			($6::uuid, $3::uuid, $4::uuid, now() + interval '1 hour', $1::uuid, now() + interval '1 hour', decode(repeat('e2', 32), 'hex'))
	`, pgx.QueryExecModeSimpleProtocol, profileID, categoryID, userID, deviceID, adminSessionID, viewerSessionID); err != nil {
		t.Fatalf("create collection import fixtures: %v", err)
	}

	inputs := make([]SaveInput, collectionCount)
	for index := range inputs {
		inputs[index] = SaveInput{
			Title: fmt.Sprintf("Imported %03d", index+1),
			Folders: []Folder{{
				Title: "Discover",
				Sources: []Source{{
					Kind: SourceKindTMDB, Title: "Popular", TMDB: &TMDBSource{},
				}},
			}},
		}
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	activeProfileID := profileID
	viewerCategoryID := categoryID
	principal := auth.Principal{
		SessionID: adminSessionID, UserID: userID, DeviceID: deviceID,
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: &activeProfileID, ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: bytes.Repeat([]byte{0xe1}, sha256.Size),
	}
	viewerPrincipal := auth.Principal{
		SessionID: viewerSessionID, UserID: userID, DeviceID: deviceID,
		Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &viewerCategoryID, ActiveProfileID: &activeProfileID, ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: bytes.Repeat([]byte{0xe2}, sha256.Size),
	}
	viewerArtwork := &collectionImportArtworkGate{}
	viewerService := NewService(pool, nil, nil, nil, nil)
	viewerService.SetArtworkPresenter(viewerArtwork)
	if _, err := viewerService.Import(ctx, viewerPrincipal, ExportDocument{
		SchemaVersion: ExportSchemaVersion,
		Collections:   inputs[:1],
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer import error = %v, want ErrForbidden", err)
	}
	if viewerArtwork.calls.Load() != 0 {
		t.Fatal("viewer import reached artwork database preprocessing")
	}

	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access
		SET can_manage = true
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileID); err != nil {
		t.Fatalf("grant import management for race test: %v", err)
	}
	raceArtwork := &collectionImportArtworkGate{entered: make(chan struct{}), release: make(chan struct{})}
	raceService := NewService(pool, nil, nil, nil, nil)
	raceService.SetArtworkPresenter(raceArtwork)
	raceResult := make(chan error, 1)
	go func() {
		_, importErr := raceService.Import(ctx, viewerPrincipal, ExportDocument{
			SchemaVersion: ExportSchemaVersion,
			Collections:   inputs[:1],
		})
		raceResult <- importErr
	}()
	select {
	case <-raceArtwork.entered:
	case <-time.After(5 * time.Second):
		close(raceArtwork.release)
		t.Fatal("import did not reach bounded artwork preprocessing")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access
		SET can_manage = false
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileID); err != nil {
		close(raceArtwork.release)
		t.Fatalf("revoke import management during preprocessing: %v", err)
	}
	close(raceArtwork.release)
	if err := <-raceResult; !errors.Is(err, ErrForbidden) {
		t.Fatalf("racing revoked import error = %v, want ErrForbidden", err)
	}
	var rejectedWrites int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM profile_collections`).Scan(&rejectedWrites); err != nil {
		t.Fatalf("count rejected import writes: %v", err)
	}
	if rejectedWrites != 0 {
		t.Fatalf("racing revoked import wrote %d collections", rejectedWrites)
	}

	artwork := &collectionImportArtworkGate{}
	service := NewService(pool, nil, nil, nil, nil)
	service.SetArtworkPresenter(artwork)
	counter.count.Store(0)
	result, err := service.Import(ctx, principal, ExportDocument{
		SchemaVersion: ExportSchemaVersion,
		Collections:   inputs,
	})
	if err != nil {
		t.Fatalf("import %d collections: %v", collectionCount, err)
	}
	if artwork.calls.Load() != 1 {
		t.Fatalf("import restored artwork in %d batches, want exactly one", artwork.calls.Load())
	}
	if result.Imported != collectionCount || len(result.Collections) != collectionCount {
		t.Fatalf("unexpected import result: %+v", result)
	}
	for index, value := range result.Collections {
		if value.Title != inputs[index].Title || value.Position != index {
			t.Fatalf("import order changed at index %d: %+v", index, value)
		}
		if len(value.ProfileIDs) != 1 || value.ProfileIDs[0] != profileID {
			t.Fatalf("import profile assignment changed at index %d: %+v", index, value.ProfileIDs)
		}
		if len(value.CategoryIDs) != 0 {
			t.Fatalf("import category assignment changed at index %d: %+v", index, value.CategoryIDs)
		}
		if value.ViewMode != ViewModeTabbedGrid || value.FolderCoverShape != TileShapePoster ||
			len(value.Folders) != 1 || len(value.Folders[0].Sources) != 1 ||
			value.Folders[0].Sources[0].Kind != SourceKindTMDB {
			t.Fatalf("imported collection payload changed at index %d: %+v", index, value)
		}
	}
	return counter.count.Load()
}

func TestCollectionImportRoundTripRebindsLegacyAddonCatalog(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL collection import round-trip test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse collection test database URL: %v", err)
	}
	counter := &collectionQueryCounter{}
	config.ConnConfig.Tracer = counter
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open collection test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	const (
		profileID       = "12345678-1234-4234-8234-123456789abc"
		categoryID      = "22345678-1234-4234-8234-123456789abc"
		currentAddonID  = "42345678-1234-4234-8234-123456789abc"
		staleAddonID    = "52345678-1234-4234-8234-123456789abc"
		userID          = "92345678-1234-4234-8234-123456789abc"
		deviceID        = "a2345678-1234-4234-8234-123456789abc"
		sessionID       = "b2345678-1234-4234-8234-123456789abc"
		currentManifest = "org.example.current"
	)
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE users (id uuid PRIMARY KEY);
		CREATE TEMPORARY TABLE devices (id uuid PRIMARY KEY, user_id uuid NOT NULL);
		CREATE TEMPORARY TABLE auth_sessions (
			id uuid PRIMARY KEY, user_id uuid NOT NULL, device_id uuid NOT NULL,
			access_expires_at timestamptz NOT NULL, active_profile_id uuid,
			profile_grant_expires_at timestamptz, profile_context_hash bytea, revoked_at timestamptz
		);
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY, category_id uuid, name text NOT NULL DEFAULT ''
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL, profile_id uuid NOT NULL, can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		CREATE TEMPORARY TABLE profile_collections (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), profile_id uuid NOT NULL,
			title text NOT NULL, backdrop_image_url text, hero_enabled boolean NOT NULL DEFAULT false,
			pin_to_top boolean NOT NULL DEFAULT false, focus_glow_enabled boolean NOT NULL DEFAULT false,
			view_mode text NOT NULL, folder_cover_shape text NOT NULL, folders jsonb NOT NULL,
			position integer NOT NULL DEFAULT 0, version integer NOT NULL DEFAULT 1,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TEMPORARY TABLE collection_profile_access (
			collection_id uuid NOT NULL REFERENCES profile_collections(id) ON DELETE CASCADE,
			profile_id uuid NOT NULL, position integer NOT NULL,
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
		CREATE TEMPORARY TABLE profile_addons (
			id uuid PRIMARY KEY, manifest_id text NOT NULL, manifest jsonb NOT NULL, enabled boolean NOT NULL DEFAULT true
		);
		CREATE TEMPORARY TABLE addon_profile_access (
			addon_id uuid NOT NULL REFERENCES profile_addons(id), profile_id uuid NOT NULL, position integer NOT NULL,
			PRIMARY KEY (addon_id, profile_id)
		);
		CREATE TEMPORARY TABLE addon_category_access (
			addon_id uuid NOT NULL, category_id uuid NOT NULL, position integer NOT NULL,
			PRIMARY KEY (addon_id, category_id)
		);
		CREATE TEMPORARY TABLE addon_profile_order (
			addon_id uuid NOT NULL, profile_id uuid NOT NULL, position integer NOT NULL,
			PRIMARY KEY (addon_id, profile_id)
		);
		INSERT INTO users (id) VALUES ($5::uuid);
		INSERT INTO devices (id, user_id) VALUES ($6::uuid, $5::uuid);
		INSERT INTO profiles (id, category_id, name) VALUES ($1::uuid, $2::uuid, 'Import owner');
		INSERT INTO profile_addons (id, manifest_id, manifest) VALUES (
			$3::uuid, $4,
			'{"id":"org.example.current","version":"1.0.0","name":"Current metadata","types":["movie"],"resources":["catalog"],"catalogs":[{"type":"movie","id":"popular"}]}'::jsonb
		);
		INSERT INTO addon_profile_access (addon_id, profile_id, position) VALUES ($3::uuid, $1::uuid, 0);
		INSERT INTO auth_sessions (
			id, user_id, device_id, access_expires_at, active_profile_id,
			profile_grant_expires_at, profile_context_hash
		) VALUES (
			$7::uuid, $5::uuid, $6::uuid, now() + interval '1 hour', $1::uuid,
			now() + interval '1 hour', decode(repeat('f1', 32), 'hex')
		)
	`, pgx.QueryExecModeSimpleProtocol, profileID, categoryID, currentAddonID, currentManifest, userID, deviceID, sessionID); err != nil {
		t.Fatalf("create legacy addon import fixtures: %v", err)
	}

	activeProfileID := profileID
	expiresAt := time.Now().UTC().Add(time.Hour)
	principal := auth.Principal{
		SessionID: sessionID, UserID: userID, DeviceID: deviceID,
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: &activeProfileID, ProfileGrantExpiresAt: &expiresAt,
		ProfileContextHash: bytes.Repeat([]byte{0xf1}, sha256.Size),
	}
	result, err := NewService(pool, nil, nil, nil, nil).Import(ctx, principal, ExportDocument{
		SchemaVersion: 1,
		Collections:   []SaveInput{legacyAddonCatalogImport(staleAddonID, "movie", "popular")},
	})
	if err != nil {
		t.Fatalf("import legacy addon catalog: %v", err)
	}
	if result.Imported != 1 || len(result.Collections) != 1 {
		t.Fatalf("unexpected legacy import result: %+v", result)
	}
	settings := result.Collections[0].Folders[0].Sources[0].AddonCatalog
	if settings.AddonID != currentAddonID || settings.ManifestID != currentManifest {
		t.Fatalf("imported addon identity was not rebound: %+v", settings)
	}
	if counter.importIdentityShareLock.Load() != 1 || counter.addonValidationShareLock.Load() != 1 {
		t.Fatalf("collection import addon locks = identities:%d validation:%d, want one FOR SHARE query each", counter.importIdentityShareLock.Load(), counter.addonValidationShareLock.Load())
	}

	var rawFolders string
	if err := pool.QueryRow(ctx, `SELECT folders::text FROM profile_collections WHERE id = $1::uuid`, result.Collections[0].ID).Scan(&rawFolders); err != nil {
		t.Fatalf("query imported collection folders: %v", err)
	}
	var persisted []Folder
	if err := json.Unmarshal([]byte(rawFolders), &persisted); err != nil {
		t.Fatalf("decode imported collection folders: %v", err)
	}
	persistedSettings := persisted[0].Sources[0].AddonCatalog
	if persistedSettings.AddonID != currentAddonID || persistedSettings.ManifestID != currentManifest {
		t.Fatalf("persisted addon identity was not rebound: %+v", persistedSettings)
	}
	if _, err := pool.Exec(ctx, `UPDATE profile_addons SET enabled = false WHERE id = $1::uuid`, currentAddonID); err != nil {
		t.Fatalf("disable addon for collection binding checks: %v", err)
	}
	identities, err := loadAddonIdentities(ctx, pool, profileID)
	if err != nil {
		t.Fatalf("load disabled addon export identity: %v", err)
	}
	if identity, exists := identities.byID[currentAddonID]; !exists || identity.ManifestID != currentManifest {
		t.Fatalf("disabled addon export identity was not preserved: %+v", identities.byID)
	}
	direct := legacyAddonCatalogImport(currentAddonID, "movie", "popular")
	if err := resolveAddonCatalogReferences(ctx, pool, []*SaveInput{&direct}, []collectionAssignments{{profileIDs: []string{profileID}}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("disabled direct addon catalog validation error = %v, want %v", err, ErrInvalidInput)
	}
	if _, err := NewService(pool, nil, nil, nil, nil).Import(ctx, principal, ExportDocument{
		SchemaVersion: 1,
		Collections:   []SaveInput{legacyAddonCatalogImport(staleAddonID, "movie", "popular")},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("disabled addon import binding error = %v, want %v", err, ErrInvalidInput)
	}
}
