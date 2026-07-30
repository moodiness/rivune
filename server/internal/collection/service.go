package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

type AddonProvider interface {
	Fetch(context.Context, auth.Principal, string, addon.ResourcePath) (addon.ResourceResult, error)
}

type TMDBProvider interface {
	ResolveCollectionSource(context.Context, TMDBSource, int, string, string) (SourcePage, error)
	LookupCollectionSource(context.Context, string, string, string, int) ([]LookupResult, error)
	CollectionGenres(context.Context, string, string) ([]Genre, error)
}

type TraktProvider interface {
	ResolveCollectionSource(context.Context, TraktSource, int) (SourcePage, error)
}

type Service struct {
	pool  *pgxpool.Pool
	addon AddonProvider
	tmdb  TMDBProvider
	trakt TraktProvider
}

func NewService(pool *pgxpool.Pool, addonProvider AddonProvider, tmdbProvider TMDBProvider, traktProvider TraktProvider) *Service {
	return &Service{pool: pool, addon: addonProvider, tmdb: tmdbProvider, trakt: traktProvider}
}

func (service *Service) List(ctx context.Context, principal auth.Principal) ([]Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return nil, err
	}
	rows, err := service.pool.Query(ctx, `
		SELECT pc.id::text, pc.title, COALESCE(pc.backdrop_image_url, ''), pc.pin_to_top,
		       pc.focus_glow_enabled, pc.view_mode, pc.folder_cover_shape, pc.folders::text,
		       ARRAY(
		           SELECT assignment.profile_id::text
		           FROM collection_profile_access assignment
		           JOIN profiles p ON p.id = assignment.profile_id
		           WHERE assignment.collection_id = pc.id
		           ORDER BY lower(p.name), p.id
		       ),
		       access.position, pc.version, pc.created_at, pc.updated_at
		FROM collection_profile_access access
		JOIN profile_collections pc ON pc.id = access.collection_id
		WHERE access.profile_id = $1::uuid
		ORDER BY pc.pin_to_top DESC, access.position, pc.id
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("query profile collections: %w", err)
	}
	defer rows.Close()
	collections := make([]Collection, 0)
	for rows.Next() {
		value, err := scanSharedCollection(rows)
		if err != nil {
			return nil, err
		}
		collections = append(collections, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profile collections: %w", err)
	}
	return collections, nil
}

func (service *Service) Export(ctx context.Context, principal auth.Principal) (ExportDocument, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return ExportDocument{}, err
	}
	values, err := service.List(ctx, principal)
	if err != nil {
		return ExportDocument{}, err
	}
	identities, err := loadAddonIdentities(ctx, service.pool, profileID)
	if err != nil {
		return ExportDocument{}, err
	}
	portable := make([]SaveInput, len(values))
	for index := range values {
		portable[index] = portableCollection(values[index], identities.byID)
	}
	return ExportDocument{
		SchemaVersion: ExportSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Collections:   portable,
	}, nil
}

func (service *Service) Get(ctx context.Context, principal auth.Principal, collectionID string) (Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Collection{}, err
	}
	if !validUUID(collectionID) {
		return Collection{}, ErrInvalidInput
	}
	value, err := scanSharedCollection(service.pool.QueryRow(ctx, `
		SELECT pc.id::text, pc.title, COALESCE(pc.backdrop_image_url, ''), pc.pin_to_top,
		       pc.focus_glow_enabled, pc.view_mode, pc.folder_cover_shape, pc.folders::text,
		       ARRAY(
		           SELECT assignment.profile_id::text
		           FROM collection_profile_access assignment
		           JOIN profiles p ON p.id = assignment.profile_id
		           WHERE assignment.collection_id = pc.id
		           ORDER BY lower(p.name), p.id
		       ),
		       access.position, pc.version, pc.created_at, pc.updated_at
		FROM collection_profile_access access
		JOIN profile_collections pc ON pc.id = access.collection_id
		WHERE access.profile_id = $1::uuid AND pc.id = $2::uuid
	`, profileID, collectionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Collection{}, ErrNotFound
	}
	return value, err
}

func (service *Service) Create(ctx context.Context, principal auth.Principal, input SaveInput) (Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Collection{}, err
	}
	profileIDs, err := normalizeCollectionProfileIDs(input.ProfileIDs, profileID)
	if err != nil {
		return Collection{}, err
	}
	normalized, err := normalizeAndValidate(input, false)
	if err != nil {
		return Collection{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Collection{}, fmt.Errorf("begin collection creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeCollectionProfiles(ctx, tx, principal, profileID, profileIDs); err != nil {
		return Collection{}, err
	}
	var profileAtLimit bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM unnest($1::uuid[]) target(profile_id)
			WHERE (
				SELECT count(*) FROM collection_profile_access access
				WHERE access.profile_id = target.profile_id
			) >= $2
		)
	`, profileIDs, maximumCollections).Scan(&profileAtLimit); err != nil {
		return Collection{}, fmt.Errorf("count assigned profile collections: %w", err)
	}
	if profileAtLimit {
		return Collection{}, invalid("an assigned profile cannot contain more than 100 collections")
	}
	var ownerPosition int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(max(position) + 1, 0)
		FROM profile_collections
		WHERE profile_id = $1::uuid
	`, profileID).Scan(&ownerPosition); err != nil {
		return Collection{}, fmt.Errorf("calculate collection owner position: %w", err)
	}
	created, err := insertCollection(ctx, tx, profileID, normalized, ownerPosition)
	if err != nil {
		return Collection{}, err
	}
	if err := writeCollectionProfiles(ctx, tx, created.ID, profileIDs); err != nil {
		return Collection{}, err
	}
	value, err := scanSharedCollection(tx.QueryRow(ctx, collectionByIDQuery, created.ID))
	if err != nil {
		return Collection{}, fmt.Errorf("query created collection: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Collection{}, fmt.Errorf("commit collection creation: %w", err)
	}
	return value, nil
}

func (service *Service) Import(ctx context.Context, principal auth.Principal, document ExportDocument) (ImportResult, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return ImportResult{}, err
	}
	if document.SchemaVersion != ExportSchemaVersion {
		return ImportResult{}, invalid("unsupported collection export schema version")
	}
	if len(document.Collections) > maximumCollections {
		return ImportResult{}, invalid("a collection import cannot contain more than 100 collections")
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return ImportResult{}, fmt.Errorf("begin collection import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeCollectionProfiles(ctx, tx, principal, profileID, []string{profileID}); err != nil {
		return ImportResult{}, err
	}
	identities, err := loadAddonIdentities(ctx, tx, profileID)
	if err != nil {
		return ImportResult{}, err
	}
	normalized := make([]SaveInput, len(document.Collections))
	for index := range document.Collections {
		input := document.Collections[index]
		if err := prepareImportedCollection(&input, identities); err != nil {
			return ImportResult{}, fmt.Errorf("collection %d: %w", index+1, err)
		}
		normalized[index], err = normalizeAndValidate(input, false)
		if err != nil {
			return ImportResult{}, fmt.Errorf("collection %d: %w", index+1, err)
		}
	}
	var visibleCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM collection_profile_access WHERE profile_id = $1::uuid`, profileID).Scan(&visibleCount); err != nil {
		return ImportResult{}, fmt.Errorf("count profile collections: %w", err)
	}
	if visibleCount+len(normalized) > maximumCollections {
		return ImportResult{}, invalid("the imported collections would exceed the profile limit of 100")
	}
	var ownerPosition int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(max(position) + 1, 0)
		FROM profile_collections
		WHERE profile_id = $1::uuid
	`, profileID).Scan(&ownerPosition); err != nil {
		return ImportResult{}, fmt.Errorf("calculate imported collection position: %w", err)
	}
	imported := make([]Collection, len(normalized))
	for index := range normalized {
		created, insertErr := insertCollection(ctx, tx, profileID, normalized[index], ownerPosition+index)
		if insertErr != nil {
			return ImportResult{}, fmt.Errorf("import collection %d: %w", index+1, insertErr)
		}
		if err := writeCollectionProfiles(ctx, tx, created.ID, []string{profileID}); err != nil {
			return ImportResult{}, fmt.Errorf("assign imported collection %d: %w", index+1, err)
		}
		imported[index], err = scanSharedCollection(tx.QueryRow(ctx, collectionByIDQuery, created.ID))
		if err != nil {
			return ImportResult{}, fmt.Errorf("query imported collection %d: %w", index+1, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ImportResult{}, fmt.Errorf("commit collection import: %w", err)
	}
	return ImportResult{Imported: len(imported), Collections: imported}, nil
}

func (service *Service) Update(ctx context.Context, principal auth.Principal, collectionID string, input SaveInput) (Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Collection{}, err
	}
	if !validUUID(collectionID) {
		return Collection{}, ErrInvalidInput
	}
	var profileIDs []string
	if input.ProfileIDs != nil {
		profileIDs, err = normalizeCollectionProfileIDs(input.ProfileIDs, profileID)
		if err != nil {
			return Collection{}, err
		}
	}
	normalized, err := normalizeAndValidate(input, true)
	if err != nil {
		return Collection{}, err
	}
	folders, err := json.Marshal(normalized.Folders)
	if err != nil {
		return Collection{}, fmt.Errorf("encode collection folders: %w", err)
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Collection{}, fmt.Errorf("begin collection update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if profileIDs == nil {
		if err := lockProfileCollections(ctx, tx, profileID); err != nil {
			return Collection{}, err
		}
	}
	var updatedID string
	err = tx.QueryRow(ctx, `
		UPDATE profile_collections pc
		SET title = $3, backdrop_image_url = NULLIF($4, ''), pin_to_top = $5,
		    focus_glow_enabled = $6, view_mode = $7, folder_cover_shape = $8,
		    folders = $9::jsonb, version = version + 1, updated_at = now()
		WHERE pc.id = $2::uuid AND pc.version = $10
		  AND EXISTS (
		      SELECT 1 FROM collection_profile_access access
		      WHERE access.collection_id = pc.id AND access.profile_id = $1::uuid
		  )
		RETURNING pc.id::text
	`, profileID, collectionID, normalized.Title, normalized.BackdropImageURL,
		normalized.PinToTop, normalized.FocusGlowEnabled, normalized.ViewMode,
		normalized.FolderCoverShape, folders, normalized.ExpectedVersion).Scan(&updatedID)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if queryErr := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM collection_profile_access
				WHERE profile_id = $1::uuid AND collection_id = $2::uuid
			)
		`, profileID, collectionID).Scan(&exists); queryErr != nil {
			return Collection{}, fmt.Errorf("check collection update conflict: %w", queryErr)
		}
		if exists {
			return Collection{}, ErrConflict
		}
		return Collection{}, ErrNotFound
	}
	if err != nil {
		return Collection{}, err
	}
	if profileIDs != nil {
		if err := applyCollectionProfiles(ctx, tx, principal, profileID, collectionID, profileIDs); err != nil {
			return Collection{}, err
		}
	}
	value, err := scanSharedCollection(tx.QueryRow(ctx, collectionByIDQuery, updatedID))
	if err != nil {
		return Collection{}, fmt.Errorf("query updated collection: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Collection{}, fmt.Errorf("commit collection update: %w", err)
	}
	return value, nil
}

func (service *Service) Delete(ctx context.Context, principal auth.Principal, collectionID string) error {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return err
	}
	if !validUUID(collectionID) {
		return ErrInvalidInput
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin collection deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockProfileCollections(ctx, tx, profileID); err != nil {
		return err
	}
	authorized, err := auth.CanManageProfiles(ctx, tx, principal, []string{profileID})
	if err != nil {
		return err
	}
	if !authorized {
		return ErrForbidden
	}
	command, err := tx.Exec(ctx, `
		DELETE FROM profile_collections pc
		WHERE pc.id = $1::uuid
		  AND EXISTS (
		      SELECT 1 FROM collection_profile_access access
		      WHERE access.collection_id = pc.id AND access.profile_id = $2::uuid
		  )
	`, collectionID, profileID)
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit collection deletion: %w", err)
	}
	return nil
}

func (service *Service) Reorder(ctx context.Context, principal auth.Principal, input ReorderInput) ([]Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(input.CollectionIDs))
	for _, collectionID := range input.CollectionIDs {
		if !validUUID(collectionID) {
			return nil, ErrInvalidInput
		}
		if _, duplicate := seen[collectionID]; duplicate {
			return nil, ErrInvalidInput
		}
		seen[collectionID] = struct{}{}
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin collection reorder: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockProfileCollections(ctx, tx, profileID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT collection_id::text FROM collection_profile_access WHERE profile_id = $1::uuid FOR UPDATE`, profileID)
	if err != nil {
		return nil, fmt.Errorf("query collections for reorder: %w", err)
	}
	current := make(map[string]struct{})
	for rows.Next() {
		var collectionID string
		if err := rows.Scan(&collectionID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan collection for reorder: %w", err)
		}
		current[collectionID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate collections for reorder: %w", err)
	}
	rows.Close()
	if len(current) != len(seen) {
		return nil, ErrInvalidInput
	}
	for collectionID := range current {
		if _, included := seen[collectionID]; !included {
			return nil, ErrInvalidInput
		}
	}
	for position, collectionID := range input.CollectionIDs {
		if _, err := tx.Exec(ctx, `
			UPDATE collection_profile_access SET position = $3
			WHERE profile_id = $1::uuid AND collection_id = $2::uuid
		`, profileID, collectionID, position); err != nil {
			return nil, fmt.Errorf("update collection position: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit collection reorder: %w", err)
	}
	return service.List(ctx, principal)
}

func activeProfileID(principal auth.Principal) (string, error) {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(time.Now().UTC()) {
		return "", ErrActiveProfileRequired
	}
	return *principal.ActiveProfileID, nil
}

func lockProfileCollections(ctx context.Context, tx pgx.Tx, profileID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1))`, profileID); err != nil {
		return fmt.Errorf("lock profile collections: %w", err)
	}
	return nil
}

type addonIdentity struct {
	ID         string
	ManifestID string
}

type addonIdentitySet struct {
	byID       map[string]addonIdentity
	byManifest map[string]string
}

type rowsQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadAddonIdentities(ctx context.Context, querier rowsQuerier, profileID string) (addonIdentitySet, error) {
	rows, err := querier.Query(ctx, `
		SELECT pa.id::text, pa.manifest_id
		FROM addon_profile_access access
		JOIN profile_addons pa ON pa.id = access.addon_id
		WHERE access.profile_id = $1::uuid
		ORDER BY access.position, pa.id
	`, profileID)
	if err != nil {
		return addonIdentitySet{}, fmt.Errorf("query profile addon identities: %w", err)
	}
	defer rows.Close()
	identities := addonIdentitySet{
		byID:       make(map[string]addonIdentity),
		byManifest: make(map[string]string),
	}
	for rows.Next() {
		var identity addonIdentity
		if err := rows.Scan(&identity.ID, &identity.ManifestID); err != nil {
			return addonIdentitySet{}, fmt.Errorf("scan profile addon identity: %w", err)
		}
		identities.byID[identity.ID] = identity
		if _, exists := identities.byManifest[identity.ManifestID]; !exists {
			identities.byManifest[identity.ManifestID] = identity.ID
		}
	}
	if err := rows.Err(); err != nil {
		return addonIdentitySet{}, fmt.Errorf("iterate profile addon identities: %w", err)
	}
	return identities, nil
}

func portableCollection(value Collection, identities map[string]addonIdentity) SaveInput {
	for folderIndex := range value.Folders {
		folder := &value.Folders[folderIndex]
		folder.ID = ""
		for sourceIndex := range folder.Sources {
			source := &folder.Sources[sourceIndex]
			source.ID = ""
			if source.AddonCatalog == nil {
				continue
			}
			if identity, exists := identities[source.AddonCatalog.AddonID]; exists {
				source.AddonCatalog.ManifestID = identity.ManifestID
			}
		}
	}
	return SaveInput{
		Title:            value.Title,
		BackdropImageURL: value.BackdropImageURL,
		PinToTop:         value.PinToTop,
		FocusGlowEnabled: value.FocusGlowEnabled,
		ViewMode:         value.ViewMode,
		FolderCoverShape: value.FolderCoverShape,
		Folders:          value.Folders,
	}
}

func prepareImportedCollection(input *SaveInput, identities addonIdentitySet) error {
	input.ExpectedVersion = 0
	input.ProfileIDs = nil
	for folderIndex := range input.Folders {
		folder := &input.Folders[folderIndex]
		folder.ID = ""
		for sourceIndex := range folder.Sources {
			source := &folder.Sources[sourceIndex]
			source.ID = ""
			if source.AddonCatalog == nil {
				continue
			}
			settings := source.AddonCatalog
			manifestID := strings.TrimSpace(settings.ManifestID)
			resolvedID := identities.byManifest[manifestID]
			if resolvedID == "" {
				if manifestID != "" {
					return invalid(fmt.Sprintf("addon %q must be installed before importing this collection", manifestID))
				}
				resolvedID = strings.TrimSpace(settings.AddonID)
			}
			settings.AddonID = resolvedID
			settings.ManifestID = ""
		}
	}
	return nil
}

func insertCollection(ctx context.Context, tx pgx.Tx, profileID string, normalized SaveInput, position int) (Collection, error) {
	folders, err := json.Marshal(normalized.Folders)
	if err != nil {
		return Collection{}, fmt.Errorf("encode collection folders: %w", err)
	}
	return scanCollection(tx.QueryRow(ctx, `
		INSERT INTO profile_collections (
			profile_id, title, backdrop_image_url, pin_to_top, focus_glow_enabled,
			view_mode, folder_cover_shape, folders, position
		)
		VALUES ($1::uuid, $2, NULLIF($3, ''), $4, $5, $6, $7, $8::jsonb, $9)
		RETURNING id::text, title, COALESCE(backdrop_image_url, ''), pin_to_top,
		          focus_glow_enabled, view_mode, folder_cover_shape, folders::text,
		          position, version, created_at, updated_at
	`, profileID, normalized.Title, normalized.BackdropImageURL, normalized.PinToTop,
		normalized.FocusGlowEnabled, normalized.ViewMode, normalized.FolderCoverShape, folders, position))
}

type rowScanner interface {
	Scan(...any) error
}

func scanCollection(scanner rowScanner) (Collection, error) {
	var value Collection
	var foldersJSON string
	if err := scanner.Scan(
		&value.ID, &value.Title, &value.BackdropImageURL, &value.PinToTop,
		&value.FocusGlowEnabled, &value.ViewMode, &value.FolderCoverShape,
		&foldersJSON, &value.Position, &value.Version, &value.CreatedAt, &value.UpdatedAt,
	); err != nil {
		return Collection{}, err
	}
	if err := json.Unmarshal([]byte(foldersJSON), &value.Folders); err != nil {
		return Collection{}, fmt.Errorf("decode stored collection folders: %w", err)
	}
	if value.Folders == nil {
		value.Folders = []Folder{}
	}
	return value, nil
}
