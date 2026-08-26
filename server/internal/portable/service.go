package portable

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/profile"
	"github.com/moodiness/rivune/server/internal/runtimesettings"
)

type Service struct {
	pool                  *pgxpool.Pool
	runtimeSettings       *runtimesettings.Source
	afterExportTitlesRead func()
}

func NewService(pool *pgxpool.Pool, runtimeSettings *runtimesettings.Source) *Service {
	return &Service{pool: pool, runtimeSettings: runtimeSettings}
}

type exportedAddon struct {
	key        string
	manifestID string
}

type exportedTitle struct {
	id            string
	value         Title
	sourceAddonID string
}

func (s *Service) Export(ctx context.Context, principal auth.Principal, profileID string) (Document, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Document{}, fmt.Errorf("begin portable archive export: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	principal, err = s.authorize(ctx, tx, principal, profileID)
	if err != nil {
		return Document{}, err
	}
	document := Document{Version: DocumentVersion, ExportedAt: time.Now().UTC(), Addons: []Addon{}, Collections: []PortableCollection{}, Titles: []Title{}, Library: []LibraryState{}, Progress: []ProgressState{}, Favorites: []FavoriteState{}, UserData: []UserDataState{}, ContinueDismissals: []ContinueDismissal{}, TrackingPreferences: []TrackingPreference{}}
	if err := exportIdentity(ctx, tx, profileID, &document); err != nil {
		return Document{}, err
	}
	if err := exportSettings(ctx, tx, profileID, &document); err != nil {
		return Document{}, err
	}
	addonKeys, err := exportAddons(ctx, tx, profileID, &document)
	if err != nil {
		return Document{}, err
	}
	if err := exportCollections(ctx, tx, profileID, addonKeys, &document); err != nil {
		return Document{}, err
	}
	titleKeys, titleIDs, err := exportTitles(ctx, tx, profileID, addonKeys, &document)
	if err != nil {
		return Document{}, err
	}
	if s.afterExportTitlesRead != nil {
		s.afterExportTitlesRead()
	}
	if err := exportStates(ctx, tx, profileID, titleKeys, titleIDs, &document); err != nil {
		return Document{}, err
	}
	if err := exportContinueDismissals(ctx, tx, profileID, titleKeys, &document); err != nil {
		return Document{}, err
	}
	if err := exportTrackingPreferences(ctx, tx, profileID, &document); err != nil {
		return Document{}, err
	}
	if err := Validate(document, time.Now().UTC()); err != nil {
		return Document{}, fmt.Errorf("validate exported portable archive: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, fmt.Errorf("commit portable archive export: %w", err)
	}
	return document, nil
}

func (s *Service) authorize(ctx context.Context, tx pgx.Tx, captured auth.Principal, profileID string) (auth.Principal, error) {
	runtime := runtimesettings.Load(ctx, s.runtimeSettings)
	principal, authorized, err := auth.ReloadAndLockPrincipal(ctx, tx, captured, time.Now().UTC(), runtime.Location)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "40001" {
			return auth.Principal{}, ErrForbidden
		}
		return auth.Principal{}, fmt.Errorf("revalidate portable archive session: %w", err)
	}
	if !authorized || !principal.IsGlobalAdministrator() {
		return auth.Principal{}, ErrForbidden
	}
	authorized, err = auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{strings.TrimSpace(profileID)}, true)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("authorize portable archive profile: %w", err)
	}
	if !authorized {
		return auth.Principal{}, ErrProfileNotFound
	}
	return principal, nil
}

func portableKey(kind, id string) string {
	sum := sha256.Sum256([]byte("rivune-portable-v1\x00" + kind + "\x00" + id))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func exportIdentity(ctx context.Context, tx pgx.Tx, profileID string, document *Document) error {
	var preset string
	var image []byte
	if err := tx.QueryRow(ctx, `
		SELECT profile.name,profile.description,profile.is_child,profile.avatar_preset,avatar.image_data
		FROM profiles profile LEFT JOIN profile_avatar_images avatar ON avatar.profile_id=profile.id
		WHERE profile.id=$1::uuid
	`, profileID).Scan(&document.Identity.Name, &document.Identity.Description, &document.Identity.IsChild, &preset, &image); err != nil {
		return fmt.Errorf("export profile identity: %w", err)
	}
	document.Identity.Avatar = Avatar{Kind: "preset", PresetID: preset}
	if len(image) > 0 {
		digest := sha256.Sum256(image)
		document.Identity.Avatar = Avatar{Kind: "image", ContentType: "image/png", SHA256: hex.EncodeToString(digest[:]), Data: image}
	}
	return nil
}

func exportContinueDismissals(ctx context.Context, tx pgx.Tx, profileID string, titleKeys map[string]string, document *Document) error {
	rows, err := tx.Query(ctx, `SELECT title_id::text,dismissed_at FROM profile_continue_dismissals WHERE profile_id=$1::uuid ORDER BY title_id`, profileID)
	if err != nil {
		return fmt.Errorf("export continue dismissals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var titleID string
		var value ContinueDismissal
		if err := rows.Scan(&titleID, &value.DismissedAt); err != nil {
			return fmt.Errorf("scan continue dismissal: %w", err)
		}
		key, exists := titleKeys[titleID]
		if !exists {
			continue
		}
		value.TitleKey = key
		document.ContinueDismissals = append(document.ContinueDismissals, value)
	}
	return rows.Err()
}

func exportSettings(ctx context.Context, tx pgx.Tx, profileID string, document *Document) error {
	var version int
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT schema_version, settings FROM profile_settings WHERE profile_id = $1::uuid`, profileID).Scan(&version, &raw); err != nil {
		return fmt.Errorf("export profile settings: %w", err)
	}
	if version != 1 {
		return fmt.Errorf("export profile settings: unsupported schema version %d", version)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document.Settings); err != nil {
		return fmt.Errorf("decode profile settings for export: %w", err)
	}
	return nil
}

func exportAddons(ctx context.Context, tx pgx.Tx, profileID string, document *Document) (map[string]exportedAddon, error) {
	rows, err := tx.Query(ctx, `SELECT addon.id::text, addon.transport_url, addon.manifest, addon.manifest_id, addon.enabled, COALESCE(ordering.position, access.position), binding.portable_key FROM profile_addons addon JOIN addon_profile_access access ON access.addon_id = addon.id AND access.profile_id = $1::uuid LEFT JOIN addon_profile_order ordering ON ordering.addon_id = addon.id AND ordering.profile_id = access.profile_id LEFT JOIN portable_profile_resource_bindings binding ON binding.profile_id=access.profile_id AND binding.resource_kind='addon' AND binding.resource_id=addon.id ORDER BY COALESCE(ordering.position, access.position), addon.id`, profileID)
	if err != nil {
		return nil, fmt.Errorf("export profile add-ons: %w", err)
	}
	defer rows.Close()
	addons := make(map[string]exportedAddon)
	for rows.Next() {
		var id, url, manifestID string
		var boundKey *string
		var raw []byte
		var enabled bool
		var position int
		if err := rows.Scan(&id, &url, &raw, &manifestID, &enabled, &position, &boundKey); err != nil {
			return nil, fmt.Errorf("scan profile add-on: %w", err)
		}
		key := portableKey("addon", id)
		if boundKey != nil {
			key = *boundKey
		}
		addons[id] = exportedAddon{key: key, manifestID: manifestID}
		document.Addons = append(document.Addons, Addon{Key: key, TransportURL: url, Manifest: json.RawMessage(raw), Enabled: enabled, Position: position})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profile add-ons: %w", err)
	}
	return addons, nil
}

func exportCollections(ctx context.Context, tx pgx.Tx, profileID string, addons map[string]exportedAddon, document *Document) error {
	rows, err := tx.Query(ctx, `SELECT value.id::text,value.title,COALESCE(value.backdrop_image_url,''),value.hero_enabled,value.pin_to_top,value.focus_glow_enabled,value.view_mode,value.folder_cover_shape,value.folders,binding.portable_key FROM profile_collections value JOIN collection_profile_access access ON access.collection_id=value.id AND access.profile_id=$1::uuid LEFT JOIN collection_profile_order ordering ON ordering.collection_id=value.id AND ordering.profile_id=access.profile_id LEFT JOIN portable_profile_resource_bindings binding ON binding.profile_id=access.profile_id AND binding.resource_kind='collection' AND binding.resource_id=value.id ORDER BY COALESCE(ordering.position,access.position),value.id`, profileID)
	if err != nil {
		return fmt.Errorf("export profile collections: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var boundKey *string
		var input collection.SaveInput
		var folders []byte
		if err := rows.Scan(&id, &input.Title, &input.BackdropImageURL, &input.HeroEnabled, &input.PinToTop, &input.FocusGlowEnabled, &input.ViewMode, &input.FolderCoverShape, &folders, &boundKey); err != nil {
			return fmt.Errorf("scan profile collection: %w", err)
		}
		if err := json.Unmarshal(folders, &input.Folders); err != nil {
			return fmt.Errorf("decode profile collection: %w", err)
		}
		for fi := range input.Folders {
			input.Folders[fi].ID = ""
			for si := range input.Folders[fi].Sources {
				source := &input.Folders[fi].Sources[si]
				source.ID = ""
				if source.AddonCatalog != nil {
					identity, ok := addons[source.AddonCatalog.AddonID]
					if !ok {
						return fmt.Errorf("%w: explicitly assigned collection references an add-on that is not explicitly assigned", ErrConflict)
					}
					source.AddonCatalog.AddonID = deterministicUUID(identity.key, "archive-addon-ref")
					source.AddonCatalog.ManifestID = identity.manifestID
				}
			}
		}
		key := portableKey("collection", id)
		if boundKey != nil {
			key = *boundKey
		}
		document.Collections = append(document.Collections, PortableCollection{Key: key, Value: input})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate profile collections: %w", err)
	}
	return nil
}

func exportTitles(ctx context.Context, tx pgx.Tx, profileID string, addons map[string]exportedAddon, document *Document) (map[string]string, []string, error) {
	rows, err := tx.Query(ctx, `WITH RECURSIVE selected(id) AS (SELECT title_id FROM profile_library WHERE profile_id=$1::uuid UNION SELECT title_id FROM profile_progress WHERE profile_id=$1::uuid UNION SELECT title_id FROM profile_favorites WHERE profile_id=$1::uuid UNION SELECT title_id FROM profile_user_data WHERE profile_id=$1::uuid UNION SELECT title.parent_id FROM titles title JOIN selected child ON child.id=title.id WHERE title.parent_id IS NOT NULL) SELECT title.id::text,title.media_type,title.parent_id::text,title.ordinal,COALESCE(title.display_title,''),COALESCE(title.poster_url,''),COALESCE(title.background_url,''),COALESCE(title.release_info,''),COALESCE(title.release_date::text,''),COALESCE(title.resource_id,''),COALESCE(title.resource_provider,''),COALESCE(title.source_addon_id::text,''),COALESCE(title.source_catalog_id,''),COALESCE(title.source_name,''),COALESCE(title.country,''),COALESCE(title.language,''),COALESCE(title.category,''),binding.portable_key FROM titles title JOIN selected ON selected.id=title.id LEFT JOIN portable_profile_resource_bindings binding ON binding.profile_id=$1::uuid AND binding.resource_kind='title' AND binding.resource_id=title.id ORDER BY CASE title.media_type WHEN 'movie' THEN 0 WHEN 'series' THEN 0 WHEN 'tv' THEN 0 WHEN 'season' THEN 1 ELSE 2 END,title.id`, profileID)
	if err != nil {
		return nil, nil, fmt.Errorf("export profile titles: %w", err)
	}
	defer rows.Close()
	exported := make([]exportedTitle, 0)
	idToKey := make(map[string]string)
	for rows.Next() {
		var value exportedTitle
		var parentID *string
		var boundKey *string
		if err := rows.Scan(&value.id, &value.value.MediaType, &parentID, &value.value.Ordinal, &value.value.DisplayTitle, &value.value.PosterURL, &value.value.BackgroundURL, &value.value.ReleaseInfo, &value.value.ReleaseDate, &value.value.ResourceID, &value.value.ResourceProvider, &value.sourceAddonID, &value.value.SourceCatalogID, &value.value.SourceName, &value.value.Country, &value.value.Language, &value.value.Category, &boundKey); err != nil {
			return nil, nil, fmt.Errorf("scan profile title: %w", err)
		}
		value.value.Key = portableKey("title", value.id)
		if boundKey != nil {
			value.value.Key = *boundKey
		}
		idToKey[value.id] = value.value.Key
		if parentID != nil {
			value.value.ParentKey = *parentID
		}
		if value.sourceAddonID != "" {
			identity, ok := addons[value.sourceAddonID]
			if !ok {
				return nil, nil, fmt.Errorf("%w: state title references an add-on that is not explicitly assigned", ErrConflict)
			}
			value.value.SourceAddonKey = identity.key
		}
		exported = append(exported, value)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate profile titles: %w", err)
	}
	ids := make([]string, 0, len(exported))
	for i := range exported {
		ids = append(ids, exported[i].id)
		if exported[i].value.ParentKey != "" {
			exported[i].value.ParentKey = idToKey[exported[i].value.ParentKey]
		}
	}
	if len(ids) > 0 {
		identityRows, err := tx.Query(ctx, `SELECT identity.title_id::text,identity.provider,identity.namespace,identity.external_id,false FROM title_external_ids identity WHERE identity.title_id=ANY($1::uuid[]) UNION ALL SELECT identity.title_id::text,identity.provider,identity.namespace,identity.external_id,true FROM profile_title_external_ids identity WHERE identity.profile_id=$2::uuid AND identity.title_id=ANY($1::uuid[]) ORDER BY 1,2,3,4,5`, ids, profileID)
		if err != nil {
			return nil, nil, fmt.Errorf("export title identities: %w", err)
		}
		identityByTitle := make(map[string][]ExternalID)
		for identityRows.Next() {
			var id string
			var value ExternalID
			if err := identityRows.Scan(&id, &value.Provider, &value.Namespace, &value.ExternalID, &value.ProfileScoped); err != nil {
				identityRows.Close()
				return nil, nil, fmt.Errorf("scan title identity: %w", err)
			}
			identityByTitle[id] = append(identityByTitle[id], value)
		}
		if err := identityRows.Err(); err != nil {
			identityRows.Close()
			return nil, nil, fmt.Errorf("iterate title identities: %w", err)
		}
		identityRows.Close()
		for i := range exported {
			exported[i].value.ExternalIDs = identityByTitle[exported[i].id]
		}
	}
	for _, value := range exported {
		document.Titles = append(document.Titles, value.value)
	}
	return idToKey, ids, nil
}

func exportStates(ctx context.Context, tx pgx.Tx, profileID string, titleKeys map[string]string, titleIDs []string, document *Document) error {
	if len(titleIDs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT title_id::text,added_at,updated_at FROM profile_library WHERE profile_id=$1::uuid ORDER BY title_id`, profileID)
	if err != nil {
		return fmt.Errorf("export library: %w", err)
	}
	for rows.Next() {
		var id string
		var v LibraryState
		if err := rows.Scan(&id, &v.AddedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		v.TitleKey = titleKeys[id]
		document.Library = append(document.Library, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT title_id::text,position_seconds,duration_seconds,completed,version,last_watched_at,updated_at FROM profile_progress WHERE profile_id=$1::uuid ORDER BY title_id`, profileID)
	if err != nil {
		return fmt.Errorf("export progress: %w", err)
	}
	for rows.Next() {
		var id string
		var v ProgressState
		if err := rows.Scan(&id, &v.PositionSeconds, &v.DurationSeconds, &v.Completed, &v.Version, &v.LastWatchedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		v.TitleKey = titleKeys[id]
		document.Progress = append(document.Progress, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT title_id::text,created_at,updated_at FROM profile_favorites WHERE profile_id=$1::uuid ORDER BY title_id`, profileID)
	if err != nil {
		return fmt.Errorf("export favorites: %w", err)
	}
	for rows.Next() {
		var id string
		var v FavoriteState
		if err := rows.Scan(&id, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		v.TitleKey = titleKeys[id]
		document.Favorites = append(document.Favorites, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT title_id::text,rating,rating_set,played_percentage,played_percentage_set,unplayed_item_count,unplayed_item_count_set,play_count,play_count_set,likes,likes_set,last_played_date,last_played_date_submicrosecond,last_played_date_set,updated_at FROM profile_user_data WHERE profile_id=$1::uuid ORDER BY title_id`, profileID)
	if err != nil {
		return fmt.Errorf("export user data: %w", err)
	}
	for rows.Next() {
		var id string
		var v UserDataState
		if err := rows.Scan(&id, &v.Rating, &v.RatingSet, &v.PlayedPercentage, &v.PlayedPercentageSet, &v.UnplayedItemCount, &v.UnplayedItemCountSet, &v.PlayCount, &v.PlayCountSet, &v.Likes, &v.LikesSet, &v.LastPlayedDate, &v.LastPlayedDateSubmicrosecond, &v.LastPlayedDateSet, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		v.TitleKey = titleKeys[id]
		document.UserData = append(document.UserData, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return nil
}

func exportTrackingPreferences(ctx context.Context, tx pgx.Tx, profileID string, document *Document) error {
	rows, err := tx.Query(ctx, `SELECT provider,sync_watched,sync_progress,sync_library FROM profile_tracking_preferences WHERE profile_id=$1::uuid ORDER BY provider`, profileID)
	if err != nil {
		return fmt.Errorf("export tracking preferences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v TrackingPreference
		if err := rows.Scan(&v.Provider, &v.SyncWatched, &v.SyncProgress, &v.SyncLibrary); err != nil {
			return err
		}
		document.TrackingPreferences = append(document.TrackingPreferences, v)
	}
	return rows.Err()
}

func (s *Service) Import(ctx context.Context, principal auth.Principal, profileID string, document Document) (ImportReport, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ImportReport{}, fmt.Errorf("begin portable archive import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, seed := range []int{0, 1} {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, $2))`, profileID, seed); err != nil {
			return ImportReport{}, fmt.Errorf("lock portable archive profile resources: %w", err)
		}
	}
	principal, err = s.authorize(ctx, tx, principal, profileID)
	if err != nil {
		return ImportReport{}, err
	}
	if err := Validate(document, time.Now().UTC()); err != nil {
		return ImportReport{}, err
	}
	if err := lockImportResources(ctx, tx, profileID, document); err != nil {
		return ImportReport{}, err
	}

	if err := validateTargetSettings(ctx, tx, document); err != nil {
		return ImportReport{}, err
	}
	report := ImportReport{Mode: "merge", ProfileID: profileID}
	identityChanged, avatarChanged, err := importIdentity(ctx, tx, profileID, document.Identity)
	if err != nil {
		return ImportReport{}, err
	}
	report.Sections = append(report.Sections, section("identity", 1, identityChanged), section("avatar", 1, avatarChanged))
	if changed, err := importSettings(ctx, tx, profileID, document); err != nil {
		return ImportReport{}, err
	} else {
		report.Sections = append(report.Sections, section("settings", 1, changed))
	}
	addonIDs, addonManifests, created, changed, err := importAddons(ctx, tx, profileID, document)
	if err != nil {
		return ImportReport{}, err
	}
	report.Sections = append(report.Sections, counts("addons", len(document.Addons), created, changed))
	created, changed, err = importCollections(ctx, tx, profileID, document, addonIDs, addonManifests)
	if err != nil {
		return ImportReport{}, err
	}
	report.Sections = append(report.Sections, counts("collections", len(document.Collections), created, changed))
	titleIDs, created, changed, err := importTitles(ctx, tx, profileID, document, addonIDs)
	if err != nil {
		return ImportReport{}, err
	}
	report.Sections = append(report.Sections, counts("titles", len(document.Titles), created, changed))
	stateReports, err := importStates(ctx, tx, profileID, document, titleIDs)
	if err != nil {
		return ImportReport{}, err
	}
	report.Sections = append(report.Sections, stateReports...)
	dismissalReport, err := importContinueDismissals(ctx, tx, profileID, document, titleIDs)
	if err != nil {
		return ImportReport{}, err
	}
	report.Sections = append(report.Sections, dismissalReport)
	if len(document.TrackingPreferences) > 0 {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x524956554e454f42)); err != nil {
			return ImportReport{}, fmt.Errorf("lock tracking outbox preferences: %w", err)
		}
	}
	trackingReport, accounts, err := importTrackingPreferences(ctx, tx, profileID, document)
	if err != nil {
		return ImportReport{}, err
	}
	report.TrackingAccounts = accounts
	report.Sections = append(report.Sections, trackingReport)
	if err := tx.Commit(ctx); err != nil {
		return ImportReport{}, fmt.Errorf("commit portable archive import: %w", err)
	}
	return report, nil
}
func importIdentity(ctx context.Context, tx pgx.Tx, profileID string, identity Identity) (bool, bool, error) {
	result, err := tx.Exec(ctx, `UPDATE profiles SET name=$2,description=$3,is_child=$4,updated_at=now() WHERE id=$1::uuid AND (name,description,is_child) IS DISTINCT FROM ($2,$3,$4)`, profileID, identity.Name, identity.Description, identity.IsChild)
	if err != nil {
		return false, false, fmt.Errorf("import profile identity: %w", err)
	}
	identityChanged, avatarChanged := result.RowsAffected() == 1, false
	if identity.Avatar.Kind == "preset" {
		result, err = tx.Exec(ctx, `UPDATE profiles SET avatar_preset=$2,updated_at=now() WHERE id=$1::uuid AND avatar_preset IS DISTINCT FROM $2`, profileID, identity.Avatar.PresetID)
		if err != nil {
			return false, false, fmt.Errorf("import profile avatar preset: %w", err)
		}
		avatarChanged = result.RowsAffected() == 1
		result, err = tx.Exec(ctx, `DELETE FROM profile_avatar_images WHERE profile_id=$1::uuid`, profileID)
		if err != nil {
			return false, false, fmt.Errorf("remove imported custom avatar: %w", err)
		}
		avatarChanged = avatarChanged || result.RowsAffected() == 1
	} else {
		normalized, normalizeErr := profile.NormalizeAvatarImage(identity.Avatar.Data)
		if normalizeErr != nil {
			return false, false, ErrInvalidDocument
		}
		result, err = tx.Exec(ctx, `INSERT INTO profile_avatar_images (profile_id,content_type,image_data,updated_at) VALUES ($1::uuid,'image/png',$2,now()) ON CONFLICT (profile_id) DO UPDATE SET content_type='image/png',image_data=EXCLUDED.image_data,updated_at=now() WHERE profile_avatar_images.image_data IS DISTINCT FROM EXCLUDED.image_data`, profileID, normalized)
		if err != nil {
			return false, false, fmt.Errorf("import profile avatar image: %w", err)
		}
		avatarChanged = result.RowsAffected() == 1
	}
	return identityChanged, avatarChanged, nil
}

func importContinueDismissals(ctx context.Context, tx pgx.Tx, profileID string, document Document, titleIDs map[string]string) (SectionReport, error) {
	created, changed := 0, 0
	for _, value := range document.ContinueDismissals {
		var prior *time.Time
		if err := tx.QueryRow(ctx, `SELECT dismissed_at FROM profile_continue_dismissals WHERE profile_id=$1::uuid AND title_id=$2::uuid`, profileID, titleIDs[value.TitleKey]).Scan(&prior); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return SectionReport{}, fmt.Errorf("read continue dismissal: %w", err)
		}
		if prior == nil {
			created++
		} else if prior.Before(value.DismissedAt) {
			changed++
		}
		if _, err := tx.Exec(ctx, `INSERT INTO profile_continue_dismissals (profile_id,title_id,dismissed_at) VALUES ($1::uuid,$2::uuid,$3) ON CONFLICT (profile_id,title_id) DO UPDATE SET dismissed_at=GREATEST(profile_continue_dismissals.dismissed_at,EXCLUDED.dismissed_at)`, profileID, titleIDs[value.TitleKey], value.DismissedAt); err != nil {
			return SectionReport{}, fmt.Errorf("import continue dismissal: %w", err)
		}
	}
	return counts("continueDismissals", len(document.ContinueDismissals), created, changed), nil
}

func lockImportResources(ctx context.Context, tx pgx.Tx, profileID string, document Document) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "profile-title-identities:"+profileID); err != nil {
		return fmt.Errorf("lock portable archive profile identities: %w", err)
	}
	keys := make([]string, 0)
	seen := make(map[string]struct{})
	for _, title := range document.Titles {
		for _, identity := range title.ExternalIDs {
			key := identity.Provider + ":" + identity.Namespace + ":" + identity.ExternalID
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return fmt.Errorf("lock portable archive title identity: %w", err)
		}
	}
	return nil
}

func section(name string, total int, changed bool) SectionReport {
	value := SectionReport{Section: name}
	if changed {
		value.Updated = total
	} else {
		value.Unchanged = total
	}
	return value
}
func counts(name string, total, created, changed int) SectionReport {
	return SectionReport{Section: name, Created: created, Updated: changed, Unchanged: total - created - changed}
}

func validateTargetSettings(ctx context.Context, tx pgx.Tx, document Document) error {
	var castLimit, directLimit int
	if err := tx.QueryRow(ctx, `SELECT COALESCE((settings->>'maximumCastMembers')::int,20),COALESCE((settings->>'maximumDirectTitles')::int,20) FROM instance_settings WHERE instance_id=1 FOR SHARE`).Scan(&castLimit, &directLimit); err != nil {
		return fmt.Errorf("validate target profile setting limits: %w", err)
	}
	if document.Settings.MaximumCastMembers != nil && *document.Settings.MaximumCastMembers > castLimit {
		return invalid("maximumCastMembers exceeds the target instance limit")
	}
	if document.Settings.MaximumDirectTitles != nil && *document.Settings.MaximumDirectTitles > directLimit {
		return invalid("maximumDirectTitles exceeds the target instance limit")
	}
	return nil
}
func importSettings(ctx context.Context, tx pgx.Tx, profileID string, document Document) (bool, error) {
	raw, err := json.Marshal(document.Settings)
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE profile_settings SET settings=settings || $2::jsonb,schema_version=1,updated_at=now() WHERE profile_id=$1::uuid AND settings IS DISTINCT FROM settings || $2::jsonb`, profileID, raw)
	if err != nil {
		return false, fmt.Errorf("import profile settings: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func importAddons(ctx context.Context, tx pgx.Tx, profileID string, document Document) (map[string]string, map[string]addon.Manifest, int, int, error) {
	ids := make(map[string]string)
	manifests := make(map[string]addon.Manifest)
	created, changed := 0, 0
	ordered := append([]Addon(nil), document.Addons...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	localIDs := make([]string, 0, len(ordered))
	for _, value := range ordered {
		manifest, raw, _ := addon.ParseManifest(value.Manifest)
		manifests[value.Key] = manifest
		identityProviders := make([]string, 0)
		identityNamespaces := make([]string, 0)
		identityValues := make([]string, 0)
		for _, title := range document.Titles {
			if title.SourceAddonKey != value.Key {
				continue
			}
			for _, identity := range title.ExternalIDs {
				if identity.ProfileScoped {
					continue
				}
				identityProviders = append(identityProviders, identity.Provider)
				identityNamespaces = append(identityNamespaces, identity.Namespace)
				identityValues = append(identityValues, identity.ExternalID)
			}
		}
		var id string
		err := tx.QueryRow(ctx, `SELECT binding.resource_id::text FROM portable_profile_resource_bindings binding JOIN profile_addons value ON value.id=binding.resource_id JOIN addon_profile_access access ON access.addon_id=value.id AND access.profile_id=binding.profile_id WHERE binding.profile_id=$1::uuid AND binding.resource_kind='addon' AND binding.portable_key=$2 FOR UPDATE OF value`, profileID, value.Key).Scan(&id)
		bound := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, 0, 0, fmt.Errorf("resolve imported add-on: %w", err)
		}
		if !bound {
			err = tx.QueryRow(ctx, `
				SELECT value.id::text
				FROM profile_addons value
				LEFT JOIN portable_profile_resource_bindings binding ON binding.profile_id=$1::uuid AND binding.resource_kind='addon' AND binding.resource_id=value.id
				WHERE binding.resource_id IS NULL
				  AND (value.transport_url,value.manifest,value.manifest_id,value.manifest_version,value.enabled)
				      IS NOT DISTINCT FROM ($2,$3::jsonb,$4,$5,$6)
				ORDER BY EXISTS (
					SELECT 1
					FROM titles title
					JOIN title_external_ids identity ON identity.title_id=title.id
					JOIN unnest($7::text[],$8::text[],$9::text[]) AS imported(provider,namespace,external_id)
					  ON (identity.provider,identity.namespace,identity.external_id)=(imported.provider,imported.namespace,imported.external_id)
					WHERE title.source_addon_id=value.id
				) DESC,
				EXISTS (SELECT 1 FROM titles title WHERE title.source_addon_id=value.id AND EXISTS (SELECT 1 FROM title_external_ids identity WHERE identity.title_id=title.id)) DESC,
				value.id
				LIMIT 1 FOR UPDATE OF value
			`, profileID, value.TransportURL, raw, manifest.ID, manifest.Version, value.Enabled, identityProviders, identityNamespaces, identityValues).Scan(&id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, 0, 0, fmt.Errorf("find exact imported add-on: %w", err)
			}
			if errors.Is(err, pgx.ErrNoRows) {
				err = tx.QueryRow(ctx, `
					SELECT value.id::text
					FROM profile_addons value
					JOIN addon_profile_access access ON access.addon_id=value.id AND access.profile_id=$1::uuid
					LEFT JOIN portable_profile_resource_bindings binding ON binding.profile_id=$1::uuid AND binding.resource_kind='addon' AND binding.resource_id=value.id
					WHERE value.transport_url=$2 AND value.manifest_id=$3 AND binding.resource_id IS NULL
					  AND NOT EXISTS (SELECT 1 FROM addon_profile_access shared WHERE shared.addon_id=value.id AND shared.profile_id<>$1::uuid)
					  AND NOT EXISTS (SELECT 1 FROM addon_category_access shared WHERE shared.addon_id=value.id)
					ORDER BY value.id LIMIT 1 FOR UPDATE OF value
				`, profileID, value.TransportURL, manifest.ID).Scan(&id)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return nil, nil, 0, 0, fmt.Errorf("find adoptable imported add-on: %w", err)
				}
				if errors.Is(err, pgx.ErrNoRows) {
					if err = tx.QueryRow(ctx, `INSERT INTO profile_addons(profile_id,transport_url,manifest,manifest_id,manifest_version,position,enabled) VALUES($1::uuid,$2,$3::jsonb,$4,$5,(SELECT COALESCE(max(position)+1,0) FROM profile_addons WHERE profile_id=$1::uuid),$6) RETURNING id::text`, profileID, value.TransportURL, raw, manifest.ID, manifest.Version, value.Enabled).Scan(&id); err != nil {
						return nil, nil, 0, 0, fmt.Errorf("create imported add-on: %w", err)
					}
					created++
				} else {
					tag, updateErr := updateImportedAddon(ctx, tx, id, value, manifest, raw, profileID)
					if updateErr != nil {
						return nil, nil, 0, 0, updateErr
					}
					if tag.RowsAffected() > 0 {
						changed++
					}
				}
			}
		} else {
			tag, updateErr := updateImportedAddon(ctx, tx, id, value, manifest, raw, profileID)
			if updateErr != nil {
				return nil, nil, 0, 0, updateErr
			}
			if tag.RowsAffected() > 0 {
				changed++
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO addon_profile_access(addon_id,profile_id,position) VALUES($1::uuid,$2::uuid,$3) ON CONFLICT(addon_id,profile_id) DO UPDATE SET position=EXCLUDED.position`, id, profileID, value.Position); err != nil {
			return nil, nil, 0, 0, fmt.Errorf("assign imported add-on: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO addon_profile_order(addon_id,profile_id,position) VALUES($1::uuid,$2::uuid,$3) ON CONFLICT(addon_id,profile_id) DO UPDATE SET position=EXCLUDED.position`, id, profileID, value.Position); err != nil {
			return nil, nil, 0, 0, fmt.Errorf("order imported add-on: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO portable_profile_resource_bindings(profile_id,resource_kind,portable_key,resource_id) VALUES($1::uuid,'addon',$2,$3::uuid) ON CONFLICT(profile_id,resource_kind,portable_key) DO UPDATE SET resource_id=EXCLUDED.resource_id,updated_at=now()`, profileID, value.Key, id); err != nil {
			return nil, nil, 0, 0, fmt.Errorf("bind imported add-on: %w", err)
		}
		ids[value.Key] = id
		localIDs = append(localIDs, id)
	}
	if err := normalizeResourceOrder(ctx, tx, "addon", profileID, localIDs); err != nil {
		return nil, nil, 0, 0, err
	}
	return ids, manifests, created, changed, nil
}

func updateImportedAddon(ctx context.Context, tx pgx.Tx, id string, value Addon, manifest addon.Manifest, raw []byte, profileID string) (pgconn.CommandTag, error) {
	var shared bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM addon_profile_access access WHERE access.addon_id=$1::uuid AND access.profile_id<>$2::uuid) OR EXISTS (SELECT 1 FROM addon_category_access access WHERE access.addon_id=$1::uuid)`, id, profileID).Scan(&shared); err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("check imported add-on sharing: %w", err)
	}
	var differs bool
	if err := tx.QueryRow(ctx, `SELECT (transport_url,manifest,manifest_id,manifest_version,enabled) IS DISTINCT FROM ($2,$3::jsonb,$4,$5,$6) FROM profile_addons WHERE id=$1::uuid`, id, value.TransportURL, raw, manifest.ID, manifest.Version, value.Enabled).Scan(&differs); err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("compare imported add-on: %w", err)
	}
	if shared && differs {
		return pgconn.CommandTag{}, fmt.Errorf("%w: imported add-on is shared with another profile or category", ErrConflict)
	}
	tag, err := tx.Exec(ctx, `UPDATE profile_addons SET transport_url=$2,manifest=$3::jsonb,manifest_id=$4,manifest_version=$5,enabled=$6,updated_at=now() WHERE id=$1::uuid AND (transport_url,manifest,manifest_id,manifest_version,enabled) IS DISTINCT FROM ($2,$3::jsonb,$4,$5,$6)`, id, value.TransportURL, raw, manifest.ID, manifest.Version, value.Enabled)
	if err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("update imported add-on: %w", err)
	}
	return tag, nil
}

func normalizeResourceOrder(ctx context.Context, tx pgx.Tx, kind, profileID string, preferred []string) error {
	var idColumn string
	var tables []string
	if kind == "addon" {
		idColumn = "addon_id"
		tables = []string{"addon_profile_access", "addon_profile_order"}
	} else {
		idColumn = "collection_id"
		tables = []string{"collection_profile_access", "collection_profile_order"}
	}
	for _, table := range tables {
		query := fmt.Sprintf(`
			WITH ranked AS (
				SELECT %s AS id,
				       row_number() OVER (
				           ORDER BY COALESCE(array_position($2::uuid[], %s), 2147483647), position, %s
				       ) - 1 AS next_position
				FROM %s
				WHERE profile_id = $1::uuid
			)
			UPDATE %s target
			SET position = ranked.next_position
			FROM ranked
			WHERE target.profile_id = $1::uuid AND target.%s = ranked.id
		`, idColumn, idColumn, idColumn, table, table, idColumn)
		if _, err := tx.Exec(ctx, query, profileID, preferred); err != nil {
			return fmt.Errorf("normalize imported %s order in %s: %w", kind, table, err)
		}
	}
	return nil
}

func importCollections(ctx context.Context, tx pgx.Tx, profileID string, document Document, addonIDs map[string]string, manifests map[string]addon.Manifest) (int, int, error) {
	created, changed := 0, 0
	localIDs := make([]string, 0, len(document.Collections))
	for _, portable := range document.Collections {
		encodedInput, err := json.Marshal(portable.Value)
		if err != nil {
			return 0, 0, fmt.Errorf("encode imported collection: %w", err)
		}
		var input collection.SaveInput
		if err := json.Unmarshal(encodedInput, &input); err != nil {
			return 0, 0, fmt.Errorf("clone imported collection: %w", err)
		}
		for fi := range input.Folders {
			input.Folders[fi].ID = deterministicUUID(portable.Key, fmt.Sprintf("folder:%d", fi))
			for si := range input.Folders[fi].Sources {
				source := &input.Folders[fi].Sources[si]
				source.ID = deterministicUUID(portable.Key, fmt.Sprintf("source:%d:%d", fi, si))
				if source.AddonCatalog != nil {
					archiveReference := source.AddonCatalog.AddonID
					key := ""
					for candidate := range addonIDs {
						if deterministicUUID(candidate, "archive-addon-ref") == archiveReference {
							key = candidate
							break
						}
					}
					id, ok := addonIDs[key]
					if !ok {
						return 0, 0, invalid("collection add-on binding is missing")
					}
					source.AddonCatalog.AddonID = id
					source.AddonCatalog.ManifestID = manifests[key].ID
				}
			}
		}
		normalized, err := collection.NormalizePortable(input)
		if err != nil {
			return 0, 0, fmt.Errorf("%w: collection %q: %v", ErrInvalidDocument, input.Title, err)
		}
		folders, err := json.Marshal(normalized.Folders)
		if err != nil {
			return 0, 0, err
		}
		var id string
		adopted := false
		err = tx.QueryRow(ctx, `SELECT binding.resource_id::text FROM portable_profile_resource_bindings binding JOIN profile_collections value ON value.id=binding.resource_id JOIN collection_profile_access access ON access.collection_id=value.id AND access.profile_id=binding.profile_id WHERE binding.profile_id=$1::uuid AND binding.resource_kind='collection' AND binding.portable_key=$2 FOR UPDATE OF value`, profileID, portable.Key).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			id, err = adoptImportedCollection(ctx, tx, profileID, normalized)
			adopted = err == nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `INSERT INTO profile_collections(profile_id,title,backdrop_image_url,hero_enabled,pin_to_top,focus_glow_enabled,view_mode,folder_cover_shape,folders,position) VALUES($1::uuid,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9::jsonb,(SELECT COALESCE(max(position)+1,0) FROM profile_collections WHERE profile_id=$1::uuid)) RETURNING id::text`, profileID, normalized.Title, normalized.BackdropImageURL, normalized.HeroEnabled, normalized.PinToTop, normalized.FocusGlowEnabled, normalized.ViewMode, normalized.FolderCoverShape, folders).Scan(&id)
			if err != nil {
				return 0, 0, fmt.Errorf("create imported collection: %w", err)
			}
			created++
		} else if err != nil {
			return 0, 0, fmt.Errorf("resolve imported collection: %w", err)
		} else if !adopted {
			tag, err := updateImportedCollection(ctx, tx, id, normalized, folders, profileID)
			if err != nil {
				return 0, 0, err
			}
			if tag.RowsAffected() > 0 {
				changed++
			}
		}
		position := len(localIDs)
		if _, err := tx.Exec(ctx, `INSERT INTO collection_profile_access(collection_id,profile_id,position) VALUES($1::uuid,$2::uuid,$3) ON CONFLICT(collection_id,profile_id) DO UPDATE SET position=EXCLUDED.position`, id, profileID, position); err != nil {
			return 0, 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO collection_profile_order(collection_id,profile_id,position) VALUES($1::uuid,$2::uuid,$3) ON CONFLICT(collection_id,profile_id) DO UPDATE SET position=EXCLUDED.position`, id, profileID, position); err != nil {
			return 0, 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO portable_profile_resource_bindings(profile_id,resource_kind,portable_key,resource_id) VALUES($1::uuid,'collection',$2,$3::uuid) ON CONFLICT(profile_id,resource_kind,portable_key) DO UPDATE SET resource_id=EXCLUDED.resource_id,updated_at=now()`, profileID, portable.Key, id); err != nil {
			return 0, 0, err
		}
		localIDs = append(localIDs, id)
	}
	if err := normalizeResourceOrder(ctx, tx, "collection", profileID, localIDs); err != nil {
		return 0, 0, err
	}
	return created, changed, nil
}
func adoptImportedCollection(ctx context.Context, tx pgx.Tx, profileID string, normalized collection.SaveInput) (string, error) {
	wanted := normalized
	clearCollectionRuntimeIDs(&wanted)
	wantedFolders, err := json.Marshal(wanted.Folders)
	if err != nil {
		return "", err
	}
	rows, err := tx.Query(ctx, `
		SELECT value.id::text,value.folders
		FROM profile_collections value
		JOIN collection_profile_access access
		  ON access.collection_id=value.id AND access.profile_id=$1::uuid
		LEFT JOIN portable_profile_resource_bindings binding
		  ON binding.profile_id=access.profile_id
		 AND binding.resource_kind='collection'
		 AND binding.resource_id=value.id
		WHERE binding.resource_id IS NULL
		  AND (value.title,COALESCE(value.backdrop_image_url,''),value.hero_enabled,value.pin_to_top,value.focus_glow_enabled,value.view_mode,value.folder_cover_shape)
		      IS NOT DISTINCT FROM ($2,$3,$4,$5,$6,$7,$8)
		ORDER BY value.id
		FOR UPDATE OF value
	`, profileID, normalized.Title, normalized.BackdropImageURL, normalized.HeroEnabled, normalized.PinToTop, normalized.FocusGlowEnabled, normalized.ViewMode, normalized.FolderCoverShape)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var rawFolders []byte
		if err := rows.Scan(&id, &rawFolders); err != nil {
			return "", err
		}
		candidate := collection.SaveInput{Folders: []collection.Folder{}}
		if err := json.Unmarshal(rawFolders, &candidate.Folders); err != nil {
			return "", fmt.Errorf("decode adoptable collection: %w", err)
		}
		clearCollectionRuntimeIDs(&candidate)
		candidateFolders, err := json.Marshal(candidate.Folders)
		if err != nil {
			return "", err
		}
		if bytes.Equal(candidateFolders, wantedFolders) {
			return id, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", pgx.ErrNoRows
}

func clearCollectionRuntimeIDs(value *collection.SaveInput) {
	for folderIndex := range value.Folders {
		value.Folders[folderIndex].ID = ""
		for sourceIndex := range value.Folders[folderIndex].Sources {
			source := &value.Folders[folderIndex].Sources[sourceIndex]
			source.ID = ""
			if source.AddonCatalog != nil {
				source.AddonCatalog.ManifestID = ""
			}
		}
	}
}

func updateImportedCollection(ctx context.Context, tx pgx.Tx, id string, normalized collection.SaveInput, folders []byte, profileID string) (pgconn.CommandTag, error) {
	var shared, scalarDiffers bool
	var currentFolders []byte
	if err := tx.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM collection_profile_access access WHERE access.collection_id=$1::uuid AND access.profile_id<>$2::uuid)
			OR EXISTS (SELECT 1 FROM collection_category_access access WHERE access.collection_id=$1::uuid),
			(title,COALESCE(backdrop_image_url,''),hero_enabled,pin_to_top,focus_glow_enabled,view_mode,folder_cover_shape)
				IS DISTINCT FROM ($3,$4,$5,$6,$7,$8,$9),
			folders
		FROM profile_collections WHERE id=$1::uuid
	`, id, profileID, normalized.Title, normalized.BackdropImageURL, normalized.HeroEnabled, normalized.PinToTop, normalized.FocusGlowEnabled, normalized.ViewMode, normalized.FolderCoverShape).Scan(&shared, &scalarDiffers, &currentFolders); err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("check imported collection sharing: %w", err)
	}
	current := collection.SaveInput{Folders: []collection.Folder{}}
	if err := json.Unmarshal(currentFolders, &current.Folders); err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("decode current imported collection: %w", err)
	}
	wanted := normalized
	clearCollectionRuntimeIDs(&current)
	clearCollectionRuntimeIDs(&wanted)
	currentSemanticFolders, err := json.Marshal(current.Folders)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	wantedSemanticFolders, err := json.Marshal(wanted.Folders)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	differs := scalarDiffers || !bytes.Equal(currentSemanticFolders, wantedSemanticFolders)
	if shared && differs {
		return pgconn.CommandTag{}, fmt.Errorf("%w: imported collection is shared with another profile or category", ErrConflict)
	}
	if !differs {
		return pgconn.CommandTag{}, nil
	}
	tag, err := tx.Exec(ctx, `UPDATE profile_collections SET title=$2,backdrop_image_url=NULLIF($3,''),hero_enabled=$4,pin_to_top=$5,focus_glow_enabled=$6,view_mode=$7,folder_cover_shape=$8,folders=$9::jsonb,version=version+1,updated_at=now() WHERE id=$1::uuid`, id, normalized.Title, normalized.BackdropImageURL, normalized.HeroEnabled, normalized.PinToTop, normalized.FocusGlowEnabled, normalized.ViewMode, normalized.FolderCoverShape, folders)
	if err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("update imported collection: %w", err)
	}
	return tag, nil
}

func deterministicUUID(key, scope string) string {
	sum := sha256.Sum256([]byte(key + "\x00" + scope))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(sum[:16])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func importTitles(ctx context.Context, tx pgx.Tx, profileID string, document Document, addonIDs map[string]string) (map[string]string, int, int, error) {
	ordered := append([]Title(nil), document.Titles...)
	sort.SliceStable(ordered, func(i, j int) bool { return titleRank(ordered[i].MediaType) < titleRank(ordered[j].MediaType) })
	ids := make(map[string]string)
	created, changed := 0, 0
	for _, value := range ordered {
		var boundID string
		err := tx.QueryRow(ctx, `SELECT resource_id::text FROM portable_profile_resource_bindings WHERE profile_id=$1::uuid AND resource_kind='title' AND portable_key=$2 FOR UPDATE`, profileID, value.Key).Scan(&boundID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, 0, err
		}
		candidates := make(map[string]struct{})
		if boundID != "" {
			candidates[boundID] = struct{}{}
		}
		for _, identity := range value.ExternalIDs {
			var candidate string
			if identity.ProfileScoped {
				err = tx.QueryRow(ctx, `SELECT title_id::text FROM profile_title_external_ids WHERE profile_id=$1::uuid AND provider=$2 AND namespace=$3 AND external_id=$4`, profileID, identity.Provider, identity.Namespace, identity.ExternalID).Scan(&candidate)
			} else {
				err = tx.QueryRow(ctx, `SELECT title_id::text FROM title_external_ids WHERE provider=$1 AND namespace=$2 AND external_id=$3`, identity.Provider, identity.Namespace, identity.ExternalID).Scan(&candidate)
			}
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, 0, 0, err
			}
			if candidate != "" {
				candidates[candidate] = struct{}{}
			}
		}
		if len(candidates) > 1 {
			return nil, 0, 0, fmt.Errorf("%w: title identities resolve to different target titles", ErrConflict)
		}
		id := ""
		for candidate := range candidates {
			id = candidate
		}
		parentID := ""
		if value.ParentKey != "" {
			parentID = ids[value.ParentKey]
			if parentID == "" {
				return nil, 0, 0, invalid("title parent was not imported")
			}
		}
		sourceAddonID := ""
		if value.SourceAddonKey != "" {
			sourceAddonID = addonIDs[value.SourceAddonKey]
		}
		createdTitle := id == ""
		if createdTitle {
			err = tx.QueryRow(ctx, `INSERT INTO titles(media_type,parent_id,ordinal,display_title,poster_url,background_url,release_info,release_date,resource_id,resource_provider,source_addon_id,source_catalog_id,source_name,country,language,category) VALUES($1,NULLIF($2,'')::uuid,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,'')::date,NULLIF($9,''),NULLIF($10,''),NULLIF($11,'')::uuid,NULLIF($12,''),NULLIF($13,''),NULLIF($14,''),NULLIF($15,''),NULLIF($16,'')) RETURNING id::text`, value.MediaType, parentID, value.Ordinal, value.DisplayTitle, value.PosterURL, value.BackgroundURL, value.ReleaseInfo, value.ReleaseDate, value.ResourceID, value.ResourceProvider, sourceAddonID, value.SourceCatalogID, value.SourceName, value.Country, value.Language, value.Category).Scan(&id)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("create imported title: %w", err)
			}
			created++
		} else {
			var currentSourceAddonID string
			var existingScopedProfile, existingScopedProvider, existingScopedNamespace, existingScopedID *string
			if err := tx.QueryRow(ctx, `SELECT COALESCE(title.source_addon_id::text,''),identity.profile_id::text,identity.provider,identity.namespace,identity.external_id FROM titles title LEFT JOIN profile_title_external_ids identity ON identity.title_id=title.id WHERE title.id=$1::uuid FOR UPDATE OF title`, id).Scan(&currentSourceAddonID, &existingScopedProfile, &existingScopedProvider, &existingScopedNamespace, &existingScopedID); err != nil {
				return nil, 0, 0, fmt.Errorf("check imported title provenance: %w", err)
			}
			if currentSourceAddonID != sourceAddonID {
				return nil, 0, 0, fmt.Errorf("%w: imported title add-on provenance differs from the resolved title", ErrConflict)
			}
			if existingScopedProfile != nil && *existingScopedProfile != profileID {
				return nil, 0, 0, fmt.Errorf("%w: resolved title is scoped to another profile", ErrConflict)
			}
			if existingScopedProvider != nil {
				for _, identity := range value.ExternalIDs {
					if identity.ProfileScoped && (*existingScopedProvider != identity.Provider || *existingScopedNamespace != identity.Namespace || *existingScopedID != identity.ExternalID) {
						return nil, 0, 0, fmt.Errorf("%w: imported scoped title identity differs from the resolved title", ErrConflict)
					}
				}
			}
		}
		if createdTitle {
			for _, identity := range value.ExternalIDs {
				if identity.ProfileScoped {
					_, err = tx.Exec(ctx, `INSERT INTO profile_title_external_ids(profile_id,title_id,provider,namespace,external_id) VALUES($1::uuid,$2::uuid,$3,$4,$5)`, profileID, id, identity.Provider, identity.Namespace, identity.ExternalID)
				} else {
					_, err = tx.Exec(ctx, `INSERT INTO title_external_ids(title_id,provider,namespace,external_id) VALUES($1::uuid,$2,$3,$4)`, id, identity.Provider, identity.Namespace, identity.ExternalID)
				}
				if err != nil {
					return nil, 0, 0, fmt.Errorf("bind imported title identity: %w", err)
				}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO portable_profile_resource_bindings(profile_id,resource_kind,portable_key,resource_id) VALUES($1::uuid,'title',$2,$3::uuid) ON CONFLICT(profile_id,resource_kind,portable_key) DO UPDATE SET resource_id=EXCLUDED.resource_id,updated_at=now()`, profileID, value.Key, id); err != nil {
			return nil, 0, 0, err
		}
		ids[value.Key] = id
	}
	return ids, created, changed, nil
}
func titleRank(mediaType string) int {
	switch mediaType {
	case "movie", "series", "tv":
		return 0
	case "season":
		return 1
	default:
		return 2
	}
}

func importStates(ctx context.Context, tx pgx.Tx, profileID string, document Document, titleIDs map[string]string) ([]SectionReport, error) {
	reports := []SectionReport{{Section: "library"}, {Section: "progress"}, {Section: "favorites"}, {Section: "userData"}}
	for _, v := range document.Library {
		created, changed, err := stateMutation(ctx, tx, `
			WITH existing AS MATERIALIZED (SELECT added_at,updated_at FROM profile_library WHERE profile_id=$1::uuid AND title_id=$2::uuid),
			upsert AS (INSERT INTO profile_library(profile_id,title_id,added_at,updated_at) VALUES($1::uuid,$2::uuid,$3,$4)
			ON CONFLICT(profile_id,title_id) DO UPDATE SET added_at=LEAST(profile_library.added_at,EXCLUDED.added_at),updated_at=GREATEST(profile_library.updated_at,EXCLUDED.updated_at)
			WHERE (profile_library.added_at,profile_library.updated_at) IS DISTINCT FROM (LEAST(profile_library.added_at,EXCLUDED.added_at),GREATEST(profile_library.updated_at,EXCLUDED.updated_at)) RETURNING 1)
			SELECT NOT EXISTS(SELECT 1 FROM existing),EXISTS(SELECT 1 FROM upsert)`, profileID, titleIDs[v.TitleKey], v.AddedAt, v.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("import library: %w", err)
		}
		addStateCount(&reports[0], created, changed)
	}
	for _, v := range document.Progress {
		created, changed, err := stateMutation(ctx, tx, `
			WITH existing AS MATERIALIZED (SELECT 1 FROM profile_progress WHERE profile_id=$1::uuid AND title_id=$2::uuid),
			upsert AS (INSERT INTO profile_progress(profile_id,title_id,position_seconds,duration_seconds,completed,version,last_watched_at,updated_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8)
			ON CONFLICT(profile_id,title_id) DO UPDATE SET
			position_seconds=CASE WHEN (EXCLUDED.last_watched_at,EXCLUDED.updated_at) > (profile_progress.last_watched_at,profile_progress.updated_at) THEN EXCLUDED.position_seconds ELSE profile_progress.position_seconds END,
			duration_seconds=CASE WHEN (EXCLUDED.last_watched_at,EXCLUDED.updated_at) > (profile_progress.last_watched_at,profile_progress.updated_at) THEN EXCLUDED.duration_seconds ELSE profile_progress.duration_seconds END,
			completed=CASE WHEN (EXCLUDED.last_watched_at,EXCLUDED.updated_at) > (profile_progress.last_watched_at,profile_progress.updated_at) THEN EXCLUDED.completed ELSE profile_progress.completed END,
			version=GREATEST(profile_progress.version,EXCLUDED.version),
			last_watched_at=CASE WHEN (EXCLUDED.last_watched_at,EXCLUDED.updated_at) > (profile_progress.last_watched_at,profile_progress.updated_at) THEN EXCLUDED.last_watched_at ELSE profile_progress.last_watched_at END,
			updated_at=CASE WHEN (EXCLUDED.last_watched_at,EXCLUDED.updated_at) > (profile_progress.last_watched_at,profile_progress.updated_at) THEN EXCLUDED.updated_at ELSE profile_progress.updated_at END
			WHERE (EXCLUDED.last_watched_at,EXCLUDED.updated_at) > (profile_progress.last_watched_at,profile_progress.updated_at) OR EXCLUDED.version > profile_progress.version RETURNING 1)
			SELECT NOT EXISTS(SELECT 1 FROM existing),EXISTS(SELECT 1 FROM upsert)`, profileID, titleIDs[v.TitleKey], v.PositionSeconds, v.DurationSeconds, v.Completed, v.Version, v.LastWatchedAt, v.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("import progress: %w", err)
		}
		addStateCount(&reports[1], created, changed)
	}
	for _, v := range document.Favorites {
		created, changed, err := stateMutation(ctx, tx, `
			WITH existing AS MATERIALIZED (SELECT created_at,updated_at FROM profile_favorites WHERE profile_id=$1::uuid AND title_id=$2::uuid),
			upsert AS (INSERT INTO profile_favorites(profile_id,title_id,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3,$4)
			ON CONFLICT(profile_id,title_id) DO UPDATE SET created_at=LEAST(profile_favorites.created_at,EXCLUDED.created_at),updated_at=GREATEST(profile_favorites.updated_at,EXCLUDED.updated_at)
			WHERE (profile_favorites.created_at,profile_favorites.updated_at) IS DISTINCT FROM (LEAST(profile_favorites.created_at,EXCLUDED.created_at),GREATEST(profile_favorites.updated_at,EXCLUDED.updated_at)) RETURNING 1)
			SELECT NOT EXISTS(SELECT 1 FROM existing),EXISTS(SELECT 1 FROM upsert)`, profileID, titleIDs[v.TitleKey], v.CreatedAt, v.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("import favorites: %w", err)
		}
		addStateCount(&reports[2], created, changed)
	}
	for _, v := range document.UserData {
		created, changed, err := stateMutation(ctx, tx, `
			WITH existing AS MATERIALIZED (SELECT 1 FROM profile_user_data WHERE profile_id=$1::uuid AND title_id=$2::uuid),
			upsert AS (INSERT INTO profile_user_data(profile_id,title_id,rating,rating_set,played_percentage,played_percentage_set,unplayed_item_count,unplayed_item_count_set,play_count,play_count_set,likes,likes_set,last_played_date,last_played_date_submicrosecond,last_played_date_set,updated_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			ON CONFLICT(profile_id,title_id) DO UPDATE SET rating=EXCLUDED.rating,rating_set=EXCLUDED.rating_set,played_percentage=EXCLUDED.played_percentage,played_percentage_set=EXCLUDED.played_percentage_set,unplayed_item_count=EXCLUDED.unplayed_item_count,unplayed_item_count_set=EXCLUDED.unplayed_item_count_set,play_count=EXCLUDED.play_count,play_count_set=EXCLUDED.play_count_set,likes=EXCLUDED.likes,likes_set=EXCLUDED.likes_set,last_played_date=EXCLUDED.last_played_date,last_played_date_submicrosecond=EXCLUDED.last_played_date_submicrosecond,last_played_date_set=EXCLUDED.last_played_date_set,updated_at=EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > profile_user_data.updated_at RETURNING 1)
			SELECT NOT EXISTS(SELECT 1 FROM existing),EXISTS(SELECT 1 FROM upsert)`, profileID, titleIDs[v.TitleKey], v.Rating, v.RatingSet, v.PlayedPercentage, v.PlayedPercentageSet, v.UnplayedItemCount, v.UnplayedItemCountSet, v.PlayCount, v.PlayCountSet, v.Likes, v.LikesSet, v.LastPlayedDate, v.LastPlayedDateSubmicrosecond, v.LastPlayedDateSet, v.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("import user data: %w", err)
		}
		addStateCount(&reports[3], created, changed)
	}
	return reports, nil
}

func stateMutation(ctx context.Context, tx pgx.Tx, query string, args ...any) (bool, bool, error) {
	var created, affected bool
	if err := tx.QueryRow(ctx, query, args...).Scan(&created, &affected); err != nil {
		return false, false, err
	}
	return created, !created && affected, nil
}

func addStateCount(report *SectionReport, created, changed bool) {
	if created {
		report.Created++
	} else if changed {
		report.Updated++
	} else {
		report.Unchanged++
	}
}

func importTrackingPreferences(ctx context.Context, tx pgx.Tx, profileID string, document Document) (SectionReport, int, error) {
	report := SectionReport{Section: "trackingPreferences"}
	accounts := 0
	for _, value := range document.TrackingPreferences {
		created, changed, err := stateMutation(ctx, tx, `WITH existing AS MATERIALIZED (SELECT 1 FROM profile_tracking_preferences WHERE profile_id=$1::uuid AND provider=$2), upsert AS (INSERT INTO profile_tracking_preferences(profile_id,provider,sync_watched,sync_progress,sync_library) VALUES($1::uuid,$2,$3,$4,$5) ON CONFLICT(profile_id,provider) DO UPDATE SET sync_watched=EXCLUDED.sync_watched,sync_progress=EXCLUDED.sync_progress,sync_library=EXCLUDED.sync_library,updated_at=now() WHERE (profile_tracking_preferences.sync_watched,profile_tracking_preferences.sync_progress,profile_tracking_preferences.sync_library) IS DISTINCT FROM (EXCLUDED.sync_watched,EXCLUDED.sync_progress,EXCLUDED.sync_library) RETURNING 1) SELECT NOT EXISTS(SELECT 1 FROM existing),EXISTS(SELECT 1 FROM upsert)`, profileID, value.Provider, value.SyncWatched, value.SyncProgress, value.SyncLibrary)
		if err != nil {
			return SectionReport{}, 0, fmt.Errorf("import tracking preference: %w", err)
		}
		addStateCount(&report, created, changed)
		tag, err := tx.Exec(ctx, `UPDATE profile_tracking_accounts SET sync_watched=$3,sync_progress=$4,sync_library=$5,updated_at=now() WHERE profile_id=$1::uuid AND provider=$2 AND (sync_watched,sync_progress,sync_library) IS DISTINCT FROM ($3,$4,$5)`, profileID, value.Provider, value.SyncWatched, value.SyncProgress, value.SyncLibrary)
		if err != nil {
			return SectionReport{}, 0, fmt.Errorf("apply imported tracking preference: %w", err)
		}
		accounts += int(tag.RowsAffected())
		disabled := make([]string, 0, 3)
		if !value.SyncWatched {
			disabled = append(disabled, "watched")
		}
		if !value.SyncProgress {
			disabled = append(disabled, "progress")
		}
		if !value.SyncLibrary {
			disabled = append(disabled, "library")
		}
		if len(disabled) > 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM profile_tracking_outbox WHERE profile_id=$1::uuid AND provider=$2 AND event_type=ANY($3)`, profileID, value.Provider, disabled); err != nil {
				return SectionReport{}, 0, fmt.Errorf("clear disabled tracking work: %w", err)
			}
		}
		if value.SyncWatched || value.SyncProgress || value.SyncLibrary {
			if err := seedImportedTrackingState(ctx, tx, profileID, value.Provider); err != nil {
				return SectionReport{}, 0, err
			}
		}
	}
	return report, accounts, nil
}

const importedTrackingSeedsSQL = `
	SELECT account.profile_id,account.provider,library.title_id,'library'::text AS event_type,
	       jsonb_build_object('inLibrary',true,'occurredAt',library.updated_at) AS payload,
	       'connect:library:' || library.title_id::text || ':' || (extract(epoch FROM library.updated_at) * 1000000000)::bigint::text AS idempotency_key,
	       false AS affects_watched
	FROM profile_tracking_accounts account
	JOIN profile_library library ON library.profile_id=account.profile_id
	WHERE account.profile_id=$1::uuid AND account.provider=$2 AND account.sync_library
	UNION ALL
	SELECT account.profile_id,account.provider,progress.title_id,'watched'::text,
	       jsonb_build_object('version',progress.version,'occurredAt',progress.updated_at)
	         || CASE WHEN progress.completed THEN jsonb_build_object('completed',true) ELSE '{}'::jsonb END,
	       'connect:watched:' || progress.title_id::text || ':' || progress.version::text,
	       true
	FROM profile_tracking_accounts account
	JOIN profile_progress progress ON progress.profile_id=account.profile_id
	WHERE account.profile_id=$1::uuid AND account.provider=$2 AND account.sync_watched
	UNION ALL
	SELECT account.profile_id,account.provider,progress.title_id,'progress'::text,
	       jsonb_build_object('positionSeconds',progress.position_seconds,'durationSeconds',progress.duration_seconds,'version',progress.version,'occurredAt',progress.updated_at),
	       'connect:progress:' || progress.title_id::text || ':' || progress.version::text,
	       false
	FROM profile_tracking_accounts account
	JOIN profile_progress progress ON progress.profile_id=account.profile_id
	WHERE account.profile_id=$1::uuid AND account.provider=$2 AND account.sync_progress
	  AND NOT progress.completed AND progress.position_seconds > 0`

func seedImportedTrackingState(ctx context.Context, tx pgx.Tx, profileID, provider string) error {
	if _, err := tx.Exec(ctx, `WITH seeds AS (`+importedTrackingSeedsSQL+`)
		INSERT INTO profile_tracking_event_heads(profile_id,provider,title_id,event_type,idempotency_key,affects_watched,updated_at)
		SELECT profile_id,provider,title_id,event_type,idempotency_key,affects_watched,clock_timestamp() FROM seeds
		ON CONFLICT(profile_id,provider,title_id,event_type) DO UPDATE
		SET idempotency_key=EXCLUDED.idempotency_key,affects_watched=EXCLUDED.affects_watched,updated_at=clock_timestamp()`, profileID, provider); err != nil {
		return fmt.Errorf("update imported tracking event heads: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM profile_tracking_outbox pending
		WHERE pending.leased_until IS NULL AND NOT EXISTS (
			SELECT 1 FROM profile_tracking_event_heads current
			WHERE current.profile_id=pending.profile_id AND current.provider=pending.provider
			  AND current.title_id=pending.title_id AND current.event_type=pending.event_type
			  AND current.idempotency_key=pending.idempotency_key
			  AND NOT EXISTS (SELECT 1 FROM profile_tracking_event_heads newer
				WHERE newer.profile_id=current.profile_id AND newer.provider=current.provider
				  AND newer.title_id=current.title_id AND newer.updated_at>current.updated_at
				  AND (newer.event_type=current.event_type OR current.affects_watched AND newer.affects_watched)))`); err != nil {
		return fmt.Errorf("remove superseded imported tracking work: %w", err)
	}
	var globalExceeded, profileExceeded bool
	if err := tx.QueryRow(ctx, `WITH seeds AS (`+importedTrackingSeedsSQL+`), missing AS (
		SELECT DISTINCT seed.profile_id,seed.provider,seed.title_id,seed.event_type FROM seeds seed
		WHERE NOT EXISTS (SELECT 1 FROM profile_tracking_outbox pending
			WHERE pending.profile_id=seed.profile_id AND pending.provider=seed.provider
			  AND pending.title_id=seed.title_id AND pending.event_type=seed.event_type AND pending.leased_until IS NULL))
		SELECT (SELECT count(*) FROM profile_tracking_outbox)+(SELECT count(*) FROM missing)>32768,
		       (SELECT count(*) FROM profile_tracking_outbox WHERE profile_id=$1::uuid AND provider=$2)+(SELECT count(*) FROM missing)>4096`, profileID, provider).Scan(&globalExceeded, &profileExceeded); err != nil {
		return fmt.Errorf("check imported tracking outbox capacity: %w", err)
	}
	if globalExceeded || profileExceeded {
		return fmt.Errorf("%w: tracking outbox capacity exceeded", ErrConflict)
	}
	if _, err := tx.Exec(ctx, `WITH seeds AS (`+importedTrackingSeedsSQL+`)
		INSERT INTO profile_tracking_outbox(profile_id,provider,title_id,event_type,payload,idempotency_key)
		SELECT seed.profile_id,seed.provider,seed.title_id,seed.event_type,seed.payload,seed.idempotency_key
		FROM seeds seed JOIN profile_tracking_event_heads head
		  ON head.profile_id=seed.profile_id AND head.provider=seed.provider AND head.title_id=seed.title_id
		 AND head.event_type=seed.event_type AND head.idempotency_key=seed.idempotency_key
		ON CONFLICT(profile_id,provider,title_id,event_type) WHERE leased_until IS NULL DO UPDATE
		SET payload=EXCLUDED.payload,idempotency_key=EXCLUDED.idempotency_key,enqueue_sequence=DEFAULT,
		    attempt_count=0,next_attempt_at=now(),last_error=NULL,created_at=now()`, profileID, provider); err != nil {
		return fmt.Errorf("seed imported tracking work: %w", err)
	}
	return nil
}
