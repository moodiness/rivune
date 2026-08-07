package watchstate

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	// MaximumCatalogPageSize bounds a single read-only catalog page.
	MaximumCatalogPageSize    = 200
	maximumCatalogOffset      = 1_000_000
	maximumCatalogSearchBytes = 256
	maximumCatalogIDs         = 100
)

var catalogMediaTypes = map[string]struct{}{
	"movie":   {},
	"series":  {},
	"season":  {},
	"episode": {},
}

// ParentID is the only hierarchy selector. Recursive expands the selected
// root(s), SearchTerm performs bounded title search in SQL, and IDs narrows the
// result to canonical UUIDs. Offset is zero-based and Limit is bounded. SortBy
// is empty for native catalog order or "sortname" with an explicit direction.
type CatalogQuery struct {
	ParentID   string
	MediaTypes []string
	SearchTerm string
	IDs        []string
	Recursive  bool
	Offset     int
	Limit      int
	SortBy     string
	SortOrder  string
}

// CatalogProgress is the active profile's materialized playback state.
type CatalogProgress struct {
	PositionSeconds int
	DurationSeconds int
	Completed       bool
	LastWatchedAt   *time.Time
}

// CatalogTitle is a read-only canonical title snapshot. ProviderIDs contains
// global identities plus identities belonging to the active profile only.
// Resource and source fields are internal provenance snapshots and must not be
// projected into untrusted protocol responses as URLs, headers, or credentials.
type CatalogTitle struct {
	ID               string            `json:"id"`
	MediaType        string            `json:"mediaType"`
	ParentID         string            `json:"parentId,omitempty"`
	SeriesID         string            `json:"seriesId,omitempty"`
	SeasonID         string            `json:"seasonId,omitempty"`
	Ordinal          *int              `json:"ordinal,omitempty"`
	ParentOrdinal    *int              `json:"parentOrdinal,omitempty"`
	Title            string            `json:"title,omitempty"`
	SeriesTitle      string            `json:"seriesTitle,omitempty"`
	SeasonTitle      string            `json:"seasonTitle,omitempty"`
	PosterURL        string            `json:"posterUrl,omitempty"`
	BackgroundURL    string            `json:"backgroundUrl,omitempty"`
	ReleaseInfo      string            `json:"releaseInfo,omitempty"`
	Released         string            `json:"released,omitempty"`
	Overview         string            `json:"overview,omitempty"`
	RuntimeMinutes   *int              `json:"runtimeMinutes,omitempty"`
	Genres           []string          `json:"genres"`
	CommunityRating  *float32          `json:"communityRating,omitempty"`
	InLibrary        bool              `json:"inLibrary"`
	Progress         *CatalogProgress  `json:"progress,omitempty"`
	ResourceID       string            `json:"resourceId,omitempty"`
	ResourceProvider string            `json:"resourceProvider,omitempty"`
	SourceAddonID    string            `json:"sourceAddonId,omitempty"`
	SourceCatalogID  string            `json:"sourceCatalogId,omitempty"`
	SourceName       string            `json:"sourceName,omitempty"`
	Country          string            `json:"country,omitempty"`
	Language         string            `json:"language,omitempty"`
	Category         string            `json:"category,omitempty"`
	ProviderIDs      map[string]string `json:"providerIds"`
}

// CatalogPage is an exact offset/limit window and its total before pagination.
type CatalogPage struct {
	Items  []CatalogTitle `json:"items"`
	Offset int            `json:"offset"`
	Limit  int            `json:"limit"`
	Total  int            `json:"total"`
}

// GetCatalogTitle returns one accessible canonical UUID without refreshing,
// resolving, or otherwise mutating metadata.
func (s *Service) GetCatalogTitle(ctx context.Context, principal auth.Principal, titleID string) (CatalogTitle, error) {
	items, err := s.GetCatalogTitles(ctx, principal, []string{titleID})
	if err != nil {
		return CatalogTitle{}, err
	}
	if len(items) == 0 {
		return CatalogTitle{}, ErrNotFound
	}
	return items[0], nil
}

// GetCatalogTitles returns a coherent, profile-authorized projection for a
// bounded set of canonical IDs. Results are not ordered; callers restore their
// domain order after indexing by ID.
func (s *Service) GetCatalogTitles(ctx context.Context, principal auth.Principal, titleIDs []string) ([]CatalogTitle, error) {
	if len(titleIDs) == 0 {
		return []CatalogTitle{}, nil
	}
	if len(titleIDs) > maximumCatalogIDs {
		return nil, fmt.Errorf("%w: too many catalog IDs", ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(titleIDs))
	normalized := make([]string, 0, len(titleIDs))
	for _, rawID := range titleIDs {
		titleID, err := normalizeTitleID(rawID)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[titleID]; duplicate {
			continue
		}
		seen[titleID] = struct{}{}
		normalized = append(normalized, titleID)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin catalog titles query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		/* watchstate.catalog_titles */
		WITH accessible_titles AS (`+accessibleTitlesSQL+`),
		catalog_page AS MATERIALIZED (
			SELECT title.*
			FROM titles title
			JOIN accessible_titles accessible ON accessible.id = title.id
			WHERE title.id = ANY($2::uuid[]) AND title.media_type = ANY($3::text[])
		), raw_provider_ids AS (
			SELECT identity.title_id, identity.provider, identity.external_id, 0 AS priority
			FROM title_external_ids identity
			JOIN catalog_page title ON title.id = identity.title_id AND title.media_type = identity.namespace
			UNION ALL
			SELECT identity.title_id, identity.provider, identity.external_id, 1 AS priority
			FROM profile_title_external_ids identity
			JOIN catalog_page title ON title.id = identity.title_id AND title.media_type = identity.namespace
			WHERE identity.profile_id = $1::uuid
		), selected_provider_ids AS (
			SELECT DISTINCT ON (title_id, provider) title_id, provider, external_id
			FROM raw_provider_ids
			ORDER BY title_id, provider, priority DESC
		), provider_ids AS (
			SELECT title_id, array_agg(provider ORDER BY provider) AS providers,
			       array_agg(external_id ORDER BY provider) AS external_ids
			FROM selected_provider_ids GROUP BY title_id
		)
		SELECT title.id::text, title.media_type, COALESCE(title.parent_id::text, ''),
		       COALESCE(CASE title.media_type WHEN 'season' THEN parent.id WHEN 'episode' THEN series.id END::text, ''),
		       COALESCE(CASE title.media_type WHEN 'episode' THEN parent.id END::text, ''),
		       title.ordinal, parent.ordinal, COALESCE(title.display_title, ''),
		       COALESCE(CASE title.media_type WHEN 'season' THEN parent.display_title WHEN 'episode' THEN series.display_title END, ''),
		       COALESCE(CASE title.media_type WHEN 'episode' THEN parent.display_title END, ''),
		       COALESCE(title.poster_url, ''), COALESCE(title.background_url, ''),
		       COALESCE(title.release_info, ''), COALESCE(title.release_date::text, ''),
		       COALESCE(metadata.overview, ''), metadata.runtime_minutes, COALESCE(metadata.genres, ARRAY[]::text[]),
		       metadata.community_rating,
		       library.title_id IS NOT NULL, progress.title_id IS NOT NULL,
		       progress.position_seconds, progress.duration_seconds, progress.completed, progress.last_watched_at,
		       COALESCE(title.resource_id, ''), COALESCE(title.resource_provider, ''),
		       COALESCE(title.source_addon_id::text, ''), COALESCE(title.source_catalog_id, ''),
		       COALESCE(title.source_name, ''), COALESCE(title.country, ''),
		       COALESCE(title.language, ''), COALESCE(title.category, ''),
		       COALESCE(provider_ids.providers, ARRAY[]::text[]), COALESCE(provider_ids.external_ids, ARRAY[]::text[])
		FROM catalog_page title
		LEFT JOIN titles parent ON parent.id = title.parent_id
		LEFT JOIN titles series ON series.id = parent.parent_id
		LEFT JOIN profile_library library ON library.profile_id = $1::uuid AND library.title_id = title.id
		LEFT JOIN profile_progress progress ON progress.profile_id = $1::uuid AND progress.title_id = title.id
		LEFT JOIN LATERAL (
			SELECT payload ->> 'overview' AS overview,
			       CASE WHEN jsonb_typeof(payload -> 'runtimeMinutes') = 'number'
			                  AND length(payload ->> 'runtimeMinutes') <= 9
			                  AND (payload ->> 'runtimeMinutes') ~ '^[0-9]+$'
			            THEN (payload ->> 'runtimeMinutes')::integer END AS runtime_minutes,
			       CASE WHEN jsonb_typeof(payload -> 'genres') = 'array' THEN ARRAY(
			            SELECT genre ->> 'name' FROM jsonb_array_elements(payload -> 'genres') genre
			            WHERE jsonb_typeof(genre) = 'object' AND NULLIF(btrim(genre ->> 'name'), '') IS NOT NULL
			       ) ELSE ARRAY[]::text[] END AS genres,
			       CASE WHEN jsonb_typeof(payload -> 'voteAverage') = 'number'
			                  AND length(payload ->> 'voteAverage') <= 16
			                  AND (payload ->> 'voteAverage') ~ '^[0-9]+([.][0-9]+)?$'
			            THEN (payload ->> 'voteAverage')::real END AS community_rating
			FROM title_metadata
			WHERE title_id = title.id
			ORDER BY updated_at DESC, provider, language LIMIT 1
		) metadata ON true
		LEFT JOIN provider_ids ON provider_ids.title_id = title.id
	`, profileID, normalized, allCatalogMediaTypes())
	if err != nil {
		return nil, fmt.Errorf("query catalog titles: %w", err)
	}
	defer rows.Close()
	items := make([]CatalogTitle, 0, len(normalized))
	for rows.Next() {
		item, scanErr := scanCatalogTitle(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan catalog title: %w", scanErr)
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate catalog titles: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit catalog titles query: %w", err)
	}
	return items, nil
}

// ListCatalogItems returns an exact, read-only page from the active profile's
// materialized library tree. Search, hierarchy expansion, user state, metadata,
// provider identities, total, and pagination are resolved by one SQL statement.
func (s *Service) ListCatalogItems(ctx context.Context, principal auth.Principal, query CatalogQuery) (CatalogPage, error) {
	query, err := normalizeCatalogQuery(query)
	if err != nil {
		return CatalogPage{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CatalogPage{}, fmt.Errorf("begin catalog page query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return CatalogPage{}, err
	}
	var parentID any
	if query.ParentID != "" {
		parentID = query.ParentID
	}

	rows, err := tx.Query(ctx, `
		/* watchstate.catalog_items */
		WITH RECURSIVE accessible_titles AS (`+accessibleTitlesSQL+`),
		profile_catalog AS MATERIALIZED (
			SELECT title.*, library.added_at AS catalog_added_at
			FROM profile_library library
			JOIN titles title ON title.id = library.title_id
			JOIN accessible_titles accessible ON accessible.id = title.id
			WHERE library.profile_id = $1::uuid AND title.parent_id IS NULL
			UNION ALL
			SELECT child.*, parent.catalog_added_at
			FROM titles child
			JOIN profile_catalog parent ON parent.id = child.parent_id
			JOIN accessible_titles accessible ON accessible.id = child.id
		), selected_descendants AS (
			SELECT title.id
			FROM profile_catalog title
			WHERE $2::uuid IS NOT NULL AND title.parent_id = $2::uuid
			UNION ALL
			SELECT child.id
			FROM profile_catalog child
			JOIN selected_descendants parent ON parent.id = child.parent_id
		), catalog_candidates AS MATERIALIZED (
			SELECT title.*
			FROM profile_catalog title
			WHERE title.media_type = ANY($3::text[])
			  AND ($6 = '' OR strpos(lower(COALESCE(title.display_title, '')), lower($6)) > 0)
			  AND (cardinality($8::uuid[]) = 0 OR title.id = ANY($8::uuid[]))
			  AND (
			      ($2::uuid IS NULL AND (($7 AND true) OR (NOT $7 AND title.parent_id IS NULL)))
			      OR ($2::uuid IS NOT NULL
			          AND EXISTS (SELECT 1 FROM profile_catalog parent WHERE parent.id = $2::uuid)
			          AND (($7 AND EXISTS (SELECT 1 FROM selected_descendants child WHERE child.id = title.id))
			               OR (NOT $7 AND title.parent_id = $2::uuid)))
			  )
		), catalog_total AS (
			SELECT count(*)::int AS total FROM catalog_candidates
		), catalog_page AS MATERIALIZED (
			SELECT * FROM catalog_candidates
			ORDER BY
			  CASE WHEN $9 = 'sortname' AND $10 = 'ascending' THEN lower(COALESCE(display_title, '')) END COLLATE "C" ASC NULLS LAST,
			  CASE WHEN $9 = 'sortname' AND $10 = 'descending' THEN lower(COALESCE(display_title, '')) END COLLATE "C" DESC NULLS LAST,
			  CASE WHEN $9 = '' AND $6 = '' AND $2::uuid IS NULL AND NOT $7 THEN catalog_added_at END DESC NULLS LAST,
			  CASE WHEN $9 = '' AND $6 = '' AND $2::uuid IS NOT NULL AND NOT $7 THEN ordinal END ASC NULLS LAST,
			  CASE WHEN $9 = '' AND ($6 <> '' OR $7) THEN lower(COALESCE(display_title, '')) END COLLATE "C" ASC NULLS LAST,
			  id
			LIMIT $4 OFFSET $5
		), raw_provider_ids AS (
			SELECT identity.title_id, identity.provider, identity.external_id, 0 AS priority
			FROM title_external_ids identity
			JOIN catalog_page title ON title.id = identity.title_id AND title.media_type = identity.namespace
			UNION ALL
			SELECT identity.title_id, identity.provider, identity.external_id, 1 AS priority
			FROM profile_title_external_ids identity
			JOIN catalog_page title ON title.id = identity.title_id AND title.media_type = identity.namespace
			WHERE identity.profile_id = $1::uuid
		), selected_provider_ids AS (
			SELECT DISTINCT ON (title_id, provider) title_id, provider, external_id
			FROM raw_provider_ids ORDER BY title_id, provider, priority DESC
		), provider_ids AS (
			SELECT title_id, array_agg(provider ORDER BY provider) AS providers,
			       array_agg(external_id ORDER BY provider) AS external_ids
			FROM selected_provider_ids GROUP BY title_id
		)
		SELECT catalog_total.total,
		       COALESCE(title.id::text, ''), COALESCE(title.media_type, ''), COALESCE(title.parent_id::text, ''),
		       COALESCE(CASE title.media_type WHEN 'season' THEN parent.id WHEN 'episode' THEN series.id END::text, ''),
		       COALESCE(CASE title.media_type WHEN 'episode' THEN parent.id END::text, ''),
		       title.ordinal, parent.ordinal, COALESCE(title.display_title, ''),
		       COALESCE(CASE title.media_type WHEN 'season' THEN parent.display_title WHEN 'episode' THEN series.display_title END, ''),
		       COALESCE(CASE title.media_type WHEN 'episode' THEN parent.display_title END, ''),
		       COALESCE(title.poster_url, ''), COALESCE(title.background_url, ''),
		       COALESCE(title.release_info, ''), COALESCE(title.release_date::text, ''),
		       COALESCE(metadata.overview, ''), metadata.runtime_minutes, COALESCE(metadata.genres, ARRAY[]::text[]),
		       metadata.community_rating,
		       library.title_id IS NOT NULL, progress.title_id IS NOT NULL,
		       progress.position_seconds, progress.duration_seconds, progress.completed, progress.last_watched_at,
		       COALESCE(title.resource_id, ''), COALESCE(title.resource_provider, ''),
		       COALESCE(title.source_addon_id::text, ''), COALESCE(title.source_catalog_id, ''),
		       COALESCE(title.source_name, ''), COALESCE(title.country, ''),
		       COALESCE(title.language, ''), COALESCE(title.category, ''),
		       COALESCE(provider_ids.providers, ARRAY[]::text[]), COALESCE(provider_ids.external_ids, ARRAY[]::text[])
		FROM catalog_total
		LEFT JOIN catalog_page title ON true
		LEFT JOIN profile_catalog parent ON parent.id = title.parent_id
		LEFT JOIN profile_catalog series ON series.id = parent.parent_id
		LEFT JOIN profile_library library ON library.profile_id = $1::uuid AND library.title_id = title.id
		LEFT JOIN profile_progress progress ON progress.profile_id = $1::uuid AND progress.title_id = title.id
		LEFT JOIN LATERAL (
			SELECT payload ->> 'overview' AS overview,
			       CASE WHEN jsonb_typeof(payload -> 'runtimeMinutes') = 'number'
			                  AND length(payload ->> 'runtimeMinutes') <= 9
			                  AND (payload ->> 'runtimeMinutes') ~ '^[0-9]+$'
			            THEN (payload ->> 'runtimeMinutes')::integer END AS runtime_minutes,
			       CASE WHEN jsonb_typeof(payload -> 'genres') = 'array' THEN ARRAY(
			            SELECT genre ->> 'name' FROM jsonb_array_elements(payload -> 'genres') genre
			            WHERE jsonb_typeof(genre) = 'object' AND NULLIF(btrim(genre ->> 'name'), '') IS NOT NULL
			       ) ELSE ARRAY[]::text[] END AS genres,
			       CASE WHEN jsonb_typeof(payload -> 'voteAverage') = 'number'
			                  AND length(payload ->> 'voteAverage') <= 16
			                  AND (payload ->> 'voteAverage') ~ '^[0-9]+([.][0-9]+)?$'
			            THEN (payload ->> 'voteAverage')::real END AS community_rating
			FROM title_metadata WHERE title_id = title.id
			ORDER BY updated_at DESC, provider, language LIMIT 1
		) metadata ON true
		LEFT JOIN provider_ids ON provider_ids.title_id = title.id
		ORDER BY
		  CASE WHEN $9 = 'sortname' AND $10 = 'ascending' THEN lower(COALESCE(title.display_title, '')) END COLLATE "C" ASC NULLS LAST,
		  CASE WHEN $9 = 'sortname' AND $10 = 'descending' THEN lower(COALESCE(title.display_title, '')) END COLLATE "C" DESC NULLS LAST,
		  CASE WHEN $9 = '' AND $6 = '' AND $2::uuid IS NULL AND NOT $7 THEN title.catalog_added_at END DESC NULLS LAST,
		  CASE WHEN $9 = '' AND $6 = '' AND $2::uuid IS NOT NULL AND NOT $7 THEN title.ordinal END ASC NULLS LAST,
		  CASE WHEN $9 = '' AND ($6 <> '' OR $7) THEN lower(COALESCE(title.display_title, '')) END COLLATE "C" ASC NULLS LAST,
		  title.id
	`, profileID, parentID, query.MediaTypes, query.Limit, query.Offset, query.SearchTerm, query.Recursive, query.IDs, query.SortBy, query.SortOrder)
	if err != nil {
		return CatalogPage{}, fmt.Errorf("query catalog page: %w", err)
	}
	defer rows.Close()
	result := CatalogPage{Items: make([]CatalogTitle, 0), Offset: query.Offset, Limit: query.Limit}
	for rows.Next() {
		var total int
		item, scanErr := scanCatalogTitleWithTotal(rows, &total)
		if scanErr != nil {
			return CatalogPage{}, fmt.Errorf("scan catalog page: %w", scanErr)
		}
		result.Total = total
		if item.ID != "" {
			result.Items = append(result.Items, item)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return CatalogPage{}, fmt.Errorf("iterate catalog page: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CatalogPage{}, fmt.Errorf("commit catalog page query: %w", err)
	}
	return result, nil
}

type catalogScanner interface {
	Scan(dest ...any) error
}

func scanCatalogTitle(row catalogScanner) (CatalogTitle, error) {
	return scanCatalogTitleDestinations(row, nil)
}

func scanCatalogTitleWithTotal(row catalogScanner, total *int) (CatalogTitle, error) {
	return scanCatalogTitleDestinations(row, total)
}

func scanCatalogTitleDestinations(row catalogScanner, total *int) (CatalogTitle, error) {
	item := CatalogTitle{}
	providers := []string{}
	externalIDs := []string{}
	var hasProgress bool
	var position, duration *int
	var completed *bool
	var lastWatchedAt *time.Time
	destinations := make([]any, 0, 36)
	if total != nil {
		destinations = append(destinations, total)
	}
	destinations = append(destinations,
		&item.ID, &item.MediaType, &item.ParentID, &item.SeriesID, &item.SeasonID,
		&item.Ordinal, &item.ParentOrdinal, &item.Title, &item.SeriesTitle, &item.SeasonTitle,
		&item.PosterURL, &item.BackgroundURL, &item.ReleaseInfo, &item.Released,
		&item.Overview, &item.RuntimeMinutes, &item.Genres, &item.CommunityRating,
		&item.InLibrary, &hasProgress, &position, &duration, &completed, &lastWatchedAt,
		&item.ResourceID, &item.ResourceProvider, &item.SourceAddonID, &item.SourceCatalogID,
		&item.SourceName, &item.Country, &item.Language, &item.Category, &providers, &externalIDs,
	)
	if err := row.Scan(destinations...); err != nil {
		return CatalogTitle{}, err
	}
	if item.Genres == nil {
		item.Genres = make([]string, 0)
	}
	if hasProgress && position != nil && duration != nil && completed != nil {
		item.Progress = &CatalogProgress{
			PositionSeconds: *position,
			DurationSeconds: *duration,
			Completed:       *completed,
			LastWatchedAt:   lastWatchedAt,
		}
	}
	item.ProviderIDs = make(map[string]string, len(providers))
	for index, provider := range providers {
		if index < len(externalIDs) {
			item.ProviderIDs[provider] = externalIDs[index]
		}
	}
	return item, nil
}

func normalizeCatalogQuery(query CatalogQuery) (CatalogQuery, error) {
	if query.Offset < 0 || query.Offset > maximumCatalogOffset {
		return CatalogQuery{}, fmt.Errorf("%w: catalog offset must be between 0 and %d", ErrInvalidInput, maximumCatalogOffset)
	}
	if query.Limit < 1 || query.Limit > MaximumCatalogPageSize {
		return CatalogQuery{}, fmt.Errorf("%w: catalog limit must be between 1 and %d", ErrInvalidInput, MaximumCatalogPageSize)
	}
	if strings.TrimSpace(query.ParentID) != "" {
		parentID, err := normalizeTitleID(query.ParentID)
		if err != nil {
			return CatalogQuery{}, err
		}
		query.ParentID = parentID
	} else {
		query.ParentID = ""
	}
	query.SearchTerm = strings.TrimSpace(query.SearchTerm)
	if !utf8.ValidString(query.SearchTerm) || len(query.SearchTerm) > maximumCatalogSearchBytes || strings.ContainsRune(query.SearchTerm, '\x00') {
		return CatalogQuery{}, fmt.Errorf("%w: invalid catalog search term", ErrInvalidInput)
	}
	if len(query.IDs) > maximumCatalogIDs {
		return CatalogQuery{}, fmt.Errorf("%w: too many catalog IDs", ErrInvalidInput)
	}
	seenIDs := make(map[string]struct{}, len(query.IDs))
	ids := make([]string, 0, len(query.IDs))
	for _, rawID := range query.IDs {
		id, err := normalizeTitleID(rawID)
		if err != nil {
			return CatalogQuery{}, err
		}
		if _, duplicate := seenIDs[id]; duplicate {
			continue
		}
		seenIDs[id] = struct{}{}
		ids = append(ids, id)
	}
	query.IDs = ids
	query.SortBy = strings.ToLower(strings.TrimSpace(query.SortBy))
	if query.SortBy != "" && query.SortBy != "sortname" {
		return CatalogQuery{}, fmt.Errorf("%w: unsupported catalog sort", ErrInvalidInput)
	}
	query.SortOrder = strings.ToLower(strings.TrimSpace(query.SortOrder))
	if query.SortBy == "" {
		if query.SortOrder != "" {
			return CatalogQuery{}, fmt.Errorf("%w: catalog sort order requires a sort key", ErrInvalidInput)
		}
	} else if query.SortOrder == "" {
		query.SortOrder = "ascending"
	} else if query.SortOrder != "ascending" && query.SortOrder != "descending" {
		return CatalogQuery{}, fmt.Errorf("%w: unsupported catalog sort order", ErrInvalidInput)
	}
	if len(query.MediaTypes) == 0 {
		query.MediaTypes = allCatalogMediaTypes()
		return query, nil
	}
	seen := make(map[string]struct{}, len(query.MediaTypes))
	mediaTypes := make([]string, 0, len(query.MediaTypes))
	for _, mediaType := range query.MediaTypes {
		mediaType = strings.ToLower(strings.TrimSpace(mediaType))
		if _, valid := catalogMediaTypes[mediaType]; !valid {
			return CatalogQuery{}, fmt.Errorf("%w: unsupported catalog media type", ErrInvalidInput)
		}
		if _, duplicate := seen[mediaType]; duplicate {
			continue
		}
		seen[mediaType] = struct{}{}
		mediaTypes = append(mediaTypes, mediaType)
	}
	if len(mediaTypes) == 0 {
		return CatalogQuery{}, fmt.Errorf("%w: catalog media types must not be empty", ErrInvalidInput)
	}
	sort.Strings(mediaTypes)
	query.MediaTypes = mediaTypes
	return query, nil
}

func allCatalogMediaTypes() []string {
	return []string{"episode", "movie", "season", "series"}
}
