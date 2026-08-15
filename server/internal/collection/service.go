package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
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

type MDBListProvider interface {
	ResolveCollectionSource(context.Context, MDBListSource, int) (SourcePage, error)
}

type ArtworkPresenter interface {
	PresentResolvedFolder(context.Context, *ResolvedFolder)
	RestoreCollectionSaveInputs(context.Context, []SaveInput)
}
type ArtworkMetadataProvider interface {
	SeriesDetails(context.Context, string, string) (metadata.ProviderSeries, error)
}

type FanartEnricher interface {
	EnrichCollection(context.Context, metadata.ProviderCollection, string) (metadata.ProviderCollection, error)
	EnrichMovie(context.Context, metadata.ProviderMovie, string) (metadata.ProviderMovie, error)
	EnrichSeries(context.Context, metadata.ProviderSeries, string) (metadata.ProviderSeries, error)
}

type ProviderSet struct {
	Generation       int64
	TMDB             TMDBProvider
	Trakt            TraktProvider
	MDBList          MDBListProvider
	ArtworkMetadata  ArtworkMetadataProvider
	ExternalResolver metadata.ExternalIDResolver
	Fanart           FanartEnricher
}

func NewProviderSet(generation int64, tmdbProvider TMDBProvider, traktProvider TraktProvider, mdblistProvider MDBListProvider, artworkMetadata ArtworkMetadataProvider, resolver metadata.ExternalIDResolver, fanart FanartEnricher) ProviderSet {
	return ProviderSet{Generation: generation, TMDB: tmdbProvider, Trakt: traktProvider, MDBList: mdblistProvider, ArtworkMetadata: artworkMetadata, ExternalResolver: resolver, Fanart: fanart}
}

type ProviderSource interface {
	CollectionProviders() ProviderSet
}

type staticProviderSource struct {
	mu        sync.RWMutex
	providers ProviderSet
}

func (source *staticProviderSource) CollectionProviders() ProviderSet {
	source.mu.RLock()
	defer source.mu.RUnlock()
	return source.providers
}

type collectionProviderContextKey struct{}

type Service struct {
	pool           *pgxpool.Pool
	addon          AddonProvider
	providerSource ProviderSource
	artwork        ArtworkPresenter
	logger         *slog.Logger
}

func NewServiceWithProviderSource(pool *pgxpool.Pool, addonProvider AddonProvider, source ProviderSource) *Service {
	if source == nil {
		source = &staticProviderSource{}
	}
	return &Service{pool: pool, addon: addonProvider, providerSource: source}
}

func NewService(pool *pgxpool.Pool, addonProvider AddonProvider, tmdbProvider TMDBProvider, traktProvider TraktProvider, mdblistProvider MDBListProvider) *Service {
	return NewServiceWithProviderSource(pool, addonProvider, &staticProviderSource{providers: NewProviderSet(0, tmdbProvider, traktProvider, mdblistProvider, nil, nil, nil)})
}

func (service *Service) pinProviders(ctx context.Context) context.Context {
	if _, ok := ctx.Value(collectionProviderContextKey{}).(ProviderSet); ok {
		return ctx
	}
	return context.WithValue(ctx, collectionProviderContextKey{}, service.providerSource.CollectionProviders())
}

func collectionProviders(ctx context.Context) ProviderSet {
	providers, _ := ctx.Value(collectionProviderContextKey{}).(ProviderSet)
	return providers
}

func (service *Service) SetArtworkPresenter(presenter ArtworkPresenter) {
	service.artwork = presenter
}

func (service *Service) SetFanartEnricher(provider ArtworkMetadataProvider, resolver metadata.ExternalIDResolver, enricher FanartEnricher, logger *slog.Logger) {
	if source, ok := service.providerSource.(*staticProviderSource); ok {
		source.mu.Lock()
		providers := source.providers
		providers.ArtworkMetadata = provider
		providers.ExternalResolver = resolver
		providers.Fanart = enricher
		source.providers = providers
		source.mu.Unlock()
	}
	if logger == nil {
		logger = slog.Default()
	}
	service.logger = logger
}

func (service *Service) List(ctx context.Context, principal auth.Principal) ([]Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return nil, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin collection list: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return nil, err
	}
	collections, err := listForProfile(ctx, tx, profileID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit collection list: %w", err)
	}
	return collections, nil
}

func (service *Service) Export(ctx context.Context, principal auth.Principal) (ExportDocument, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return ExportDocument{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return ExportDocument{}, fmt.Errorf("begin collection export: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var prelockedCategoryIDs []string
	if err := tx.QueryRow(ctx, `
		SELECT ARRAY(
			SELECT DISTINCT assigned_category.category_id::text
			FROM profiles active_profile
			JOIN profile_collections pc ON true
			LEFT JOIN collection_profile_access explicit_access
			  ON explicit_access.collection_id = pc.id AND explicit_access.profile_id = active_profile.id
			LEFT JOIN collection_category_access category_access
			  ON category_access.collection_id = pc.id AND category_access.category_id = active_profile.category_id
			JOIN collection_category_access assigned_category ON assigned_category.collection_id = pc.id
			WHERE active_profile.id = $1::uuid
			  AND (explicit_access.collection_id IS NOT NULL OR category_access.collection_id IS NOT NULL)
			ORDER BY assigned_category.category_id::text
		)
	`, profileID).Scan(&prelockedCategoryIDs); err != nil {
		return ExportDocument{}, fmt.Errorf("query collection export policies: %w", err)
	}
	if len(prelockedCategoryIDs) != 0 {
		if err := authorizeGlobalCollectionPolicy(ctx, tx, principal); err != nil {
			return ExportDocument{}, err
		}
		if err := lockCollectionCategories(ctx, tx, prelockedCategoryIDs); err != nil {
			return ExportDocument{}, err
		}
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return ExportDocument{}, err
	}
	values, err := listForProfile(ctx, tx, profileID)
	if err != nil {
		return ExportDocument{}, err
	}
	managedProfileIDs := make([]string, 0)
	categoryIDs := make([]string, 0)
	for _, value := range values {
		managedProfileIDs = mergeCollectionIDs(managedProfileIDs, value.ProfileIDs)
		categoryIDs = mergeCollectionIDs(categoryIDs, value.CategoryIDs)
	}
	if len(categoryIDs) != 0 {
		if err := authorizeGlobalCollectionPolicy(ctx, tx, principal); err != nil {
			return ExportDocument{}, err
		}
		if err := lockCollectionCategories(ctx, tx, categoryIDs); err != nil {
			return ExportDocument{}, err
		}
	}
	if err := authorizeCollectionProfiles(ctx, tx, principal, profileID, managedProfileIDs); err != nil {
		return ExportDocument{}, err
	}
	identities, err := loadAddonIdentities(ctx, tx, profileID)
	if err != nil {
		return ExportDocument{}, err
	}
	portable := make([]SaveInput, len(values))
	for index := range values {
		portable[index] = portableCollection(values[index], identities.byID)
	}
	document := ExportDocument{
		SchemaVersion: ExportSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Collections:   portable,
	}
	if err := tx.Commit(ctx); err != nil {
		return ExportDocument{}, fmt.Errorf("commit collection export: %w", err)
	}
	return document, nil
}

func (service *Service) Get(ctx context.Context, principal auth.Principal, collectionID string) (Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Collection{}, err
	}
	if !validUUID(collectionID) {
		return Collection{}, ErrInvalidInput
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Collection{}, fmt.Errorf("begin collection query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return Collection{}, err
	}
	value, err := scanSharedCollection(tx.QueryRow(ctx, `
		SELECT `+sharedCollectionFields+`,
		       COALESCE(profile_order.position, explicit_access.position, category_access.position)`+sharedCollectionTail+`
		FROM profile_collections pc
		JOIN profiles active_profile ON active_profile.id = $1::uuid
		LEFT JOIN collection_profile_access explicit_access
		  ON explicit_access.collection_id = pc.id AND explicit_access.profile_id = active_profile.id
		LEFT JOIN collection_category_access category_access
		  ON category_access.collection_id = pc.id AND category_access.category_id = active_profile.category_id
		LEFT JOIN collection_profile_order profile_order
		  ON profile_order.collection_id = pc.id AND profile_order.profile_id = active_profile.id
		WHERE pc.id = $2::uuid
		  AND (explicit_access.collection_id IS NOT NULL OR category_access.collection_id IS NOT NULL)
	`, profileID, collectionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Collection{}, ErrNotFound
	}
	if err != nil {
		return Collection{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Collection{}, fmt.Errorf("commit collection query: %w", err)
	}
	return value, nil
}

func (service *Service) Management(ctx context.Context, principal auth.Principal, collectionID string) (Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Collection{}, err
	}
	if !validUUID(collectionID) {
		return Collection{}, ErrInvalidInput
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Collection{}, fmt.Errorf("begin collection management query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockCollectionPolicyBeforeProfiles(ctx, tx, principal, collectionID, nil); err != nil {
		return Collection{}, err
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return Collection{}, err
	}
	if _, err := authorizeExistingCollectionAssignments(ctx, tx, principal, profileID, collectionID); err != nil {
		return Collection{}, err
	}
	value, err := scanSharedCollection(tx.QueryRow(ctx, collectionByIDQuery, collectionID, profileID))
	if err != nil {
		return Collection{}, fmt.Errorf("query collection management details: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Collection{}, fmt.Errorf("commit collection management query: %w", err)
	}
	return value, nil
}

func (service *Service) Create(ctx context.Context, principal auth.Principal, input SaveInput) (Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Collection{}, err
	}
	assignments, err := normalizeNewCollectionAssignments(input.ProfileIDs, input.CategoryIDs, profileID)
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
	if len(assignments.categoryIDs) != 0 {
		if err := authorizeGlobalCollectionPolicy(ctx, tx, principal); err != nil {
			return Collection{}, err
		}
		if err := lockCollectionCategories(ctx, tx, assignments.categoryIDs); err != nil {
			return Collection{}, err
		}
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return Collection{}, err
	}
	if err := authorizeCollectionAssignmentPolicy(ctx, tx, principal, profileID, collectionAssignments{}, assignments); err != nil {
		return Collection{}, err
	}
	targetProfileIDs, err := lockCollectionTargetProfiles(ctx, tx, assignments)
	if err != nil {
		return Collection{}, err
	}
	if err := resolveAddonCatalogReferences(ctx, tx, []*SaveInput{&normalized}, []collectionAssignments{assignments}); err != nil {
		return Collection{}, err
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
	if err := writeCollectionAssignments(ctx, tx, created.ID, assignments); err != nil {
		return Collection{}, err
	}
	if err := ensureCollectionProfileLimits(ctx, tx, targetProfileIDs); err != nil {
		return Collection{}, err
	}
	if err := ensureCollectionCategoryLimits(ctx, tx, assignments.categoryIDs); err != nil {
		return Collection{}, err
	}
	value, err := scanSharedCollection(tx.QueryRow(ctx, collectionByIDQuery, created.ID, profileID))
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
	if err := service.preflightImportAuthorization(ctx, principal, profileID); err != nil {
		return ImportResult{}, err
	}
	if err := validateImportDocumentBudget(document); err != nil {
		return ImportResult{}, err
	}
	if service.artwork != nil {
		service.artwork.RestoreCollectionSaveInputs(ctx, document.Collections)
		if err := validateImportDocumentBudget(document); err != nil {
			return ImportResult{}, err
		}
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return ImportResult{}, fmt.Errorf("begin collection import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if document.SchemaVersion != ExportSchemaVersion {
		return ImportResult{}, invalid("unsupported collection export schema version")
	}
	if err := authorizeImportProfile(ctx, tx, principal, profileID); err != nil {
		return ImportResult{}, err
	}
	if err := lockProfileCollections(ctx, tx, profileID); err != nil {
		return ImportResult{}, err
	}
	identities, err := loadImportAddonIdentities(ctx, tx, profileID, document)
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
	inputPointers := make([]*SaveInput, len(normalized))
	inputAssignments := make([]collectionAssignments, len(normalized))
	for index := range normalized {
		inputPointers[index] = &normalized[index]
		inputAssignments[index] = collectionAssignments{profileIDs: []string{profileID}}
	}
	if err := resolveAddonCatalogReferences(ctx, tx, inputPointers, inputAssignments); err != nil {
		return ImportResult{}, err
	}
	var visibleCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT pc.id)
		FROM profiles active_profile
		JOIN profile_collections pc ON true
		LEFT JOIN collection_profile_access explicit_access ON explicit_access.collection_id = pc.id AND explicit_access.profile_id = active_profile.id
		LEFT JOIN collection_category_access category_access ON category_access.collection_id = pc.id AND category_access.category_id = active_profile.category_id
		WHERE active_profile.id = $1::uuid
		  AND (explicit_access.collection_id IS NOT NULL OR category_access.collection_id IS NOT NULL)
	`, profileID).Scan(&visibleCount); err != nil {
		return ImportResult{}, fmt.Errorf("count effective profile collections: %w", err)
	}
	if visibleCount+len(normalized) > maximumCollections {
		return ImportResult{}, invalid("the imported collections would exceed the profile limit of 100")
	}
	imported, err := insertImportedCollections(ctx, tx, profileID, normalized)
	if err != nil {
		return ImportResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ImportResult{}, fmt.Errorf("commit collection import: %w", err)
	}
	return ImportResult{Imported: len(imported), Collections: imported}, nil
}

func (service *Service) preflightImportAuthorization(ctx context.Context, principal auth.Principal, profileID string) error {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin collection import authorization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeImportProfile(ctx, tx, principal, profileID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit collection import authorization: %w", err)
	}
	return nil
}

func authorizeImportProfile(ctx context.Context, tx pgx.Tx, principal auth.Principal, profileID string) error {
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return err
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return fmt.Errorf("authorize collection import profile: %w", err)
	}
	if !authorized {
		return ErrForbidden
	}
	return nil
}

func (service *Service) Update(ctx context.Context, principal auth.Principal, collectionID string, input SaveInput) (Collection, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Collection{}, err
	}
	if !validUUID(collectionID) {
		return Collection{}, ErrInvalidInput
	}
	requested, err := normalizeCollectionAssignmentUpdate(input.ProfileIDs, input.CategoryIDs)
	if err != nil {
		return Collection{}, err
	}
	normalized, err := normalizeAndValidate(input, true)
	if err != nil {
		return Collection{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Collection{}, fmt.Errorf("begin collection update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockCollectionPolicyBeforeProfiles(ctx, tx, principal, collectionID, requested.categoryIDs); err != nil {
		return Collection{}, err
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return Collection{}, err
	}
	var assignments collectionAssignments
	if input.ProfileIDs == nil && input.CategoryIDs == nil {
		assignments, err = authorizeExistingCollectionAssignments(ctx, tx, principal, profileID, collectionID)
	} else {
		assignments, err = applyCollectionAssignments(
			ctx, tx, principal, profileID, collectionID, requested,
			input.ProfileIDs != nil, input.CategoryIDs != nil,
		)
	}
	if err != nil {
		return Collection{}, err
	}
	if err := resolveAddonCatalogReferences(ctx, tx, []*SaveInput{&normalized}, []collectionAssignments{assignments}); err != nil {
		return Collection{}, err
	}
	folders, err := json.Marshal(normalized.Folders)
	if err != nil {
		return Collection{}, fmt.Errorf("encode collection folders: %w", err)
	}
	var updatedID string
	err = tx.QueryRow(ctx, `
		UPDATE profile_collections pc
		SET title = $2, backdrop_image_url = NULLIF($3, ''), hero_enabled = $4, pin_to_top = $5,
		    focus_glow_enabled = $6, view_mode = $7, folder_cover_shape = $8,
		    folders = $9::jsonb, version = version + 1, updated_at = now()
		WHERE pc.id = $1::uuid AND pc.version = $10
		RETURNING pc.id::text
	`, collectionID, normalized.Title, normalized.BackdropImageURL, normalized.HeroEnabled,
		normalized.PinToTop, normalized.FocusGlowEnabled, normalized.ViewMode,
		normalized.FolderCoverShape, folders, normalized.ExpectedVersion).Scan(&updatedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Collection{}, ErrConflict
	}
	if err != nil {
		return Collection{}, err
	}
	value, err := scanSharedCollection(tx.QueryRow(ctx, collectionByIDQuery, updatedID, profileID))
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
	if err := lockCollectionPolicyBeforeProfiles(ctx, tx, principal, collectionID, nil); err != nil {
		return err
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return err
	}
	if _, err := authorizeExistingCollectionAssignments(ctx, tx, principal, profileID, collectionID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM profile_collections WHERE id = $1::uuid`, collectionID)
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
	var categoryIDs []string
	if err := tx.QueryRow(ctx, `
		SELECT ARRAY(
			SELECT DISTINCT category_id::text
			FROM collection_category_access
			WHERE collection_id = ANY($1::uuid[])
			ORDER BY category_id::text
		)
	`, input.CollectionIDs).Scan(&categoryIDs); err != nil {
		return nil, fmt.Errorf("query reordered collection policies: %w", err)
	}
	if len(categoryIDs) != 0 {
		if err := authorizeGlobalCollectionPolicy(ctx, tx, principal); err != nil {
			return nil, err
		}
		if err := lockCollectionCategories(ctx, tx, categoryIDs); err != nil {
			return nil, err
		}
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return nil, err
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return nil, fmt.Errorf("authorize collection reorder management: %w", err)
	}
	if !authorized {
		return nil, ErrForbidden
	}
	if err := lockProfileCollections(ctx, tx, profileID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		/* collection.lock_effective_collections_for_reorder */
		SELECT pc.id::text
		FROM profile_collections pc
		JOIN profiles active_profile ON active_profile.id = $1::uuid
		LEFT JOIN collection_profile_access explicit_access ON explicit_access.collection_id = pc.id AND explicit_access.profile_id = active_profile.id
		LEFT JOIN collection_category_access category_access ON category_access.collection_id = pc.id AND category_access.category_id = active_profile.category_id
		WHERE explicit_access.collection_id IS NOT NULL OR category_access.collection_id IS NOT NULL
		ORDER BY pc.id
		FOR UPDATE OF pc
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("lock effective collections for reorder: %w", err)
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
			INSERT INTO collection_profile_order (collection_id, profile_id, position)
			VALUES ($1::uuid, $2::uuid, $3)
			ON CONFLICT (collection_id, profile_id) DO UPDATE SET position = EXCLUDED.position
		`, collectionID, profileID, position); err != nil {
			return nil, fmt.Errorf("update collection profile order: %w", err)
		}
	}
	collections, err := listForProfile(ctx, tx, profileID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit collection reorder: %w", err)
	}
	return collections, nil
}

func listForProfile(ctx context.Context, tx pgx.Tx, profileID string) ([]Collection, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+sharedCollectionFields+`,
		       COALESCE(profile_order.position, explicit_access.position, category_access.position)`+sharedCollectionTail+`
		FROM profile_collections pc
		JOIN profiles active_profile ON active_profile.id = $1::uuid
		LEFT JOIN collection_profile_access explicit_access
		  ON explicit_access.collection_id = pc.id AND explicit_access.profile_id = active_profile.id
		LEFT JOIN collection_category_access category_access
		  ON category_access.collection_id = pc.id AND category_access.category_id = active_profile.category_id
		LEFT JOIN collection_profile_order profile_order
		  ON profile_order.collection_id = pc.id AND profile_order.profile_id = active_profile.id
		WHERE explicit_access.collection_id IS NOT NULL OR category_access.collection_id IS NOT NULL
		ORDER BY pc.pin_to_top DESC,
		         COALESCE(profile_order.position, explicit_access.position, category_access.position),
		         pc.id
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("query effective profile collections: %w", err)
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
		return nil, fmt.Errorf("iterate effective profile collections: %w", err)
	}
	return collections, nil
}

func activeProfileID(principal auth.Principal) (string, error) {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(time.Now().UTC()) {
		return "", ErrActiveProfileRequired
	}
	return *principal.ActiveProfileID, nil
}

func authorizeActiveProfile(ctx context.Context, tx pgx.Tx, principal auth.Principal, profileID string) error {
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil {
		return fmt.Errorf("authorize active collection profile: %w", err)
	}
	if !authorized {
		return ErrActiveProfileRequired
	}
	valid, err := auth.LockActiveProfileSelection(ctx, tx, principal)
	if err != nil {
		return fmt.Errorf("lock active collection selection: %w", err)
	}
	if !valid {
		return ErrActiveProfileRequired
	}
	return nil
}

func (service *Service) validateActiveProfile(ctx context.Context, principal auth.Principal) (string, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return "", err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin collection authorization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit collection authorization: %w", err)
	}
	return profileID, nil
}

func (service *Service) revalidateCollectionVersion(ctx context.Context, principal auth.Principal, collectionID string, expectedVersion int) error {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin collection revalidation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return err
	}
	var version int
	err = tx.QueryRow(ctx, `
		SELECT pc.version
		FROM profile_collections pc
		JOIN profiles active_profile ON active_profile.id = $1::uuid
		LEFT JOIN collection_profile_access explicit_access ON explicit_access.collection_id = pc.id AND explicit_access.profile_id = active_profile.id
		LEFT JOIN collection_category_access category_access ON category_access.collection_id = pc.id AND category_access.category_id = active_profile.category_id
		WHERE pc.id = $2::uuid
		  AND (explicit_access.collection_id IS NOT NULL OR category_access.collection_id IS NOT NULL)
	`, profileID, collectionID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("revalidate collection: %w", err)
	}
	if version != expectedVersion {
		return ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit collection revalidation: %w", err)
	}
	return nil
}

func lockProfileCollections(ctx context.Context, tx pgx.Tx, profileID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1))`, profileID); err != nil {
		return fmt.Errorf("lock profile collections: %w", err)
	}
	return nil
}

type addonIdentity struct {
	ID             string
	ManifestID     string
	parsedManifest addon.Manifest
}

type addonIdentitySet struct {
	byID       map[string]addonIdentity
	byManifest map[string][]addonIdentity
	assigned   []addonIdentity
}

type rowsQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type installedAddonCatalogs struct {
	manifest    addon.Manifest
	profileIDs  map[string]struct{}
	categoryIDs map[string]struct{}
}

func resolveAddonCatalogReferences(ctx context.Context, querier rowsQuerier, inputs []*SaveInput, assignments []collectionAssignments) error {
	addonIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, input := range inputs {
		for _, folder := range input.Folders {
			for _, source := range folder.Sources {
				if source.AddonCatalog == nil {
					continue
				}
				addonID := source.AddonCatalog.AddonID
				if _, exists := seen[addonID]; !exists {
					seen[addonID] = struct{}{}
					addonIDs = append(addonIDs, addonID)
				}
			}
		}
	}
	if len(addonIDs) == 0 {
		return nil
	}
	rows, err := querier.Query(ctx, `
		/* collection.lock_addon_catalog_references */
		SELECT pa.id::text, pa.manifest::text,
		       ARRAY(
		           SELECT p.id::text
		           FROM profiles p
		           WHERE EXISTS (SELECT 1 FROM addon_profile_access access WHERE access.addon_id = pa.id AND access.profile_id = p.id)
		              OR EXISTS (SELECT 1 FROM addon_category_access access WHERE access.addon_id = pa.id AND access.category_id = p.category_id)
		           ORDER BY p.id
		       ),
		       ARRAY(
		           SELECT access.category_id::text
		           FROM addon_category_access access
		           WHERE access.addon_id = pa.id
		           ORDER BY access.category_id
		       )
		FROM profile_addons pa
		WHERE pa.id = ANY($1::uuid[]) AND pa.enabled
		FOR SHARE OF pa
	`, addonIDs)
	if err != nil {
		return fmt.Errorf("query collection addon catalogs: %w", err)
	}
	defer rows.Close()
	installed := make(map[string]installedAddonCatalogs, len(addonIDs))
	for rows.Next() {
		var addonID, rawManifest string
		var assignedProfileIDs, assignedCategoryIDs []string
		if err := rows.Scan(&addonID, &rawManifest, &assignedProfileIDs, &assignedCategoryIDs); err != nil {
			return fmt.Errorf("scan collection addon catalog: %w", err)
		}
		manifest, _, err := addon.ParseManifest([]byte(rawManifest))
		if err != nil {
			return fmt.Errorf("parse collection addon manifest: %w", err)
		}
		profileSet := make(map[string]struct{}, len(assignedProfileIDs))
		for _, profileID := range assignedProfileIDs {
			profileSet[profileID] = struct{}{}
		}
		categorySet := make(map[string]struct{}, len(assignedCategoryIDs))
		for _, categoryID := range assignedCategoryIDs {
			categorySet[categoryID] = struct{}{}
		}
		installed[addonID] = installedAddonCatalogs{manifest: manifest, profileIDs: profileSet, categoryIDs: categorySet}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate collection addon catalogs: %w", err)
	}
	for inputIndex, input := range inputs {
		for folderIndex := range input.Folders {
			for sourceIndex := range input.Folders[folderIndex].Sources {
				settings := input.Folders[folderIndex].Sources[sourceIndex].AddonCatalog
				if settings == nil {
					continue
				}
				record, exists := installed[settings.AddonID]
				if !exists {
					return invalid("an addon catalog source is not installed or enabled")
				}
				for _, profileID := range assignments[inputIndex].profileIDs {
					if _, assigned := record.profileIDs[profileID]; !assigned {
						return invalid("an addon catalog source is not assigned to every collection profile")
					}
				}
				for _, categoryID := range assignments[inputIndex].categoryIDs {
					if _, assigned := record.categoryIDs[categoryID]; !assigned {
						return invalid("an addon catalog source is not assigned to every collection category")
					}
				}
				extra := make([]addon.ExtraValue, len(settings.Extra))
				for index, value := range settings.Extra {
					extra[index] = addon.ExtraValue{Name: value.Name, Value: value.Value}
				}
				path := record.manifest.ApplyCatalogDefaults(addon.ResourcePath{
					Resource: "catalog", Type: settings.Type, ID: settings.CatalogID, Extra: extra,
				})
				if !record.manifest.Supports(path) {
					return invalid("an addon catalog source is not declared by its installed manifest")
				}
				settings.ManifestID = record.manifest.ID
			}
		}
	}
	return nil
}

func loadAddonIdentities(ctx context.Context, querier rowsQuerier, profileID string) (addonIdentitySet, error) {
	rows, err := querier.Query(ctx, `
		SELECT pa.id::text, pa.manifest_id
		FROM profiles active_profile
		JOIN profile_addons pa ON true
		LEFT JOIN addon_profile_access explicit_access ON explicit_access.addon_id = pa.id AND explicit_access.profile_id = active_profile.id
		LEFT JOIN addon_category_access category_access ON category_access.addon_id = pa.id AND category_access.category_id = active_profile.category_id
		LEFT JOIN addon_profile_order profile_order ON profile_order.addon_id = pa.id AND profile_order.profile_id = active_profile.id
		WHERE active_profile.id = $1::uuid
		  AND (explicit_access.addon_id IS NOT NULL OR category_access.addon_id IS NOT NULL)
		ORDER BY COALESCE(profile_order.position, explicit_access.position, category_access.position), pa.id
	`, profileID)
	if err != nil {
		return addonIdentitySet{}, fmt.Errorf("query profile addon identities: %w", err)
	}
	defer rows.Close()
	identities := addonIdentitySet{
		byID:       make(map[string]addonIdentity),
		byManifest: make(map[string][]addonIdentity),
	}
	for rows.Next() {
		var identity addonIdentity
		if err := rows.Scan(&identity.ID, &identity.ManifestID); err != nil {
			return addonIdentitySet{}, fmt.Errorf("scan profile addon identity: %w", err)
		}
		identities.byID[identity.ID] = identity
		identities.byManifest[identity.ManifestID] = append(identities.byManifest[identity.ManifestID], identity)
	}
	if err := rows.Err(); err != nil {
		return addonIdentitySet{}, fmt.Errorf("iterate profile addon identities: %w", err)
	}
	return identities, nil
}

func loadImportAddonIdentities(ctx context.Context, querier rowsQuerier, profileID string, document ExportDocument) (addonIdentitySet, error) {
	hasAddonCatalog := false
	for _, input := range document.Collections {
		for _, folder := range input.Folders {
			for _, source := range folder.Sources {
				if source.AddonCatalog != nil {
					hasAddonCatalog = true
					break
				}
			}
			if hasAddonCatalog {
				break
			}
		}
		if hasAddonCatalog {
			break
		}
	}
	identities := addonIdentitySet{
		byID:       make(map[string]addonIdentity),
		byManifest: make(map[string][]addonIdentity),
	}
	if !hasAddonCatalog {
		return identities, nil
	}
	rows, err := querier.Query(ctx, `
		/* collection.lock_import_addon_identities */
		SELECT pa.id::text, pa.manifest_id, pa.manifest::text
		FROM profiles active_profile
		JOIN profile_addons pa ON true
		LEFT JOIN addon_profile_access explicit_access ON explicit_access.addon_id = pa.id AND explicit_access.profile_id = active_profile.id
		LEFT JOIN addon_category_access category_access ON category_access.addon_id = pa.id AND category_access.category_id = active_profile.category_id
		LEFT JOIN addon_profile_order profile_order ON profile_order.addon_id = pa.id AND profile_order.profile_id = active_profile.id
		WHERE active_profile.id = $1::uuid AND pa.enabled
		  AND (explicit_access.addon_id IS NOT NULL OR category_access.addon_id IS NOT NULL)
		ORDER BY COALESCE(profile_order.position, explicit_access.position, category_access.position), pa.id
		LIMIT $2
		FOR SHARE OF pa
	`, profileID, maximumImportSources+1)
	if err != nil {
		return addonIdentitySet{}, fmt.Errorf("query imported addon identities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if len(identities.assigned) >= maximumImportSources {
			return addonIdentitySet{}, invalid("collection import exceeds the addon identity limit")
		}
		var identity addonIdentity
		var rawManifest string
		if err := rows.Scan(&identity.ID, &identity.ManifestID, &rawManifest); err != nil {
			return addonIdentitySet{}, fmt.Errorf("scan imported addon identity: %w", err)
		}
		manifest, _, err := addon.ParseManifest([]byte(rawManifest))
		if err != nil {
			return addonIdentitySet{}, fmt.Errorf("parse imported addon manifest: %w", err)
		}
		identity.parsedManifest = manifest
		identities.byID[identity.ID] = identity
		identities.byManifest[identity.ManifestID] = append(identities.byManifest[identity.ManifestID], identity)
		identities.assigned = append(identities.assigned, identity)
	}
	if err := rows.Err(); err != nil {
		return addonIdentitySet{}, fmt.Errorf("iterate imported addon identities: %w", err)
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
		HeroEnabled:      value.HeroEnabled,
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
	input.CategoryIDs = nil
	for folderIndex := range input.Folders {
		folder := &input.Folders[folderIndex]
		folder.ID = ""
		for sourceIndex := range folder.Sources {
			source := &folder.Sources[sourceIndex]
			source.ID = ""
			if source.AddonCatalog == nil {
				continue
			}
			identity, err := resolveImportedAddonCatalog(source.AddonCatalog, identities)
			if err != nil {
				return err
			}
			source.AddonCatalog.AddonID = identity.ID
			source.AddonCatalog.ManifestID = identity.ManifestID
		}
	}
	return nil
}

func resolveImportedAddonCatalog(settings *AddonCatalogSource, identities addonIdentitySet) (addonIdentity, error) {
	if identity, exists := identities.byID[strings.TrimSpace(settings.AddonID)]; exists && addonIdentitySupportsCatalog(identity, settings) {
		return identity, nil
	}
	manifestID := strings.TrimSpace(settings.ManifestID)
	if manifestID != "" {
		matches := identities.byManifest[manifestID]
		if len(matches) == 0 {
			return addonIdentity{}, invalid(fmt.Sprintf("addon %q must be installed before importing this collection", manifestID))
		}
		if len(matches) != 1 {
			return addonIdentity{}, invalid(fmt.Sprintf("addon %q is assigned more than once and cannot be resolved safely", manifestID))
		}
		if !addonIdentitySupportsCatalog(matches[0], settings) {
			return addonIdentity{}, invalid("an addon catalog source is not declared by its installed manifest")
		}
		return matches[0], nil
	}
	var resolved addonIdentity
	for _, identity := range identities.assigned {
		if !addonIdentitySupportsCatalog(identity, settings) {
			continue
		}
		if resolved.ID != "" {
			return addonIdentity{}, invalid("an addon catalog source matches multiple assigned addons and cannot be resolved safely")
		}
		resolved = identity
	}
	if resolved.ID == "" {
		return addonIdentity{}, invalid("an addon catalog source does not match an assigned addon")
	}
	return resolved, nil
}

func addonIdentitySupportsCatalog(identity addonIdentity, settings *AddonCatalogSource) bool {
	extra := make([]addon.ExtraValue, len(settings.Extra))
	for index, value := range settings.Extra {
		extra[index] = addon.ExtraValue{Name: strings.TrimSpace(value.Name), Value: strings.TrimSpace(value.Value)}
	}
	path := identity.parsedManifest.ApplyCatalogDefaults(addon.ResourcePath{
		Resource: "catalog",
		Type:     strings.TrimSpace(settings.Type),
		ID:       strings.TrimSpace(settings.CatalogID),
		Extra:    extra,
	})
	return identity.parsedManifest.Supports(path)
}

func insertCollection(ctx context.Context, tx pgx.Tx, profileID string, normalized SaveInput, position int) (Collection, error) {
	folders, err := json.Marshal(normalized.Folders)
	if err != nil {
		return Collection{}, fmt.Errorf("encode collection folders: %w", err)
	}
	return scanCollection(tx.QueryRow(ctx, `
		INSERT INTO profile_collections (
			profile_id, title, backdrop_image_url, hero_enabled, pin_to_top, focus_glow_enabled,
			view_mode, folder_cover_shape, folders, position
		)
		VALUES ($1::uuid, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9::jsonb, $10)
		RETURNING id::text, title, COALESCE(backdrop_image_url, ''), hero_enabled, pin_to_top,
		          focus_glow_enabled, view_mode, folder_cover_shape, folders::text,
		          position, version, created_at, updated_at
	`, profileID, normalized.Title, normalized.BackdropImageURL, normalized.HeroEnabled, normalized.PinToTop,
		normalized.FocusGlowEnabled, normalized.ViewMode, normalized.FolderCoverShape, folders, position))
}

func insertImportedCollections(ctx context.Context, tx pgx.Tx, profileID string, normalized []SaveInput) ([]Collection, error) {
	if len(normalized) == 0 {
		return []Collection{}, nil
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("encode imported collections: %w", err)
	}
	rows, err := tx.Query(ctx, `
		WITH input AS (
			SELECT item.ordinality,
			       item.value->>'title' AS title,
			       COALESCE(item.value->>'backdropImageUrl', '') AS backdrop_image_url,
			       (item.value->>'heroEnabled')::boolean AS hero_enabled,
			       (item.value->>'pinToTop')::boolean AS pin_to_top,
			       (item.value->>'focusGlowEnabled')::boolean AS focus_glow_enabled,
			       item.value->>'viewMode' AS view_mode,
			       item.value->>'folderCoverShape' AS folder_cover_shape,
			       item.value->'folders' AS folders
			FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY AS item(value, ordinality)
		),
		owner_position AS (
			SELECT COALESCE(max(position) + 1, 0) AS value
			FROM profile_collections
			WHERE profile_id = $1::uuid
		),
		access_position AS (
			SELECT COALESCE(max(position) + 1, 0) AS value
			FROM collection_profile_access
			WHERE profile_id = $1::uuid
		),
		order_position AS (
			SELECT COALESCE(max(position) + 1, 0) AS value
			FROM collection_profile_order
			WHERE profile_id = $1::uuid
		),
		inserted AS (
			INSERT INTO profile_collections (
				profile_id, title, backdrop_image_url, hero_enabled, pin_to_top, focus_glow_enabled,
				view_mode, folder_cover_shape, folders, position
			)
			SELECT $1::uuid,
			       input.title,
			       NULLIF(input.backdrop_image_url, ''),
			       input.hero_enabled,
			       input.pin_to_top,
			       input.focus_glow_enabled,
			       input.view_mode,
			       input.folder_cover_shape,
			       input.folders,
			       owner_position.value + input.ordinality - 1
			FROM input
			CROSS JOIN owner_position
			ORDER BY input.ordinality
			RETURNING id, title, backdrop_image_url, hero_enabled, pin_to_top, focus_glow_enabled,
			          view_mode, folder_cover_shape, folders, position, version, created_at, updated_at
		),
		assigned AS (
			INSERT INTO collection_profile_access (collection_id, profile_id, position)
			SELECT inserted.id,
			       $1::uuid,
			       access_position.value + row_number() OVER (ORDER BY inserted.position) - 1
			FROM inserted
			CROSS JOIN access_position
			RETURNING collection_id
		),
		ordered AS (
			INSERT INTO collection_profile_order (collection_id, profile_id, position)
			SELECT inserted.id,
			       $1::uuid,
			       order_position.value + row_number() OVER (ORDER BY inserted.position) - 1
			FROM inserted
			CROSS JOIN order_position
			RETURNING collection_id, position
		)
		SELECT inserted.id::text,
		       inserted.title,
		       COALESCE(inserted.backdrop_image_url, ''),
		       inserted.hero_enabled,
		       inserted.pin_to_top,
		       inserted.focus_glow_enabled,
		       inserted.view_mode,
		       inserted.folder_cover_shape,
		       inserted.folders::text,
		       ARRAY[$1::text],
		       ARRAY[]::text[],
		       ordered.position,
		       inserted.version,
		       inserted.created_at,
		       inserted.updated_at
		FROM inserted
		JOIN assigned ON assigned.collection_id = inserted.id
		JOIN ordered ON ordered.collection_id = inserted.id
		ORDER BY inserted.position
	`, profileID, payload)
	if err != nil {
		return nil, fmt.Errorf("import collections: %w", err)
	}
	defer rows.Close()
	imported := make([]Collection, 0, len(normalized))
	for rows.Next() {
		value, scanErr := scanSharedCollection(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("query imported collections: %w", scanErr)
		}
		imported = append(imported, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported collections: %w", err)
	}
	if len(imported) != len(normalized) {
		return nil, fmt.Errorf("import collections: inserted %d of %d collections", len(imported), len(normalized))
	}
	return imported, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanCollection(scanner rowScanner) (Collection, error) {
	var value Collection
	var foldersJSON string
	if err := scanner.Scan(
		&value.ID, &value.Title, &value.BackdropImageURL, &value.HeroEnabled, &value.PinToTop,
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
