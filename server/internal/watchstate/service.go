package watchstate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/runtimesettings"
	"github.com/moodiness/rivune/server/internal/tracking"
)

var (
	ErrConflict           = errors.New("watch state version conflict")
	ErrInvalidInput       = errors.New("invalid watch state input")
	ErrNotFound           = errors.New("title or watch state not found")
	ErrProgressNotFound   = errors.New("playback progress not found")
	ErrForbidden          = errors.New("watch state operation forbidden")
	ErrProfileRequired    = errors.New("active profile required")
	ErrOutboxCapacity     = errors.New("tracking synchronization capacity reached")
	errAddonAccessChanged = fmt.Errorf("%w", ErrNotFound)

	uuidPattern                     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	providerPattern                 = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	artworkPathPattern              = regexp.MustCompile(`^/api/v1/artwork/[0-9a-f]{64}$`)
	continueMetadataLanguagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
)

const (
	defaultPageSize                         = 20
	maximumPageSize                         = 100
	maximumProfileTitleIdentitiesPerProfile = 100_000
	MaximumTVLibraryMembershipIdentities    = 100
	maximumSupplementalUserDataCount        = int64(1<<31 - 1)
)

const accessibleTitlesSQL = `
	SELECT title.id, title.media_type
	FROM titles title
	WHERE title.is_current
	  AND CASE
	    WHEN title.media_type = 'tv' THEN EXISTS (
	        SELECT 1
	        FROM profile_addons addon
	        JOIN profiles profile ON profile.id = $1::uuid
	        WHERE addon.id = title.source_addon_id
	          AND addon.enabled = true
	          AND (
	              EXISTS (
	                  SELECT 1
	                  FROM addon_profile_access access
	                  WHERE access.addon_id = addon.id
	                    AND access.profile_id = profile.id
	              ) OR EXISTS (
	                  SELECT 1
	                  FROM addon_category_access access
	                  WHERE access.addon_id = addon.id
	                    AND access.category_id = profile.category_id
	              )
	          )
	    )
	    ELSE (
	        NOT EXISTS (
	            SELECT 1
	            FROM profile_title_external_ids scoped
	            WHERE scoped.title_id = title.id
	        ) OR EXISTS (
	            SELECT 1
	            FROM profile_title_external_ids scoped
	            WHERE scoped.title_id = title.id
	              AND scoped.profile_id = $1::uuid
	        )
	    ) AND (
	        title.source_addon_id IS NULL OR EXISTS (
	            SELECT 1
	            FROM profile_addons addon
	            JOIN profiles profile ON profile.id = $1::uuid
	            WHERE addon.id = title.source_addon_id
	              AND addon.enabled = true
	              AND (
	                  EXISTS (
	                      SELECT 1
	                      FROM addon_profile_access access
	                      WHERE access.addon_id = addon.id
	                        AND access.profile_id = profile.id
	                  ) OR EXISTS (
	                      SELECT 1
	                      FROM addon_category_access access
	                      WHERE access.addon_id = addon.id
	                        AND access.category_id = profile.category_id
	                  )
	              )
	        )
	    )
	END
`

type trackingSink interface {
	EnqueueTx(context.Context, pgx.Tx, string, string, string, tracking.Event) error
}

type trackingBatchSink interface {
	EnqueueBatchTx(context.Context, pgx.Tx, string, []tracking.BatchEvent) error
}

type canonicalTitleProvider interface {
	MovieDetails(context.Context, string, string) (metadata.ProviderMovie, error)
	SeriesDetails(context.Context, string, string) (metadata.ProviderSeries, error)
}

type canonicalExternalIDResolver interface {
	ResolveExternalID(context.Context, string, string, string) (string, error)
}

type ProviderSet struct {
	Generation int64
	Canonical  canonicalTitleProvider
	Resolver   canonicalExternalIDResolver
}

func NewProviderSet(generation int64, provider canonicalTitleProvider, resolver canonicalExternalIDResolver) ProviderSet {
	return ProviderSet{Generation: generation, Canonical: provider, Resolver: resolver}
}

type ProviderSource interface {
	WatchstateProviders() ProviderSet
}

type staticProviderSource struct {
	mu        sync.RWMutex
	providers ProviderSet
}

func (source *staticProviderSource) WatchstateProviders() ProviderSet {
	source.mu.RLock()
	defer source.mu.RUnlock()
	return source.providers
}

type watchstateProviderContextKey struct{}

type Service struct {
	pool            *pgxpool.Pool
	location        *time.Location
	tracking        trackingSink
	providerSource  ProviderSource
	runtimeSettings *runtimesettings.Source
}

func NewServiceWithProviderSource(pool *pgxpool.Pool, location *time.Location, source ProviderSource, sinks ...trackingSink) *Service {
	if source == nil {
		source = &staticProviderSource{}
	}
	service := &Service{pool: pool, location: location, providerSource: source}
	if len(sinks) > 0 {
		service.tracking = sinks[0]
	}
	return service
}

func NewService(pool *pgxpool.Pool, location *time.Location, sinks ...trackingSink) *Service {
	return NewServiceWithProviderSource(pool, location, &staticProviderSource{}, sinks...)
}

func NewServiceWithRuntimeSettings(pool *pgxpool.Pool, runtimeSettings *runtimesettings.Source, source ProviderSource, sinks ...trackingSink) *Service {
	service := NewServiceWithProviderSource(pool, nil, source, sinks...)
	service.runtimeSettings = runtimeSettings
	return service
}

func (s *Service) runtimeLocation(ctx context.Context) *time.Location {
	if s.runtimeSettings != nil {
		return runtimesettings.Load(ctx, s.runtimeSettings).Location
	}
	return s.location
}

func (s *Service) pinProviders(ctx context.Context) context.Context {
	if _, ok := ctx.Value(watchstateProviderContextKey{}).(ProviderSet); ok {
		return ctx
	}
	return context.WithValue(ctx, watchstateProviderContextKey{}, s.providerSource.WatchstateProviders())
}

func watchstateProviders(ctx context.Context) ProviderSet {
	providers, _ := ctx.Value(watchstateProviderContextKey{}).(ProviderSet)
	return providers
}

func (s *Service) SetCanonicalProvider(provider canonicalTitleProvider, resolver canonicalExternalIDResolver) {
	if source, ok := s.providerSource.(*staticProviderSource); ok {
		source.mu.Lock()
		source.providers = NewProviderSet(0, provider, resolver)
		source.mu.Unlock()
	}
}

func watchstateTrackingError(err error) error {
	if errors.Is(err, tracking.ErrOutboxCapacity) {
		return ErrOutboxCapacity
	}
	return err
}

func (s *Service) enqueueTrackingTx(ctx context.Context, tx pgx.Tx, profileID, titleID, key string, event tracking.Event) error {
	if s.tracking == nil {
		return nil
	}
	return watchstateTrackingError(s.tracking.EnqueueTx(ctx, tx, profileID, titleID, key, event))
}

func (s *Service) ResolveTitle(ctx context.Context, principal auth.Principal, input ResolveTitleInput) (TitleReference, error) {
	return s.resolveTitle(ctx, principal, input, true, false)
}

// ResolveCatalogTitle canonicalizes a provider search result for an active,
// authorized profile without requiring profile-management authority.
func (s *Service) ResolveCatalogTitle(ctx context.Context, principal auth.Principal, input ResolveTitleInput) (TitleReference, error) {
	return s.resolveTitle(ctx, principal, input, false, false)
}

// ResolveLinkedCatalogTitle revalidates linked-session authority after any
// provider work and holds its authorization locks through persistence commit.
func (s *Service) ResolveLinkedCatalogTitle(ctx context.Context, principal auth.Principal, input ResolveTitleInput) (TitleReference, error) {
	return s.resolveTitle(ctx, principal, input, false, true)
}

func (s *Service) resolveTitle(ctx context.Context, principal auth.Principal, input ResolveTitleInput, requireManagement, linked bool) (TitleReference, error) {
	ctx = s.pinProviders(ctx)
	ctx = runtimesettings.Pin(ctx, s.runtimeSettings)
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.Title = strings.TrimSpace(input.Title)
	input.PosterURL = strings.TrimSpace(input.PosterURL)
	input.BackgroundURL = strings.TrimSpace(input.BackgroundURL)
	input.ReleaseInfo = strings.TrimSpace(input.ReleaseInfo)
	input.Released = strings.TrimSpace(input.Released)
	input.SourceAddonID = strings.ToLower(strings.TrimSpace(input.SourceAddonID))
	input.SourceCatalogID = strings.TrimSpace(input.SourceCatalogID)
	input.SourceName = strings.TrimSpace(input.SourceName)
	input.Country = strings.TrimSpace(input.Country)
	input.Language = strings.TrimSpace(input.Language)
	input.Category = strings.TrimSpace(input.Category)
	if input.MediaType != "movie" && input.MediaType != "series" && input.MediaType != "tv" {
		return TitleReference{}, fmt.Errorf("%w: mediaType must be movie, series, or tv", ErrInvalidInput)
	}
	if !providerPattern.MatchString(input.Provider) {
		return TitleReference{}, fmt.Errorf("%w: invalid provider", ErrInvalidInput)
	}
	if len(input.ResourceID) < 1 || len(input.ResourceID) > 512 {
		return TitleReference{}, fmt.Errorf("%w: invalid resource identifier", ErrInvalidInput)
	}
	if len(input.Title) < 1 || len(input.Title) > 500 || len(input.PosterURL) > 4096 || len(input.BackgroundURL) > 4096 || len(input.ReleaseInfo) > 120 {
		return TitleReference{}, fmt.Errorf("%w: invalid title snapshot", ErrInvalidInput)
	}
	if input.MediaType == "tv" {
		if input.Provider != "addon" {
			return TitleReference{}, fmt.Errorf("%w: TV provider must be addon", ErrInvalidInput)
		}
		if !uuidPattern.MatchString(input.SourceAddonID) {
			return TitleReference{}, fmt.Errorf("%w: sourceAddonId must be a UUID", ErrInvalidInput)
		}
		if len(input.SourceCatalogID) > 512 || len(input.SourceName) > 500 || len(input.Country) > 128 || len(input.Language) > 128 || len(input.Category) > 256 {
			return TitleReference{}, fmt.Errorf("%w: invalid TV source snapshot", ErrInvalidInput)
		}
		input.ExternalID = tvExternalID(input.SourceAddonID, input.ResourceID)
		input.Released = ""
		return s.resolveTVTitle(ctx, principal, input, linked)
	}
	if len(input.ExternalID) < 1 || len(input.ExternalID) > 512 {
		return TitleReference{}, fmt.Errorf("%w: invalid external identifier", ErrInvalidInput)
	}
	if input.MediaType == "movie" && input.Released != "" {
		released, err := time.Parse(time.DateOnly, input.Released)
		if err != nil || released.Year() < 1 || released.Format(time.DateOnly) != input.Released {
			return TitleReference{}, fmt.Errorf("%w: released must be a YYYY-MM-DD date", ErrInvalidInput)
		}
	}
	if input.Provider == "addon" && input.SourceAddonID != "" {
		if !uuidPattern.MatchString(input.SourceAddonID) || len(input.SourceCatalogID) < 1 || len(input.SourceCatalogID) > 512 || len(input.SourceName) < 1 || len(input.SourceName) > 500 {
			return TitleReference{}, fmt.Errorf("%w: invalid addon source snapshot", ErrInvalidInput)
		}
		expectedIdentity := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(input.SourceAddonID+"\x00"+input.MediaType+"\x00"+input.ResourceID)))
		if input.ExternalID != expectedIdentity {
			return TitleReference{}, fmt.Errorf("%w: addon source does not match external identity", ErrInvalidInput)
		}
	} else {
		input.SourceAddonID = ""
		input.SourceCatalogID = ""
		input.SourceName = ""
	}
	input.Country = ""
	input.Language = ""
	input.Category = ""

	if s.usesProfileScopedTitleIdentity(ctx, input) {
		return s.persistProfileScopedTitle(ctx, principal, input, requireManagement, linked)
	}
	if err := s.authorizeCanonicalTitleResolution(ctx, principal, linked); err != nil {
		return TitleReference{}, err
	}
	canonical, err := s.resolveCanonicalTitle(ctx, input)
	if err != nil {
		return TitleReference{}, err
	}

	return s.persistCanonicalTitle(ctx, principal, input, canonical, linked)
}

type customResolverVideo struct {
	InputIndex      int    `json:"inputIndex"`
	ResourceID      string `json:"resourceId"`
	Title           string `json:"title"`
	SeasonNumber    int    `json:"seasonNumber"`
	EpisodeNumber   int    `json:"episodeNumber"`
	ThumbnailURL    string `json:"thumbnailUrl"`
	BackgroundURL   string `json:"backgroundUrl"`
	ReleaseInfo     string `json:"releaseInfo"`
	Released        string `json:"released"`
	SeasonIdentity  string `json:"seasonIdentity"`
	EpisodeIdentity string `json:"episodeIdentity"`
}

func (s *Service) ResolveCustomSeries(ctx context.Context, principal auth.Principal, input ResolveCustomSeriesInput) (ResolveCustomSeriesResult, error) {
	input, videos, err := normalizeCustomSeriesInput(input)
	if err != nil {
		return ResolveCustomSeriesResult{}, err
	}
	payload, err := json.Marshal(videos)
	if err != nil {
		return ResolveCustomSeriesResult{}, fmt.Errorf("encode custom series videos: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ResolveCustomSeriesResult{}, fmt.Errorf("begin custom series resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return ResolveCustomSeriesResult{}, err
	}
	var addonAccessible bool
	err = tx.QueryRow(ctx, `
		SELECT true
		FROM profile_addons addon
		JOIN profiles profile ON profile.id = $2::uuid
		WHERE addon.id = $1::uuid
		  AND addon.enabled = true
		  AND (
		      EXISTS (
		          SELECT 1
		          FROM addon_profile_access access
		          WHERE access.addon_id = addon.id
		            AND access.profile_id = profile.id
		      ) OR EXISTS (
		          SELECT 1
		          FROM addon_category_access access
		          WHERE access.addon_id = addon.id
		            AND access.category_id = profile.category_id
		      )
		  )
		FOR SHARE OF addon
	`, input.SourceAddonID, profileID).Scan(&addonAccessible)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolveCustomSeriesResult{}, ErrNotFound
	}
	if err != nil {
		return ResolveCustomSeriesResult{}, fmt.Errorf("authorize custom series addon: %w", err)
	}
	seriesIdentity := customTitleExternalID(input.SourceAddonID, input.SourceType, "series", input.Series.ResourceID)
	if err := lockProfileTitleIdentities(ctx, tx, profileID); err != nil {
		return ResolveCustomSeriesResult{}, err
	}
	var existingIdentityCount, newIdentityCount int64
	if err := tx.QueryRow(ctx, `
		WITH video_input AS (
			SELECT *
			FROM jsonb_to_recordset($2::jsonb) AS video(
				"seasonIdentity" text, "episodeIdentity" text
			)
		), requested AS (
			SELECT 'series'::text AS namespace, $3::text AS external_id
			UNION
			SELECT 'season', "seasonIdentity" FROM video_input
			UNION
			SELECT 'episode', "episodeIdentity" FROM video_input
		)
		SELECT
			(SELECT count(*)
			 FROM profile_title_external_ids
			 WHERE profile_id = $1::uuid),
			(SELECT count(*)
			 FROM requested
			 WHERE NOT EXISTS (
				 SELECT 1
				 FROM profile_title_external_ids existing
				 WHERE existing.profile_id = $1::uuid
				   AND existing.provider = 'addon'
				   AND existing.namespace = requested.namespace
				   AND existing.external_id = requested.external_id
			 ))
	`, profileID, payload, seriesIdentity).Scan(&existingIdentityCount, &newIdentityCount); err != nil {
		return ResolveCustomSeriesResult{}, fmt.Errorf("check custom title identity capacity: %w", err)
	}
	if newIdentityCount > 0 && existingIdentityCount+newIdentityCount > maximumProfileTitleIdentitiesPerProfile {
		return ResolveCustomSeriesResult{}, fmt.Errorf("%w: profile title identity capacity reached", ErrInvalidInput)
	}

	var seriesTitleID string
	if err := tx.QueryRow(ctx, `
		WITH existing AS (
			SELECT identity.title_id
			FROM profile_title_external_ids identity
			WHERE identity.profile_id = $1::uuid
			  AND identity.provider = 'addon'
			  AND identity.namespace = 'series'
			  AND identity.external_id = $2
		), candidate AS (
			SELECT COALESCE((SELECT title_id FROM existing), gen_random_uuid()) AS id
		), inserted_title AS (
			INSERT INTO titles (
				id, media_type, display_title, poster_url, background_url, release_info,
				resource_id, resource_provider, source_addon_id, is_current
			)
			SELECT candidate.id, 'series', $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''),
			       $7, 'addon', $8::uuid, true
			FROM candidate
			WHERE NOT EXISTS (SELECT 1 FROM existing)
			RETURNING id
		), updated_title AS (
			UPDATE titles title
			SET display_title = $3,
			    poster_url = NULLIF($4, ''),
			    background_url = NULLIF($5, ''),
			    release_info = NULLIF($6, ''),
			    resource_id = $7,
			    resource_provider = 'addon',
			    source_addon_id = $8::uuid,
			    is_current = true,
			    updated_at = now()
			FROM existing
			WHERE title.id = existing.title_id
			RETURNING title.id
		), inserted_identity AS (
			INSERT INTO profile_title_external_ids (profile_id, title_id, provider, namespace, external_id)
			SELECT $1::uuid, inserted_title.id, 'addon', 'series', $2
			FROM inserted_title
			RETURNING title_id
		)
		SELECT id::text FROM candidate
	`, profileID, seriesIdentity, input.Series.Title, input.Series.PosterURL,
		input.Series.BackgroundURL, input.Series.ReleaseInfo, input.Series.ResourceID,
		input.SourceAddonID).Scan(&seriesTitleID); err != nil {
		return ResolveCustomSeriesResult{}, fmt.Errorf("upsert custom series title: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		WITH scoped_seasons AS (
			SELECT id FROM titles
			WHERE parent_id = $1::uuid AND media_type = 'season'
		)
		UPDATE titles title
		SET is_current = false, updated_at = now()
		WHERE title.is_current
		  AND ((title.media_type = 'season' AND title.parent_id = $1::uuid)
		       OR (title.media_type = 'episode' AND title.parent_id IN (SELECT id FROM scoped_seasons)))
	`, seriesTitleID); err != nil {
		return ResolveCustomSeriesResult{}, fmt.Errorf("deactivate stale custom series titles: %w", err)
	}

	result := ResolveCustomSeriesResult{
		Series:  CustomSeriesReference{TitleID: seriesTitleID, ResourceID: input.Series.ResourceID},
		Seasons: []CustomSeasonReference{},
		Videos:  []CustomVideoReference{},
	}
	if len(videos) > 0 {
		rows, err := tx.Query(ctx, `
			WITH video_input AS (
				SELECT * FROM jsonb_to_recordset($1::jsonb) AS video(
					"inputIndex" integer, "resourceId" text, title text,
					"seasonNumber" integer, "episodeNumber" integer,
					"thumbnailUrl" text, "backgroundUrl" text, "releaseInfo" text,
					released text, "seasonIdentity" text, "episodeIdentity" text
				)
			), season_input AS (
				SELECT DISTINCT ON ("seasonNumber")
				       "seasonNumber" AS season_number, "seasonIdentity" AS identity
				FROM video_input
				ORDER BY "seasonNumber", "inputIndex"
			), existing_seasons AS (
				SELECT identity.title_id AS id, season.season_number
				FROM season_input season
				JOIN profile_title_external_ids identity
				  ON identity.profile_id = $2::uuid
				 AND identity.provider = 'addon'
				 AND identity.namespace = 'season'
				 AND identity.external_id = season.identity
			), updated_seasons AS (
				UPDATE titles title
				SET parent_id = $3::uuid, ordinal = existing.season_number,
				    display_title = 'Season ' || existing.season_number::text,
				    poster_url = NULL, background_url = NULL, release_info = NULL,
				    release_date = NULL, resource_id = $4, resource_provider = 'addon',
				    source_addon_id = $5::uuid, is_current = true, updated_at = now()
				FROM existing_seasons existing
				WHERE title.id = existing.id
				RETURNING title.id, title.ordinal AS season_number
			), new_seasons AS (
				SELECT gen_random_uuid() AS id, season.season_number, season.identity
				FROM season_input season
				LEFT JOIN existing_seasons existing ON existing.season_number = season.season_number
				WHERE existing.id IS NULL
			), inserted_seasons AS (
				INSERT INTO titles (
					id, media_type, parent_id, ordinal, display_title, resource_id,
					resource_provider, source_addon_id, is_current
				)
				SELECT id, 'season', $3::uuid, season_number, 'Season ' || season_number::text,
				       $4, 'addon', $5::uuid, true
				FROM new_seasons
				RETURNING id, ordinal AS season_number
			), inserted_season_identities AS (
				INSERT INTO profile_title_external_ids (profile_id, title_id, provider, namespace, external_id)
				SELECT $2::uuid, new_seasons.id, 'addon', 'season', new_seasons.identity
				FROM new_seasons
				JOIN inserted_seasons ON inserted_seasons.id = new_seasons.id
				RETURNING title_id
			), season_rows AS (
				SELECT id, season_number FROM updated_seasons
				UNION ALL
				SELECT id, season_number FROM inserted_seasons
			), existing_episodes AS (
				SELECT identity.title_id AS id, video.*
				FROM video_input video
				JOIN profile_title_external_ids identity
				  ON identity.profile_id = $2::uuid
				 AND identity.provider = 'addon'
				 AND identity.namespace = 'episode'
				 AND identity.external_id = video."episodeIdentity"
			), updated_episodes AS (
				UPDATE titles title
				SET parent_id = season.id, ordinal = existing."episodeNumber",
				    display_title = NULLIF(existing.title, ''),
				    poster_url = NULLIF(existing."thumbnailUrl", ''),
				    background_url = NULLIF(existing."backgroundUrl", ''),
				    release_info = NULLIF(existing."releaseInfo", ''),
				    release_date = NULLIF(existing.released, '')::date,
				    resource_id = existing."resourceId", resource_provider = 'addon',
				    source_addon_id = $5::uuid, is_current = true, updated_at = now()
				FROM existing_episodes existing
				JOIN season_rows season ON season.season_number = existing."seasonNumber"
				WHERE title.id = existing.id
				RETURNING title.id, title.resource_id, title.parent_id AS season_id,
				          title.ordinal AS episode_number, existing."seasonNumber" AS season_number,
				          existing."inputIndex" AS input_index
			), new_episodes AS (
				SELECT gen_random_uuid() AS id, video.*, season.id AS season_id
				FROM video_input video
				JOIN season_rows season ON season.season_number = video."seasonNumber"
				LEFT JOIN existing_episodes existing ON existing."inputIndex" = video."inputIndex"
				WHERE existing.id IS NULL
			), inserted_episodes AS (
				INSERT INTO titles (
					id, media_type, parent_id, ordinal, display_title, poster_url,
					background_url, release_info, release_date, resource_id,
					resource_provider, source_addon_id, is_current
				)
				SELECT id, 'episode', season_id, "episodeNumber", NULLIF(title, ''),
				       NULLIF("thumbnailUrl", ''), NULLIF("backgroundUrl", ''),
				       NULLIF("releaseInfo", ''), NULLIF(released, '')::date,
				       "resourceId", 'addon', $5::uuid, true
				FROM new_episodes
				RETURNING id, resource_id, parent_id AS season_id, ordinal AS episode_number
			), inserted_episode_identities AS (
				INSERT INTO profile_title_external_ids (profile_id, title_id, provider, namespace, external_id)
				SELECT $2::uuid, new_episodes.id, 'addon', 'episode', new_episodes."episodeIdentity"
				FROM new_episodes
				JOIN inserted_episodes ON inserted_episodes.id = new_episodes.id
				RETURNING title_id
			), episode_rows AS (
				SELECT id, resource_id, season_id, season_number, episode_number, input_index
				FROM updated_episodes
				UNION ALL
				SELECT inserted.id, inserted.resource_id, inserted.season_id,
				       new_episodes."seasonNumber", inserted.episode_number, new_episodes."inputIndex"
				FROM inserted_episodes inserted
				JOIN new_episodes ON new_episodes.id = inserted.id
			)
			SELECT 'season', season.id::text, '', '', season.season_number, 0, -1
			FROM season_rows season
			UNION ALL
			SELECT 'video', episode.id::text, episode.resource_id, episode.season_id::text,
			       episode.season_number, episode.episode_number, episode.input_index
			FROM episode_rows episode
			ORDER BY 1, 7, 5
		`, payload, profileID, seriesTitleID, input.Series.ResourceID, input.SourceAddonID)
		if err != nil {
			return ResolveCustomSeriesResult{}, fmt.Errorf("upsert custom series hierarchy: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rowKind, titleID, resourceID, seasonTitleID string
			var seasonNumber, episodeNumber, inputIndex int
			if err := rows.Scan(&rowKind, &titleID, &resourceID, &seasonTitleID, &seasonNumber, &episodeNumber, &inputIndex); err != nil {
				return ResolveCustomSeriesResult{}, fmt.Errorf("scan custom series hierarchy: %w", err)
			}
			if rowKind == "season" {
				result.Seasons = append(result.Seasons, CustomSeasonReference{TitleID: titleID, SeasonNumber: seasonNumber})
				continue
			}
			result.Videos = append(result.Videos, CustomVideoReference{
				TitleID: titleID, ResourceID: resourceID, SeasonTitleID: seasonTitleID,
				SeasonNumber: seasonNumber, EpisodeNumber: episodeNumber,
			})
		}
		if err := rows.Err(); err != nil {
			return ResolveCustomSeriesResult{}, fmt.Errorf("iterate custom series hierarchy: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ResolveCustomSeriesResult{}, fmt.Errorf("commit custom series resolution: %w", err)
	}
	return result, nil
}

func normalizeCustomSeriesInput(input ResolveCustomSeriesInput) (ResolveCustomSeriesInput, []customResolverVideo, error) {
	input.SourceAddonID = strings.ToLower(strings.TrimSpace(input.SourceAddonID))
	if !uuidPattern.MatchString(input.SourceAddonID) {
		return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: sourceAddonId must be a UUID", ErrInvalidInput)
	}
	if len(input.SourceType) < 1 || len(input.SourceType) > 512 || strings.TrimSpace(input.SourceType) != input.SourceType || strings.ContainsRune(input.SourceType, '\x00') {
		return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: sourceType must contain between 1 and 512 trimmed non-NUL characters", ErrInvalidInput)
	}
	if len(input.Videos) > MaximumCustomSeriesVideos {
		return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: videos must contain at most %d items", ErrInvalidInput, MaximumCustomSeriesVideos)
	}
	input.Series.Title = strings.TrimSpace(input.Series.Title)
	input.Series.PosterURL = strings.TrimSpace(input.Series.PosterURL)
	input.Series.BackgroundURL = strings.TrimSpace(input.Series.BackgroundURL)
	input.Series.ReleaseInfo = strings.TrimSpace(input.Series.ReleaseInfo)
	if err := validateOpaqueResourceID(input.Series.ResourceID); err != nil {
		return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: invalid series resourceId", ErrInvalidInput)
	}
	if len(input.Series.Title) < 1 || len(input.Series.Title) > 500 || len(input.Series.ReleaseInfo) > 120 ||
		strings.ContainsRune(input.Series.Title+input.Series.ReleaseInfo, '\x00') ||
		!validLocalizedArtwork(input.Series.PosterURL) || !validLocalizedArtwork(input.Series.BackgroundURL) {
		return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: invalid series snapshot", ErrInvalidInput)
	}
	resourceIDs := make(map[string]struct{}, len(input.Videos))
	coordinates := make(map[[2]int]struct{}, len(input.Videos))
	videos := make([]customResolverVideo, len(input.Videos))
	for index := range input.Videos {
		video := &input.Videos[index]
		video.Title = strings.TrimSpace(video.Title)
		video.ThumbnailURL = strings.TrimSpace(video.ThumbnailURL)
		video.BackgroundURL = strings.TrimSpace(video.BackgroundURL)
		video.ReleaseInfo = strings.TrimSpace(video.ReleaseInfo)
		video.Released = strings.TrimSpace(video.Released)
		if err := validateOpaqueResourceID(video.ResourceID); err != nil {
			return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: videos[%d].resourceId is invalid", ErrInvalidInput, index)
		}
		if _, exists := resourceIDs[video.ResourceID]; exists {
			return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: duplicate video resourceId", ErrInvalidInput)
		}
		resourceIDs[video.ResourceID] = struct{}{}
		coordinate := [2]int{video.SeasonNumber, video.EpisodeNumber}
		if video.SeasonNumber < 0 || video.EpisodeNumber < 0 ||
			int64(video.SeasonNumber) > 2147483647 || int64(video.EpisodeNumber) > 2147483647 {
			return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: video seasonNumber and episodeNumber must be between 0 and 2147483647", ErrInvalidInput)
		}
		if _, exists := coordinates[coordinate]; exists {
			return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: duplicate video season and episode numbers", ErrInvalidInput)
		}
		coordinates[coordinate] = struct{}{}
		if len(video.Title) > 500 || len(video.ReleaseInfo) > 120 ||
			strings.ContainsRune(video.Title+video.ReleaseInfo, '\x00') ||
			!validLocalizedArtwork(video.ThumbnailURL) || !validLocalizedArtwork(video.BackgroundURL) {
			return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: videos[%d] has an invalid snapshot", ErrInvalidInput, index)
		}
		if video.Released != "" {
			released, err := time.Parse(time.DateOnly, video.Released)
			if err != nil || released.Year() < 1 || released.Format(time.DateOnly) != video.Released {
				return ResolveCustomSeriesInput{}, nil, fmt.Errorf("%w: videos[%d].released must be a YYYY-MM-DD date", ErrInvalidInput, index)
			}
		}
		videos[index] = customResolverVideo{
			InputIndex: index, ResourceID: video.ResourceID, Title: video.Title,
			SeasonNumber: video.SeasonNumber, EpisodeNumber: video.EpisodeNumber,
			ThumbnailURL: video.ThumbnailURL, BackgroundURL: video.BackgroundURL,
			ReleaseInfo: video.ReleaseInfo, Released: video.Released,
			SeasonIdentity:  customTitleExternalID(input.SourceAddonID, input.SourceType, "season", input.Series.ResourceID, strconv.Itoa(video.SeasonNumber)),
			EpisodeIdentity: customTitleExternalID(input.SourceAddonID, input.SourceType, "episode", input.Series.ResourceID, video.ResourceID),
		}
	}
	return input, videos, nil
}
func validateOpaqueResourceID(value string) error {
	if len(value) < 1 || len(value) > 512 || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return ErrInvalidInput
	}
	return nil
}

func validLocalizedArtwork(value string) bool {
	return value == "" || (len(value) <= 4096 && artworkPathPattern.MatchString(value))
}

func customTitleExternalID(sourceAddonID, sourceType, kind string, opaqueIDs ...string) string {
	parts := []string{sourceAddonID, sourceType, kind}
	parts = append(parts, opaqueIDs...)
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

func lockProfileTitleIdentities(ctx context.Context, tx pgx.Tx, profileID string) error {
	lockKey := "profile-title-identities:" + profileID
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock profile title identities: %w", err)
	}
	return nil
}

type canonicalTitleIdentity struct {
	provider   string
	externalID string
	storedID   string
}

type canonicalTitleSnapshot struct {
	identities    []canonicalTitleIdentity
	title         string
	posterURL     string
	backgroundURL string
	releaseInfo   string
	released      string
	resourceID    string
	resource      string
}

func (s *Service) usesProfileScopedTitleIdentity(ctx context.Context, input ResolveTitleInput) bool {
	ctx = s.pinProviders(ctx)
	providers := watchstateProviders(ctx)
	if providers.Canonical == nil || input.Provider == "addon" {
		return true
	}
	if input.Provider == "tmdb" {
		return false
	}
	if providers.Resolver == nil {
		return true
	}
	switch input.Provider {
	case "imdb", "tvdb":
		return false
	default:
		return true
	}
}

func (s *Service) resolveCanonicalTitle(ctx context.Context, input ResolveTitleInput) (canonicalTitleSnapshot, error) {
	ctx = s.pinProviders(ctx)
	providers := watchstateProviders(ctx)
	if providers.Canonical == nil {
		return canonicalTitleSnapshot{}, errors.New("canonical title provider unavailable")
	}
	canonicalID := input.ExternalID
	if input.Provider != "tmdb" {
		if providers.Resolver == nil {
			return canonicalTitleSnapshot{}, fmt.Errorf("%w: title provider identity cannot be verified", ErrInvalidInput)
		}
		resolvedID, err := providers.Resolver.ResolveExternalID(ctx, input.MediaType, input.Provider, input.ExternalID)
		if err != nil {
			if errors.Is(err, metadata.ErrProviderNotFound) {
				return canonicalTitleSnapshot{}, fmt.Errorf("%w: title provider identity cannot be verified", ErrInvalidInput)
			}
			return canonicalTitleSnapshot{}, errors.New("canonical title provider unavailable")
		}
		canonicalID = strings.TrimSpace(resolvedID)
	}
	if canonicalID == "" || len(canonicalID) > 512 {
		return canonicalTitleSnapshot{}, fmt.Errorf("%w: canonical title identity is invalid", ErrInvalidInput)
	}

	var snapshot canonicalTitleSnapshot
	additionalIDs := map[string]string{}
	switch input.MediaType {
	case "movie":
		provided, err := providers.Canonical.MovieDetails(ctx, canonicalID, "en-US")
		if err != nil {
			if errors.Is(err, metadata.ErrProviderNotFound) {
				return canonicalTitleSnapshot{}, fmt.Errorf("%w: title provider identity cannot be verified", ErrInvalidInput)
			}
			return canonicalTitleSnapshot{}, errors.New("canonical title provider unavailable")
		}
		if strings.TrimSpace(provided.ExternalID) != canonicalID {
			return canonicalTitleSnapshot{}, fmt.Errorf("%w: canonical title identity conflicts with provider metadata", ErrInvalidInput)
		}
		snapshot.title = strings.TrimSpace(provided.Title)
		snapshot.posterURL = strings.TrimSpace(provided.PosterURL)
		snapshot.backgroundURL = strings.TrimSpace(provided.BackdropURL)
		snapshot.released = strings.TrimSpace(provided.ReleaseDate)
		additionalIDs = provided.AdditionalIDs
	case "series":
		provided, err := providers.Canonical.SeriesDetails(ctx, canonicalID, "en-US")
		if err != nil {
			if errors.Is(err, metadata.ErrProviderNotFound) {
				return canonicalTitleSnapshot{}, fmt.Errorf("%w: title provider identity cannot be verified", ErrInvalidInput)
			}
			return canonicalTitleSnapshot{}, errors.New("canonical title provider unavailable")
		}
		if strings.TrimSpace(provided.ExternalID) != canonicalID {
			return canonicalTitleSnapshot{}, fmt.Errorf("%w: canonical title identity conflicts with provider metadata", ErrInvalidInput)
		}
		snapshot.title = strings.TrimSpace(provided.Name)
		snapshot.posterURL = strings.TrimSpace(provided.PosterURL)
		snapshot.backgroundURL = strings.TrimSpace(provided.BackdropURL)
		snapshot.released = strings.TrimSpace(provided.FirstAirDate)
		additionalIDs = provided.AdditionalIDs
	}
	if len(snapshot.title) < 1 || len(snapshot.title) > 500 || len(snapshot.posterURL) > 4096 || len(snapshot.backgroundURL) > 4096 {
		return canonicalTitleSnapshot{}, errors.New("canonical title provider returned an invalid snapshot")
	}
	if snapshot.released != "" {
		released, err := time.Parse(time.DateOnly, snapshot.released)
		if err != nil || released.Year() < 1 || released.Format(time.DateOnly) != snapshot.released {
			return canonicalTitleSnapshot{}, errors.New("canonical title provider returned an invalid snapshot")
		}
		snapshot.releaseInfo = strconv.Itoa(released.Year())
	}

	resolvedIDs := make(map[string]string, len(additionalIDs)+1)
	resolvedIDs["tmdb"] = canonicalID
	for provider, externalID := range additionalIDs {
		provider = strings.ToLower(strings.TrimSpace(provider))
		externalID = strings.TrimSpace(externalID)
		if !providerPattern.MatchString(provider) || externalID == "" || len(externalID) > 512 {
			continue
		}
		if existing, exists := resolvedIDs[provider]; exists && existing != externalID {
			return canonicalTitleSnapshot{}, fmt.Errorf("%w: canonical title identity conflicts with provider metadata", ErrInvalidInput)
		}
		resolvedIDs[provider] = externalID
	}
	if resolvedIDs[input.Provider] != input.ExternalID {
		return canonicalTitleSnapshot{}, fmt.Errorf("%w: canonical title identity conflicts with provider metadata", ErrInvalidInput)
	}
	providerNames := make([]string, 0, len(resolvedIDs))
	for provider := range resolvedIDs {
		providerNames = append(providerNames, provider)
	}
	sort.Strings(providerNames)
	snapshot.identities = make([]canonicalTitleIdentity, 0, len(providerNames))
	for _, provider := range providerNames {
		externalID := resolvedIDs[provider]
		snapshot.identities = append(snapshot.identities, canonicalTitleIdentity{
			provider: provider, externalID: externalID, storedID: storedTitleExternalID(externalID),
		})
	}
	snapshot.resource = "tmdb"
	snapshot.resourceID = canonicalID
	if imdbID := resolvedIDs["imdb"]; imdbID != "" {
		snapshot.resource = "imdb"
		snapshot.resourceID = imdbID
	}
	return snapshot, nil
}

func storedTitleExternalID(externalID string) string {
	if len(externalID) <= 128 {
		return externalID
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(externalID)))
}

func (s *Service) authorizeCanonicalTitleResolution(ctx context.Context, principal auth.Principal, linked bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin title resolution authorization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if linked {
		_, err = s.mutationProfileID(ctx, tx, principal, true)
	} else {
		_, err = authorizedActiveProfileID(ctx, tx, principal)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit title resolution authorization: %w", err)
	}
	return nil
}

func (s *Service) persistProfileScopedTitle(ctx context.Context, principal auth.Principal, input ResolveTitleInput, requireManagement, linked bool) (TitleReference, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TitleReference{}, fmt.Errorf("begin profile title resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var profileID string
	if linked {
		profileID, err = s.mutationProfileID(ctx, tx, principal, true)
	} else if requireManagement {
		profileID, err = authorizedActiveManagerProfileID(ctx, tx, principal)
	} else {
		profileID, err = authorizedActiveProfileID(ctx, tx, principal)
	}
	if err != nil {
		return TitleReference{}, err
	}
	if err := lockProfileTitleIdentities(ctx, tx, profileID); err != nil {
		return TitleReference{}, err
	}
	if input.SourceAddonID != "" {
		var lockedAddonID string
		if err := tx.QueryRow(ctx, `
			/* watchstate.lock_profile_title_addon */
			SELECT addon.id::text
			FROM profile_addons addon
			JOIN profiles profile ON profile.id = $2::uuid
			WHERE addon.id = $1::uuid
			  AND addon.enabled = true
			  AND (
			      EXISTS (
			          SELECT 1 FROM addon_profile_access access
			          WHERE access.addon_id = addon.id AND access.profile_id = profile.id
			      ) OR EXISTS (
			          SELECT 1 FROM addon_category_access access
			          WHERE access.addon_id = addon.id AND access.category_id = profile.category_id
			      )
			  )
			FOR SHARE OF addon
		`, input.SourceAddonID, profileID).Scan(&lockedAddonID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return TitleReference{}, ErrNotFound
			}
			return TitleReference{}, fmt.Errorf("lock profile title addon access: %w", err)
		}
	}

	var titleID string
	err = tx.QueryRow(ctx, `
		SELECT identity.title_id::text
		FROM profile_title_external_ids identity
		JOIN titles title ON title.id = identity.title_id
		WHERE identity.profile_id = $1::uuid
		  AND identity.provider = $2
		  AND identity.namespace = $3
		  AND identity.external_id = $4
		FOR UPDATE OF title
	`, profileID, input.Provider, input.MediaType, input.ExternalID).Scan(&titleID)
	if errors.Is(err, pgx.ErrNoRows) {
		var identityCount int64
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM profile_title_external_ids
			WHERE profile_id = $1::uuid
		`, profileID).Scan(&identityCount); err != nil {
			return TitleReference{}, fmt.Errorf("check profile title identity capacity: %w", err)
		}
		if identityCount >= maximumProfileTitleIdentitiesPerProfile {
			return TitleReference{}, fmt.Errorf("%w: profile title identity capacity reached", ErrInvalidInput)
		}
		if input.SourceAddonID == "" {
			if err := tx.QueryRow(ctx, `
				INSERT INTO titles (
					media_type, display_title, poster_url, background_url, release_info,
					release_date, resource_id, resource_provider
				)
				VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''),
				        NULLIF($6, '')::date, $7, $8)
				RETURNING id::text
			`, input.MediaType, input.Title, input.PosterURL, input.BackgroundURL,
				input.ReleaseInfo, input.Released, input.ResourceID, input.Provider).Scan(&titleID); err != nil {
				return TitleReference{}, fmt.Errorf("create profile-scoped title: %w", err)
			}
		} else {
			if err := tx.QueryRow(ctx, `
				INSERT INTO titles (
					media_type, display_title, poster_url, background_url, release_info,
					release_date, resource_id, resource_provider, source_addon_id,
					source_catalog_id, source_name
				)
				VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''),
				        NULLIF($6, '')::date, $7, $8, $9::uuid, $10, $11)
				RETURNING id::text
			`, input.MediaType, input.Title, input.PosterURL, input.BackgroundURL,
				input.ReleaseInfo, input.Released, input.ResourceID, input.Provider,
				input.SourceAddonID, input.SourceCatalogID, input.SourceName).Scan(&titleID); err != nil {
				return TitleReference{}, fmt.Errorf("create profile-scoped addon title: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO profile_title_external_ids (
				profile_id, title_id, provider, namespace, external_id
			)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		`, profileID, titleID, input.Provider, input.MediaType, input.ExternalID); err != nil {
			return TitleReference{}, fmt.Errorf("store profile-scoped title identity: %w", err)
		}
	} else if err != nil {
		return TitleReference{}, fmt.Errorf("find profile-scoped title identity: %w", err)
	} else {
		if input.SourceAddonID == "" {
			if _, err := tx.Exec(ctx, `
				UPDATE titles
				SET display_title = $2,
				    poster_url = COALESCE(NULLIF($3, ''), poster_url),
				    background_url = COALESCE(NULLIF($4, ''), background_url),
				    release_info = COALESCE(NULLIF($5, ''), release_info),
				    release_date = COALESCE(NULLIF($6, '')::date, release_date),
				    resource_id = $7,
				    resource_provider = $8,
				    updated_at = now()
				WHERE id = $1::uuid
			`, titleID, input.Title, input.PosterURL, input.BackgroundURL, input.ReleaseInfo,
				input.Released, input.ResourceID, input.Provider); err != nil {
				return TitleReference{}, fmt.Errorf("update profile-scoped title snapshot: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE titles
				SET display_title = $2,
				    poster_url = COALESCE(NULLIF($3, ''), poster_url),
				    background_url = COALESCE(NULLIF($4, ''), background_url),
				    release_info = COALESCE(NULLIF($5, ''), release_info),
				    release_date = COALESCE(NULLIF($6, '')::date, release_date),
				    resource_id = $7,
				    resource_provider = $8,
				    source_addon_id = $9::uuid,
				    source_catalog_id = $10,
				    source_name = $11,
				    updated_at = now()
				WHERE id = $1::uuid
			`, titleID, input.Title, input.PosterURL, input.BackgroundURL, input.ReleaseInfo,
				input.Released, input.ResourceID, input.Provider, input.SourceAddonID,
				input.SourceCatalogID, input.SourceName); err != nil {
				return TitleReference{}, fmt.Errorf("update profile-scoped addon title snapshot: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return TitleReference{}, fmt.Errorf("commit profile title resolution: %w", err)
	}
	return TitleReference{
		TitleID: titleID, MediaType: input.MediaType, Provider: input.Provider,
		ExternalID: input.ExternalID, ResourceID: input.ResourceID, Title: input.Title,
		PosterURL: input.PosterURL, BackgroundURL: input.BackgroundURL, ReleaseInfo: input.ReleaseInfo,
		SourceAddonID: input.SourceAddonID, SourceCatalogID: input.SourceCatalogID, SourceName: input.SourceName,
	}, nil
}
func (s *Service) persistCanonicalTitle(ctx context.Context, principal auth.Principal, input ResolveTitleInput, canonical canonicalTitleSnapshot, linked bool) (TitleReference, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TitleReference{}, fmt.Errorf("begin title resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if linked {
		_, err = s.mutationProfileID(ctx, tx, principal, true)
	} else {
		_, err = authorizedActiveProfileID(ctx, tx, principal)
	}
	if err != nil {
		return TitleReference{}, err
	}
	for _, identity := range canonical.identities {
		lockKey := identity.provider + ":" + input.MediaType + ":" + identity.storedID
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
			return TitleReference{}, fmt.Errorf("lock title resolution: %w", err)
		}
	}

	providers := make([]string, len(canonical.identities))
	externalIDs := make([]string, len(canonical.identities))
	for index, identity := range canonical.identities {
		providers[index] = identity.provider
		externalIDs[index] = identity.storedID
	}
	rows, err := tx.Query(ctx, `
		SELECT external.title_id::text
		FROM unnest($1::text[], $2::text[]) AS requested(provider, external_id)
		JOIN title_external_ids external
		  ON external.provider = requested.provider
		 AND external.namespace = $3
		 AND external.external_id = requested.external_id
		JOIN titles title ON title.id = external.title_id
		FOR UPDATE OF title
	`, providers, externalIDs, input.MediaType)
	if err != nil {
		return TitleReference{}, fmt.Errorf("find canonical title identities: %w", err)
	}
	titleIDs := make([]string, 0, 1)
	seenTitleIDs := make(map[string]struct{}, 1)
	for rows.Next() {
		var titleID string
		if err := rows.Scan(&titleID); err != nil {
			rows.Close()
			return TitleReference{}, fmt.Errorf("scan canonical title identity: %w", err)
		}
		if _, exists := seenTitleIDs[titleID]; !exists {
			seenTitleIDs[titleID] = struct{}{}
			titleIDs = append(titleIDs, titleID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TitleReference{}, fmt.Errorf("read canonical title identities: %w", err)
	}
	rows.Close()
	if len(titleIDs) > 1 {
		return TitleReference{}, ErrConflict
	}

	var titleID string
	if len(titleIDs) == 0 {
		if err := tx.QueryRow(ctx, `
			INSERT INTO titles (
				media_type, display_title, poster_url, background_url, release_info,
				release_date, resource_id, resource_provider
			)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''),
			        NULLIF($6, '')::date, $7, $8)
			RETURNING id::text
		`, input.MediaType, canonical.title, canonical.posterURL, canonical.backgroundURL,
			canonical.releaseInfo, canonical.released, canonical.resourceID, canonical.resource).Scan(&titleID); err != nil {
			return TitleReference{}, fmt.Errorf("create canonical title: %w", err)
		}
	} else {
		titleID = titleIDs[0]
		var identityConflict bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM title_external_ids existing
				JOIN unnest($2::text[], $3::text[]) AS canonical(provider, external_id)
				  ON canonical.provider = existing.provider
				WHERE existing.title_id = $1::uuid
				  AND existing.namespace = $4
				  AND existing.external_id <> canonical.external_id
			)
		`, titleID, providers, externalIDs, input.MediaType).Scan(&identityConflict); err != nil {
			return TitleReference{}, fmt.Errorf("check canonical title identity conflict: %w", err)
		}
		if identityConflict {
			return TitleReference{}, ErrConflict
		}
		if _, err := tx.Exec(ctx, `
			UPDATE titles
			SET display_title = $2,
			    poster_url = COALESCE(NULLIF($3, ''), poster_url),
			    background_url = COALESCE(NULLIF($4, ''), background_url),
			    release_info = COALESCE(NULLIF($5, ''), release_info),
			    release_date = COALESCE(NULLIF($6, '')::date, release_date),
			    resource_id = $7,
			    resource_provider = $8,
			    updated_at = now()
			WHERE id = $1::uuid
		`, titleID, canonical.title, canonical.posterURL, canonical.backgroundURL,
			canonical.releaseInfo, canonical.released, canonical.resourceID, canonical.resource); err != nil {
			return TitleReference{}, fmt.Errorf("update canonical title: %w", err)
		}
	}
	for _, identity := range canonical.identities {
		if _, err := tx.Exec(ctx, `
			INSERT INTO title_external_ids (title_id, provider, external_id, namespace)
			VALUES ($1::uuid, $2, $3, $4)
			ON CONFLICT (provider, namespace, external_id) DO NOTHING
		`, titleID, identity.provider, identity.storedID, input.MediaType); err != nil {
			return TitleReference{}, fmt.Errorf("store canonical title identity: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return TitleReference{}, fmt.Errorf("commit title resolution: %w", err)
	}
	return TitleReference{
		TitleID: titleID, MediaType: input.MediaType, Provider: input.Provider,
		ExternalID: input.ExternalID, ResourceID: canonical.resourceID, Title: canonical.title,
		PosterURL: canonical.posterURL, BackgroundURL: canonical.backgroundURL, ReleaseInfo: canonical.releaseInfo,
	}, nil
}

func (s *Service) resolveTVTitle(ctx context.Context, principal auth.Principal, input ResolveTitleInput, linked bool) (TitleReference, error) {
	storedExternalID := storedTitleExternalID(input.ExternalID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TitleReference{}, fmt.Errorf("begin title resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var profileID string
	if linked {
		profileID, err = s.mutationProfileID(ctx, tx, principal, true)
	} else {
		profileID, err = authorizedActiveManagerProfileID(ctx, tx, principal)
	}
	if err != nil {
		return TitleReference{}, err
	}
	var lockedAddonID string
	if err := tx.QueryRow(ctx, `
		/* watchstate.lock_tv_addon */
		SELECT addon.id::text
		FROM profile_addons addon
		JOIN profiles profile ON profile.id = $2::uuid
		WHERE addon.id = $1::uuid
		  AND addon.enabled = true
		  AND (
		      EXISTS (
		          SELECT 1
		          FROM addon_profile_access access
		          WHERE access.addon_id = addon.id
		            AND access.profile_id = profile.id
		      ) OR EXISTS (
		          SELECT 1
		          FROM addon_category_access access
		          WHERE access.addon_id = addon.id
		            AND access.category_id = profile.category_id
		      )
		  )
		FOR SHARE OF addon
	`, input.SourceAddonID, profileID).Scan(&lockedAddonID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TitleReference{}, ErrNotFound
		}
		return TitleReference{}, fmt.Errorf("lock TV addon access: %w", err)
	}
	lockKey := input.Provider + ":" + input.MediaType + ":" + storedExternalID
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return TitleReference{}, fmt.Errorf("lock title resolution: %w", err)
	}

	var titleID string
	err = tx.QueryRow(ctx, `
		SELECT external.title_id::text
		FROM title_external_ids external
		JOIN titles title ON title.id = external.title_id
		WHERE external.provider = $1 AND external.namespace = $2 AND external.external_id = $3
		FOR UPDATE OF title
	`, input.Provider, input.MediaType, storedExternalID).Scan(&titleID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			INSERT INTO titles (
				media_type, display_title, poster_url, background_url, release_info,
				release_date, resource_id, resource_provider, source_addon_id,
				source_catalog_id, source_name, country, language, category
			)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''),
			        NULLIF($6, '')::date, $7, $8, NULLIF($9, '')::uuid,
			        NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''),
			        NULLIF($13, ''), NULLIF($14, ''))
			RETURNING id::text
		`, input.MediaType, input.Title, input.PosterURL, input.BackgroundURL, input.ReleaseInfo,
			input.Released, input.ResourceID, input.Provider, input.SourceAddonID,
			input.SourceCatalogID, input.SourceName, input.Country, input.Language, input.Category).Scan(&titleID); err != nil {
			return TitleReference{}, fmt.Errorf("create resolved title: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO title_external_ids (title_id, provider, external_id, namespace)
			VALUES ($1::uuid, $2, $3, $4)
		`, titleID, input.Provider, storedExternalID, input.MediaType); err != nil {
			return TitleReference{}, fmt.Errorf("store resolved title identity: %w", err)
		}
	} else if err != nil {
		return TitleReference{}, fmt.Errorf("find resolved title: %w", err)
	} else {
		if err := authorizeTitleSnapshotProfiles(ctx, tx, principal, profileID, titleID); err != nil {
			return TitleReference{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE titles
			SET display_title = COALESCE(display_title, $2),
			    poster_url = COALESCE(poster_url, NULLIF($3, '')),
			    background_url = COALESCE(background_url, NULLIF($4, '')),
			    release_info = COALESCE(release_info, NULLIF($5, '')),
			    release_date = COALESCE(release_date, NULLIF($6, '')::date),
			    resource_id = $7,
			    resource_provider = $8,
			    source_addon_id = NULLIF($9, '')::uuid,
			    source_catalog_id = COALESCE(NULLIF($10, ''), source_catalog_id),
			    source_name = COALESCE(NULLIF($11, ''), source_name),
			    country = COALESCE(NULLIF($12, ''), country),
			    language = COALESCE(NULLIF($13, ''), language),
			    category = COALESCE(NULLIF($14, ''), category),
			    updated_at = now()
			WHERE id = $1::uuid
		`, titleID, input.Title, input.PosterURL, input.BackgroundURL, input.ReleaseInfo,
			input.Released, input.ResourceID, input.Provider, input.SourceAddonID,
			input.SourceCatalogID, input.SourceName, input.Country, input.Language, input.Category); err != nil {
			return TitleReference{}, fmt.Errorf("update resolved title snapshot: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return TitleReference{}, fmt.Errorf("commit title resolution: %w", err)
	}
	return TitleReference{
		TitleID: titleID, MediaType: input.MediaType, Provider: input.Provider,
		ExternalID: input.ExternalID, ResourceID: input.ResourceID, Title: input.Title,
		PosterURL: input.PosterURL, BackgroundURL: input.BackgroundURL, ReleaseInfo: input.ReleaseInfo,
		SourceAddonID: input.SourceAddonID, SourceCatalogID: input.SourceCatalogID,
		SourceName: input.SourceName, Country: input.Country, Language: input.Language, Category: input.Category,
	}, nil
}

func (s *Service) AddLibrary(ctx context.Context, principal auth.Principal, titleID string) (LibraryItem, error) {
	titleID, err := normalizeTitleID(titleID)
	if err != nil {
		return LibraryItem{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LibraryItem{}, fmt.Errorf("begin library addition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return LibraryItem{}, err
	}
	if err := lockUserDataMutation(ctx, tx, profileID, titleID); err != nil {
		return LibraryItem{}, err
	}
	mediaType, err := accessibleTitleMediaType(ctx, tx, profileID, titleID)
	if err != nil {
		return LibraryItem{}, err
	}
	if mediaType != "movie" && mediaType != "series" && mediaType != "tv" {
		return LibraryItem{}, fmt.Errorf("%w: library titles must be movies, series, or TV channels", ErrInvalidInput)
	}
	var item LibraryItem
	err = tx.QueryRow(ctx, `
		WITH library AS (
			INSERT INTO profile_library (profile_id, title_id)
			VALUES ($1::uuid, $2::uuid)
			ON CONFLICT (profile_id, title_id) DO UPDATE SET updated_at = now()
			RETURNING added_at, updated_at
		)
		SELECT title.id::text, title.media_type,
		       COALESCE(title.resource_provider, ''),
		       COALESCE(scoped_identity.external_id, global_identity.external_id, ''),
		       COALESCE(title.resource_id, ''), COALESCE(title.display_title, ''),
		       COALESCE(title.poster_url, ''), COALESCE(title.background_url, ''),
		       COALESCE(title.release_info, ''), COALESCE(title.source_addon_id::text, ''),
		       COALESCE(title.source_catalog_id, ''), COALESCE(title.source_name, ''),
		       COALESCE(title.country, ''), COALESCE(title.language, ''),
		       COALESCE(title.category, ''),
		       title.media_type <> 'tv' OR EXISTS (
		           SELECT 1
		           FROM profile_addons addon
		           JOIN profiles profile ON profile.id = $1::uuid
		           WHERE addon.id = title.source_addon_id
		             AND addon.enabled = true
		             AND (
		                 EXISTS (
		                     SELECT 1
		                     FROM addon_profile_access access
		                     WHERE access.addon_id = addon.id
		                       AND access.profile_id = profile.id
		                 ) OR EXISTS (
		                     SELECT 1
		                     FROM addon_category_access access
		                     WHERE access.addon_id = addon.id
		                       AND access.category_id = profile.category_id
		                 )
		             )
		       ),
		       library.added_at, library.updated_at
		FROM titles title
		CROSS JOIN library
		LEFT JOIN title_external_ids global_identity
		  ON global_identity.title_id = title.id
		 AND global_identity.provider = title.resource_provider
		 AND global_identity.namespace = title.media_type
		LEFT JOIN profile_title_external_ids scoped_identity
		  ON scoped_identity.title_id = title.id
		 AND scoped_identity.profile_id = $1::uuid
		 AND scoped_identity.provider = title.resource_provider
		 AND scoped_identity.namespace = title.media_type
		WHERE title.id = $2::uuid
	`, profileID, titleID).Scan(
		&item.TitleID, &item.MediaType, &item.Provider, &item.ExternalID,
		&item.ResourceID, &item.Title, &item.PosterURL, &item.BackgroundURL,
		&item.ReleaseInfo, &item.SourceAddonID, &item.SourceCatalogID, &item.SourceName,
		&item.Country, &item.Language, &item.Category, &item.Available,
		&item.AddedAt, &item.UpdatedAt,
	)
	if err != nil {
		return LibraryItem{}, fmt.Errorf("add library title: %w", err)
	}
	if mediaType != "tv" {
		if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("library:add:%s:%d", titleID, item.UpdatedAt.UnixNano()), tracking.Event{
			Type: "library", TitleID: titleID, InLibrary: true, OccurredAt: item.UpdatedAt,
		}); err != nil {
			return LibraryItem{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return LibraryItem{}, fmt.Errorf("commit library addition: %w", err)
	}
	return item, nil
}

func (s *Service) RemoveLibrary(ctx context.Context, principal auth.Principal, titleID string) error {
	titleID, err := normalizeTitleID(titleID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin library removal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return err
	}
	if err := lockUserDataMutation(ctx, tx, profileID, titleID); err != nil {
		return err
	}
	mediaType, err := accessibleTitleMediaType(ctx, tx, profileID, titleID)
	if errors.Is(err, ErrNotFound) {
		if err == errAddonAccessChanged {
			return ErrNotFound
		}
		err = tx.QueryRow(ctx, `
			SELECT title.media_type
			FROM profile_library library
			JOIN titles title ON title.id = library.title_id
			WHERE library.profile_id = $1::uuid
			  AND library.title_id = $2::uuid
			  AND title.media_type = 'tv'
		`, profileID, titleID).Scan(&mediaType)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM profile_library WHERE profile_id = $1::uuid AND title_id = $2::uuid`, profileID, titleID); err != nil {
		return fmt.Errorf("remove library title: %w", err)
	}
	if mediaType != "tv" {
		occurredAt := time.Now().UTC()
		if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("library:remove:%s:%d", titleID, occurredAt.UnixNano()), tracking.Event{
			Type: "library", TitleID: titleID, InLibrary: false, OccurredAt: occurredAt,
		}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit library removal: %w", err)
	}
	return nil
}

func (s *Service) Library(ctx context.Context, principal auth.Principal, mediaType string, page, pageSize int) (LibraryPage, error) {
	var err error
	mediaType, page, pageSize, err = normalizeLibraryQuery(mediaType, page, pageSize)
	if err != nil {
		return LibraryPage{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LibraryPage{}, fmt.Errorf("begin library query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return LibraryPage{}, err
	}

	var totalResults int
	if err := tx.QueryRow(ctx, `
		WITH accessible_titles AS (`+accessibleTitlesSQL+`)
		SELECT count(*)
		FROM profile_library library
		JOIN titles title ON title.id = library.title_id
		LEFT JOIN accessible_titles accessible ON accessible.id = title.id
		WHERE library.profile_id = $1::uuid
		  AND ($2 = '' OR title.media_type = $2)
		  AND (title.media_type = 'tv' OR accessible.id IS NOT NULL)
	`, profileID, mediaType).Scan(&totalResults); err != nil {
		return LibraryPage{}, fmt.Errorf("count library titles: %w", err)
	}
	rows, err := tx.Query(ctx, `
		WITH accessible_titles AS (`+accessibleTitlesSQL+`)
		SELECT title.id::text, title.media_type,
		       COALESCE(title.resource_provider, ''),
		       COALESCE(scoped_identity.external_id, global_identity.external_id, ''),
		       COALESCE(title.resource_id, ''), COALESCE(title.display_title, ''),
		       COALESCE(title.poster_url, ''), COALESCE(title.background_url, ''),
		       COALESCE(title.release_info, ''), COALESCE(title.source_addon_id::text, ''),
		       COALESCE(title.source_catalog_id, ''), COALESCE(title.source_name, ''),
		       COALESCE(title.country, ''), COALESCE(title.language, ''),
		       COALESCE(title.category, ''),
		       title.media_type <> 'tv' OR EXISTS (
		           SELECT 1
		           FROM profile_addons addon
		           JOIN profiles profile ON profile.id = library.profile_id
		           WHERE addon.id = title.source_addon_id
		             AND addon.enabled = true
		             AND (
		                 EXISTS (
		                     SELECT 1
		                     FROM addon_profile_access access
		                     WHERE access.addon_id = addon.id
		                       AND access.profile_id = profile.id
		                 ) OR EXISTS (
		                     SELECT 1
		                     FROM addon_category_access access
		                     WHERE access.addon_id = addon.id
		                       AND access.category_id = profile.category_id
		                 )
		             )
		       ),
		       library.added_at, library.updated_at
		FROM profile_library library
		JOIN titles title ON title.id = library.title_id
		LEFT JOIN accessible_titles accessible ON accessible.id = title.id
		LEFT JOIN title_external_ids global_identity
		  ON global_identity.title_id = title.id
		 AND global_identity.provider = title.resource_provider
		 AND global_identity.namespace = title.media_type
		LEFT JOIN profile_title_external_ids scoped_identity
		  ON scoped_identity.title_id = title.id
		 AND scoped_identity.profile_id = library.profile_id
		 AND scoped_identity.provider = title.resource_provider
		 AND scoped_identity.namespace = title.media_type
		WHERE library.profile_id = $1::uuid
		  AND ($2 = '' OR title.media_type = $2)
		  AND (title.media_type = 'tv' OR accessible.id IS NOT NULL)
		ORDER BY library.added_at DESC, title.id
		LIMIT $3 OFFSET $4
	`, profileID, mediaType, pageSize, (page-1)*pageSize)
	if err != nil {
		return LibraryPage{}, fmt.Errorf("query library titles: %w", err)
	}
	defer rows.Close()
	items := make([]LibraryItem, 0)
	for rows.Next() {
		var item LibraryItem
		if err := rows.Scan(
			&item.TitleID, &item.MediaType, &item.Provider, &item.ExternalID,
			&item.ResourceID, &item.Title, &item.PosterURL, &item.BackgroundURL,
			&item.ReleaseInfo, &item.SourceAddonID, &item.SourceCatalogID, &item.SourceName,
			&item.Country, &item.Language, &item.Category, &item.Available,
			&item.AddedAt, &item.UpdatedAt,
		); err != nil {
			return LibraryPage{}, fmt.Errorf("scan library title: %w", err)
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return LibraryPage{}, fmt.Errorf("iterate library titles: %w", err)
	}
	totalPages := 0
	if totalResults > 0 {
		totalPages = (totalResults + pageSize - 1) / pageSize
	}
	pageResult := LibraryPage{Items: items, Page: page, TotalPages: totalPages, TotalResults: totalResults}
	if err := tx.Commit(ctx); err != nil {
		return LibraryPage{}, fmt.Errorf("commit library query: %w", err)
	}
	return pageResult, nil
}

func (s *Service) TVLibraryMembership(ctx context.Context, principal auth.Principal, identities []TVLibraryIdentity) (TVLibraryMembershipResult, error) {
	if len(identities) < 1 || len(identities) > MaximumTVLibraryMembershipIdentities {
		return TVLibraryMembershipResult{}, fmt.Errorf("%w: identities must contain between 1 and %d items", ErrInvalidInput, MaximumTVLibraryMembershipIdentities)
	}
	type identityKey struct {
		sourceAddonID string
		resourceID    string
	}
	seen := make(map[identityKey]struct{}, len(identities))
	sourceAddonIDs := make([]string, 0, len(identities))
	resourceIDs := make([]string, 0, len(identities))
	externalIDs := make([]string, 0, len(identities))
	for _, identity := range identities {
		sourceAddonID := strings.ToLower(strings.TrimSpace(identity.SourceAddonID))
		resourceID := strings.TrimSpace(identity.ResourceID)
		if !uuidPattern.MatchString(sourceAddonID) {
			return TVLibraryMembershipResult{}, fmt.Errorf("%w: sourceAddonId must be a UUID", ErrInvalidInput)
		}
		if len(resourceID) < 1 || len(resourceID) > 512 {
			return TVLibraryMembershipResult{}, fmt.Errorf("%w: invalid resource identifier", ErrInvalidInput)
		}
		key := identityKey{sourceAddonID: sourceAddonID, resourceID: resourceID}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		sourceAddonIDs = append(sourceAddonIDs, sourceAddonID)
		resourceIDs = append(resourceIDs, resourceID)
		externalIDs = append(externalIDs, tvExternalID(sourceAddonID, resourceID))
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TVLibraryMembershipResult{}, fmt.Errorf("begin TV library membership query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return TVLibraryMembershipResult{}, err
	}
	rows, err := tx.Query(ctx, `
		WITH requested AS (
			SELECT source_addon_id, resource_id, external_id, ordinal
			FROM unnest($2::uuid[], $3::text[], $4::text[]) WITH ORDINALITY
			     AS requested_identity(source_addon_id, resource_id, external_id, ordinal)
		)
		SELECT requested.source_addon_id::text, requested.resource_id, identity.title_id::text
		FROM requested
		JOIN title_external_ids identity
		  ON identity.provider = 'addon'
		 AND identity.namespace = 'tv'
		 AND identity.external_id = requested.external_id
		JOIN profile_library library
		  ON library.title_id = identity.title_id
		 AND library.profile_id = $1::uuid
		ORDER BY requested.ordinal, identity.title_id
	`, profileID, sourceAddonIDs, resourceIDs, externalIDs)
	if err != nil {
		return TVLibraryMembershipResult{}, fmt.Errorf("query TV library membership: %w", err)
	}
	defer rows.Close()
	items := make([]TVLibraryMembership, 0, len(sourceAddonIDs))
	for rows.Next() {
		var item TVLibraryMembership
		if err := rows.Scan(&item.SourceAddonID, &item.ResourceID, &item.TitleID); err != nil {
			return TVLibraryMembershipResult{}, fmt.Errorf("scan TV library membership: %w", err)
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return TVLibraryMembershipResult{}, fmt.Errorf("iterate TV library membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TVLibraryMembershipResult{}, fmt.Errorf("commit TV library membership query: %w", err)
	}
	return TVLibraryMembershipResult{Items: items}, nil
}

func (s *Service) GetProgress(ctx context.Context, principal auth.Principal, titleID string) (Progress, error) {
	titleID, err := normalizeTitleID(titleID)
	if err != nil {
		return Progress{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Progress{}, fmt.Errorf("begin playback progress query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return Progress{}, err
	}
	if _, err := accessibleTitleMediaType(ctx, tx, profileID, titleID); err != nil {
		return Progress{}, err
	}
	progress, err := progressForProfile(ctx, tx, profileID, titleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrProgressNotFound
	}
	if err != nil {
		return Progress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Progress{}, fmt.Errorf("commit playback progress query: %w", err)
	}
	return progress, nil
}

func (s *Service) GetProgressBatch(ctx context.Context, principal auth.Principal, titleIDs []string) (ProgressBatch, error) {
	titleIDs, err := normalizeProgressBatchTitleIDs(titleIDs)
	if err != nil {
		return ProgressBatch{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProgressBatch{}, fmt.Errorf("begin playback progress batch query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return ProgressBatch{}, err
	}
	rows, err := tx.Query(ctx, `
		/* watchstate.progress_batch */
		WITH accessible_titles AS (`+accessibleTitlesSQL+`),
		input AS (
			SELECT title_id, ordinality
			FROM unnest($2::uuid[]) WITH ORDINALITY AS requested(title_id, ordinality)
		)
		SELECT input.ordinality, input.title_id::text, title.media_type,
		       progress.title_id::text, progress.position_seconds, progress.duration_seconds,
		       progress.completed, progress.version, progress.last_watched_at, progress.updated_at
		FROM input
		LEFT JOIN accessible_titles title ON title.id = input.title_id
		LEFT JOIN profile_progress progress
		  ON progress.profile_id = $1::uuid
		 AND progress.title_id = input.title_id
		 AND title.id IS NOT NULL
		ORDER BY input.ordinality
	`, profileID, titleIDs)
	if err != nil {
		return ProgressBatch{}, fmt.Errorf("query playback progress batch: %w", err)
	}
	defer rows.Close()
	items := make([]ProgressBatchItem, 0, len(titleIDs))
	for rows.Next() {
		var (
			ordinal         int
			titleID         string
			mediaType       *string
			progressTitleID *string
			position        *int
			duration        *int
			completed       *bool
			version         *int64
			lastWatchedAt   *time.Time
			updatedAt       *time.Time
		)
		if err := rows.Scan(
			&ordinal, &titleID, &mediaType, &progressTitleID, &position, &duration,
			&completed, &version, &lastWatchedAt, &updatedAt,
		); err != nil {
			return ProgressBatch{}, fmt.Errorf("scan playback progress batch: %w", err)
		}
		if ordinal != len(items)+1 {
			return ProgressBatch{}, fmt.Errorf("playback progress batch order mismatch")
		}
		if mediaType == nil {
			return ProgressBatch{}, ErrNotFound
		}
		item := ProgressBatchItem{TitleID: titleID}
		if progressTitleID != nil {
			item.Progress = &Progress{
				TitleID:         *progressTitleID,
				MediaType:       *mediaType,
				PositionSeconds: *position,
				DurationSeconds: *duration,
				Completed:       *completed,
				Version:         *version,
				LastWatchedAt:   *lastWatchedAt,
				UpdatedAt:       *updatedAt,
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ProgressBatch{}, fmt.Errorf("iterate playback progress batch: %w", err)
	}
	if len(items) != len(titleIDs) {
		return ProgressBatch{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return ProgressBatch{}, fmt.Errorf("commit playback progress batch query: %w", err)
	}
	return ProgressBatch{Items: items}, nil
}

func (s *Service) SetWatchedBatch(ctx context.Context, principal auth.Principal, input []SetWatchedBatchItem) (ProgressBatch, error) {
	input, err := normalizeSetWatchedBatchInput(input)
	if err != nil {
		return ProgressBatch{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProgressBatch{}, fmt.Errorf("begin watched state batch update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return ProgressBatch{}, err
	}
	titleIDs := make([]string, len(input))
	completedValues := make([]bool, len(input))
	expectedVersions := make([]int64, len(input))
	for index, item := range input {
		titleIDs[index] = item.TitleID
		completedValues[index] = item.Completed
		expectedVersions[index] = item.ExpectedVersion
	}
	if err := lockUserDataMutations(ctx, tx, profileID, titleIDs); err != nil {
		return ProgressBatch{}, err
	}
	for _, titleID := range titleIDs {
		if _, err := accessibleTitleMediaType(ctx, tx, profileID, titleID); err != nil {
			return ProgressBatch{}, err
		}
	}
	rows, err := tx.Query(ctx, `
		/* watchstate.set_watched_batch */
		WITH accessible_titles AS (`+accessibleTitlesSQL+`),
		input AS (
			SELECT title_id, completed, expected_version, ordinality
			FROM unnest($2::uuid[], $3::boolean[], $4::bigint[]) WITH ORDINALITY
			  AS requested(title_id, completed, expected_version, ordinality)
		), candidates AS (
			SELECT input.*, title.media_type,
			       CASE
			         WHEN title.id IS NULL THEN 'not_found'
			         WHEN title.media_type NOT IN ('movie', 'episode') THEN 'invalid'
			         ELSE ''
			       END AS pre_error
			FROM input
			LEFT JOIN accessible_titles title ON title.id = input.title_id
		), updated AS (
			UPDATE profile_progress progress
			SET position_seconds = CASE WHEN candidate.completed THEN progress.duration_seconds ELSE 0 END,
			    completed = candidate.completed,
			    version = progress.version + 1,
			    last_watched_at = now(),
			    updated_at = now()
			FROM candidates candidate
			WHERE progress.profile_id = $1::uuid
			  AND progress.title_id = candidate.title_id
			  AND candidate.pre_error = ''
			  AND candidate.expected_version > 0
			  AND progress.version = candidate.expected_version
			RETURNING progress.title_id, progress.position_seconds, progress.duration_seconds,
			          progress.completed, progress.version, progress.last_watched_at, progress.updated_at
		), inserted AS (
			INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed)
			SELECT $1::uuid, candidate.title_id, 0, 0, candidate.completed
			FROM candidates candidate
			WHERE candidate.pre_error = '' AND candidate.expected_version = 0
			ON CONFLICT (profile_id, title_id) DO NOTHING
			RETURNING title_id, position_seconds, duration_seconds, completed, version, last_watched_at, updated_at
		), changed AS (
			SELECT * FROM updated
			UNION ALL
			SELECT * FROM inserted
		), cleared AS (
			DELETE FROM profile_continue_dismissals dismissal
			USING changed
			WHERE dismissal.profile_id = $1::uuid AND dismissal.title_id = changed.title_id
			RETURNING dismissal.title_id
		)
		SELECT candidate.ordinality, candidate.title_id::text, candidate.media_type,
		       CASE
		         WHEN candidate.pre_error <> '' THEN candidate.pre_error
		         WHEN changed.title_id IS NULL THEN 'conflict'
		         ELSE ''
		       END,
		       changed.title_id::text, changed.position_seconds, changed.duration_seconds,
		       changed.completed, changed.version, changed.last_watched_at, changed.updated_at,
		       (SELECT count(*) FROM cleared)
		FROM candidates candidate
		LEFT JOIN changed ON changed.title_id = candidate.title_id
		ORDER BY candidate.ordinality
	`, profileID, titleIDs, completedValues, expectedVersions)
	if err != nil {
		return ProgressBatch{}, fmt.Errorf("update watched state batch: %w", err)
	}
	defer rows.Close()
	items := make([]ProgressBatchItem, 0, len(input))
	var batchErr error
	for rows.Next() {
		var (
			ordinal         int
			titleID         string
			mediaType       *string
			status          string
			progressTitleID *string
			position        *int
			duration        *int
			completed       *bool
			version         *int64
			lastWatchedAt   *time.Time
			updatedAt       *time.Time
			clearedCount    int
		)
		if err := rows.Scan(
			&ordinal, &titleID, &mediaType, &status, &progressTitleID, &position, &duration,
			&completed, &version, &lastWatchedAt, &updatedAt, &clearedCount,
		); err != nil {
			return ProgressBatch{}, fmt.Errorf("scan watched state batch: %w", err)
		}
		if ordinal != len(items)+1 {
			return ProgressBatch{}, fmt.Errorf("watched state batch order mismatch")
		}
		switch status {
		case "not_found":
			batchErr = ErrNotFound
		case "invalid":
			if batchErr == nil {
				batchErr = fmt.Errorf("%w: watched titles must be movies or episodes", ErrInvalidInput)
			}
		case "conflict":
			if batchErr == nil {
				batchErr = ErrConflict
			}
		case "":
			progress := &Progress{
				TitleID:         *progressTitleID,
				MediaType:       *mediaType,
				PositionSeconds: *position,
				DurationSeconds: *duration,
				Completed:       *completed,
				Version:         *version,
				LastWatchedAt:   *lastWatchedAt,
				UpdatedAt:       *updatedAt,
			}
			items = append(items, ProgressBatchItem{TitleID: titleID, Progress: progress})
			continue
		default:
			return ProgressBatch{}, fmt.Errorf("unknown watched state batch status %q", status)
		}
		items = append(items, ProgressBatchItem{TitleID: titleID})
	}
	if err := rows.Err(); err != nil {
		return ProgressBatch{}, fmt.Errorf("iterate watched state batch: %w", err)
	}
	rows.Close()
	if len(items) != len(input) {
		return ProgressBatch{}, fmt.Errorf("watched state batch result count mismatch")
	}
	if batchErr != nil {
		return ProgressBatch{}, batchErr
	}
	if s.tracking != nil {
		batch := make([]tracking.BatchEvent, len(items))
		for index, item := range items {
			progress := item.Progress
			batch[index] = tracking.BatchEvent{
				TitleID:        progress.TitleID,
				IdempotencyKey: fmt.Sprintf("watched:%s:%d:%t", progress.TitleID, progress.Version, progress.Completed),
				Event: tracking.Event{
					Type: "watched", TitleID: progress.TitleID, Completed: progress.Completed,
					Version: progress.Version, OccurredAt: progress.UpdatedAt,
				},
			}
		}
		if batchSink, ok := s.tracking.(trackingBatchSink); ok {
			if err := batchSink.EnqueueBatchTx(ctx, tx, profileID, batch); err != nil {
				return ProgressBatch{}, watchstateTrackingError(err)
			}
		} else {
			for _, item := range batch {
				if err := s.enqueueTrackingTx(ctx, tx, profileID, item.TitleID, item.IdempotencyKey, item.Event); err != nil {
					return ProgressBatch{}, err
				}
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ProgressBatch{}, fmt.Errorf("commit watched state batch update: %w", err)
	}
	return ProgressBatch{Items: items}, nil
}

func validateUserDataInput(input UpdateUserDataInput) error {
	if input.Rating.Set && input.Rating.Value != nil {
		value := *input.Rating.Value
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 10 {
			return fmt.Errorf("%w: rating must be between 0 and 10", ErrInvalidInput)
		}
	}
	if input.PlayedPercentage.Set && input.PlayedPercentage.Value != nil {
		value := *input.PlayedPercentage.Value
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return fmt.Errorf("%w: played percentage must be between 0 and 100", ErrInvalidInput)
		}
	}
	if input.UnplayedItemCount.Set && input.UnplayedItemCount.Value != nil &&
		(*input.UnplayedItemCount.Value < 0 || int64(*input.UnplayedItemCount.Value) > maximumSupplementalUserDataCount) {
		return fmt.Errorf("%w: unplayed item count must be between 0 and %d", ErrInvalidInput, maximumSupplementalUserDataCount)
	}
	if input.PlayCount.Set && (input.PlayCount.Value == nil || *input.PlayCount.Value < 0 ||
		int64(*input.PlayCount.Value) > maximumSupplementalUserDataCount) {
		return fmt.Errorf("%w: play count must be between 0 and %d", ErrInvalidInput, maximumSupplementalUserDataCount)
	}
	if input.LastPlayedDate.Set && input.LastPlayedDate.Value != nil {
		year := input.LastPlayedDate.Value.Year()
		if year < 1 || year > 9999 {
			return fmt.Errorf("%w: last played date is outside the supported timestamp range", ErrInvalidInput)
		}
	}
	return nil
}

func hasSupplementalUserDataUpdate(input UpdateUserDataInput) bool {
	return input.Rating.Set || input.PlayedPercentage.Set || input.UnplayedItemCount.Set ||
		input.PlayCount.Set || input.Likes.Set || input.LastPlayedDate.Set
}

func mergeSupplementalUserData(current UserDataValues, input UpdateUserDataInput) UserDataValues {
	if input.Rating.Set {
		current.Rating, current.RatingSet = input.Rating.Value, true
	}
	if input.PlayedPercentage.Set {
		current.PlayedPercentage, current.PlayedPercentageSet = input.PlayedPercentage.Value, true
	}
	if input.UnplayedItemCount.Set {
		current.UnplayedItemCount, current.UnplayedItemCountSet = input.UnplayedItemCount.Value, true
	}
	if input.PlayCount.Set {
		current.PlayCount, current.PlayCountSet = input.PlayCount.Value, true
	}
	if input.Likes.Set {
		current.Likes, current.LikesSet = input.Likes.Value, true
	}
	if input.LastPlayedDate.Set {
		current.LastPlayedDate, current.LastPlayedDateSet = input.LastPlayedDate.Value, true
	}
	return current
}

func splitLastPlayedDate(value *time.Time) (*time.Time, *int) {
	if value == nil {
		return nil, nil
	}
	remainderValue := value.Nanosecond() % int(time.Microsecond)
	baseValue := time.Unix(value.Unix(), int64(value.Nanosecond()-remainderValue)).UTC()
	return &baseValue, &remainderValue
}

func joinLastPlayedDate(value *time.Time, submicrosecond *int) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	if submicrosecond != nil {
		result = result.Add(time.Duration(*submicrosecond))
	}
	return &result
}

// UpdateUserDataForLinkedSession merges compatibility progress, favorite, and
// normalized supplemental fields in one transaction without changing library
// membership.
func (s *Service) UpdateUserDataForLinkedSession(ctx context.Context, principal auth.Principal, titleID string, input UpdateUserDataInput) (UserDataState, error) {
	titleID, err := normalizeTitleID(titleID)
	if err != nil {
		return UserDataState{}, err
	}
	if input.DurationSeconds < 0 || input.PositionSeconds != nil && *input.PositionSeconds < 0 {
		return UserDataState{}, fmt.Errorf("%w: playback times must be zero or greater", ErrInvalidInput)
	}
	if err := validateUserDataInput(input); err != nil {
		return UserDataState{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UserDataState{}, fmt.Errorf("begin user data update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := s.mutationProfileID(ctx, tx, principal, true)
	if err != nil {
		return UserDataState{}, err
	}
	if err := lockUserDataMutation(ctx, tx, profileID, titleID); err != nil {
		return UserDataState{}, err
	}
	mediaType, err := accessibleTitleMediaType(ctx, tx, profileID, titleID)
	if err != nil {
		return UserDataState{}, err
	}

	var current Progress
	progressExists := true
	err = scanProgress(tx.QueryRow(ctx, `
		SELECT progress.title_id::text, $3::text, progress.position_seconds,
		       progress.duration_seconds, progress.completed, progress.version,
		       progress.last_watched_at, progress.updated_at
		FROM profile_progress progress
		WHERE progress.profile_id = $1::uuid AND progress.title_id = $2::uuid
		FOR UPDATE
	`, profileID, titleID, mediaType), &current)
	if errors.Is(err, pgx.ErrNoRows) {
		progressExists = false
		current = Progress{TitleID: titleID, MediaType: mediaType}
	} else if err != nil {
		return UserDataState{}, fmt.Errorf("read user data progress: %w", err)
	}
	var inLibrary, favorite bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM profile_library
			WHERE profile_id = $1::uuid AND title_id = $2::uuid
		), EXISTS (
			SELECT 1 FROM profile_favorites
			WHERE profile_id = $1::uuid AND title_id = $2::uuid
		)
	`, profileID, titleID).Scan(&inLibrary, &favorite); err != nil {
		return UserDataState{}, fmt.Errorf("read user data state: %w", err)
	}

	var userData UserDataValues
	userDataExists := true
	var storedLastPlayedDate *time.Time
	var lastPlayedDateSubmicrosecond *int
	err = tx.QueryRow(ctx, `
		SELECT rating, rating_set, played_percentage, played_percentage_set,
		       unplayed_item_count, unplayed_item_count_set, play_count, play_count_set,
		       likes, likes_set, last_played_date, last_played_date_submicrosecond, last_played_date_set
		FROM profile_user_data
		WHERE profile_id = $1::uuid AND title_id = $2::uuid
	`, profileID, titleID).Scan(
		&userData.Rating, &userData.RatingSet, &userData.PlayedPercentage, &userData.PlayedPercentageSet,
		&userData.UnplayedItemCount, &userData.UnplayedItemCountSet, &userData.PlayCount, &userData.PlayCountSet,
		&userData.Likes, &userData.LikesSet, &storedLastPlayedDate, &lastPlayedDateSubmicrosecond, &userData.LastPlayedDateSet,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		userDataExists = false
		userData = UserDataValues{}
	} else if err != nil {
		return UserDataState{}, fmt.Errorf("read supplemental user data: %w", err)
	}
	if userDataExists {
		userData.LastPlayedDate = joinLastPlayedDate(storedLastPlayedDate, lastPlayedDateSubmicrosecond)
	}

	position, played := current.PositionSeconds, current.Completed
	if input.PositionSeconds != nil {
		position = *input.PositionSeconds
	}
	if input.Played != nil {
		played = *input.Played
	}
	progressChanged := input.PositionSeconds != nil && position != current.PositionSeconds || input.Played != nil && played != current.Completed
	if !progressExists {
		progressChanged = input.PositionSeconds != nil && position != 0 || input.Played != nil && played
	}
	if progressChanged {
		if mediaType != "movie" && mediaType != "episode" {
			return UserDataState{}, fmt.Errorf("%w: progress titles must be movies or episodes", ErrInvalidInput)
		}
		duration := current.DurationSeconds
		if duration == 0 {
			duration = input.DurationSeconds
		}
		if err := validateProgressInput(UpdateProgressInput{PositionSeconds: position, DurationSeconds: duration, Completed: played}); err != nil {
			return UserDataState{}, err
		}
		if progressExists {
			err = scanProgress(tx.QueryRow(ctx, `
				UPDATE profile_progress
				SET position_seconds = $3, duration_seconds = $4, completed = $5,
				    version = version + 1, last_watched_at = now(), updated_at = now()
				WHERE profile_id = $1::uuid AND title_id = $2::uuid
				RETURNING title_id::text, $6::text, position_seconds, duration_seconds,
				          completed, version, last_watched_at, updated_at
			`, profileID, titleID, position, duration, played, mediaType), &current)
		} else {
			err = scanProgress(tx.QueryRow(ctx, `
				INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed)
				VALUES ($1::uuid, $2::uuid, $3, $4, $5)
				RETURNING title_id::text, $6::text, position_seconds, duration_seconds,
				          completed, version, last_watched_at, updated_at
			`, profileID, titleID, position, duration, played, mediaType), &current)
			progressExists = true
		}
		if err != nil {
			return UserDataState{}, fmt.Errorf("update user data progress: %w", err)
		}
		if err := clearContinueDismissalTx(ctx, tx, profileID, titleID); err != nil {
			return UserDataState{}, err
		}
		if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("progress:%s:%d", titleID, current.Version), tracking.Event{
			Type: "progress", TitleID: titleID, Completed: current.Completed,
			PositionSeconds: current.PositionSeconds, DurationSeconds: current.DurationSeconds,
			Version: current.Version, OccurredAt: current.UpdatedAt,
		}); err != nil {
			return UserDataState{}, err
		}
	}

	desiredFavorite := favorite
	if input.Favorite != nil {
		desiredFavorite = *input.Favorite
	}
	if desiredFavorite != favorite {
		if desiredFavorite {
			if _, err := tx.Exec(ctx, `
				INSERT INTO profile_favorites (profile_id, title_id)
				VALUES ($1::uuid, $2::uuid)
			`, profileID, titleID); err != nil {
				return UserDataState{}, fmt.Errorf("add user data favorite: %w", err)
			}
		} else if _, err := tx.Exec(ctx, `
			DELETE FROM profile_favorites
			WHERE profile_id = $1::uuid AND title_id = $2::uuid
		`, profileID, titleID); err != nil {
			return UserDataState{}, fmt.Errorf("remove user data favorite: %w", err)
		}
		favorite = desiredFavorite
	}
	if hasSupplementalUserDataUpdate(input) {
		userData = mergeSupplementalUserData(userData, input)
		lastPlayedDate, lastPlayedDateSubmicrosecond := splitLastPlayedDate(userData.LastPlayedDate)
		if _, err := tx.Exec(ctx, `
			INSERT INTO profile_user_data (
				profile_id, title_id, rating, rating_set, played_percentage, played_percentage_set,
				unplayed_item_count, unplayed_item_count_set, play_count, play_count_set,
				likes, likes_set, last_played_date, last_played_date_submicrosecond, last_played_date_set
			) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (profile_id, title_id) DO UPDATE SET
				rating = EXCLUDED.rating, rating_set = EXCLUDED.rating_set,
				played_percentage = EXCLUDED.played_percentage, played_percentage_set = EXCLUDED.played_percentage_set,
				unplayed_item_count = EXCLUDED.unplayed_item_count, unplayed_item_count_set = EXCLUDED.unplayed_item_count_set,
				play_count = EXCLUDED.play_count, play_count_set = EXCLUDED.play_count_set,
				likes = EXCLUDED.likes, likes_set = EXCLUDED.likes_set,
				last_played_date = EXCLUDED.last_played_date,
				last_played_date_submicrosecond = EXCLUDED.last_played_date_submicrosecond,
				last_played_date_set = EXCLUDED.last_played_date_set,
				updated_at = now()
		`, profileID, titleID, userData.Rating, userData.RatingSet, userData.PlayedPercentage, userData.PlayedPercentageSet,
			userData.UnplayedItemCount, userData.UnplayedItemCountSet, userData.PlayCount, userData.PlayCountSet,
			userData.Likes, userData.LikesSet, lastPlayedDate, lastPlayedDateSubmicrosecond, userData.LastPlayedDateSet); err != nil {
			return UserDataState{}, fmt.Errorf("update supplemental user data: %w", err)
		}
		userDataExists = true
	}
	if err := tx.Commit(ctx); err != nil {
		return UserDataState{}, fmt.Errorf("commit user data update: %w", err)
	}
	result := UserDataState{InLibrary: inLibrary, Favorite: favorite}
	if userDataExists {
		result.UserData = &userData
	}
	if progressExists {
		result.Progress = &current
	}
	return result, nil
}

func (s *Service) UpdateProgress(ctx context.Context, principal auth.Principal, titleID string, input UpdateProgressInput) (Progress, error) {
	return s.updateProgress(ctx, principal, titleID, input, false)
}

// ApplyPlaybackEventForLinkedSession atomically revalidates protocol-linked
// authority, checks the caller's version, writes the complete playback state,
// and enqueues tracking before the transaction commits.
func (s *Service) ApplyPlaybackEventForLinkedSession(ctx context.Context, principal auth.Principal, titleID string, input UpdateProgressInput) (Progress, error) {
	return s.updateProgress(ctx, principal, titleID, input, true)
}

func (s *Service) updateProgress(ctx context.Context, principal auth.Principal, titleID string, input UpdateProgressInput, linked bool) (Progress, error) {
	titleID, err := normalizeTitleID(titleID)
	if err != nil {
		return Progress{}, err
	}
	if err := validateProgressInput(input); err != nil {
		return Progress{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Progress{}, fmt.Errorf("begin playback progress update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := s.mutationProfileID(ctx, tx, principal, linked)
	if err != nil {
		return Progress{}, err
	}
	if err := lockUserDataMutation(ctx, tx, profileID, titleID); err != nil {
		return Progress{}, err
	}
	mediaType, err := accessibleTitleMediaType(ctx, tx, profileID, titleID)
	if err != nil {
		return Progress{}, err
	}
	if mediaType != "movie" && mediaType != "episode" {
		return Progress{}, fmt.Errorf("%w: progress titles must be movies or episodes", ErrInvalidInput)
	}
	var progress Progress
	if input.ExpectedVersion == 0 {
		err = scanProgress(tx.QueryRow(ctx, `
			INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5)
			ON CONFLICT (profile_id, title_id) DO NOTHING
			RETURNING title_id::text, $6::text, position_seconds, duration_seconds,
			          completed, version, last_watched_at, updated_at
		`, profileID, titleID, input.PositionSeconds, input.DurationSeconds, input.Completed, mediaType), &progress)
	} else {
		err = scanProgress(tx.QueryRow(ctx, `
			UPDATE profile_progress
			SET position_seconds = $4, duration_seconds = $5, completed = $6,
			    version = version + 1, last_watched_at = now(), updated_at = now()
			WHERE profile_id = $1::uuid AND title_id = $2::uuid AND version = $3
			RETURNING title_id::text, $7::text, position_seconds, duration_seconds,
			          completed, version, last_watched_at, updated_at
		`, profileID, titleID, input.ExpectedVersion, input.PositionSeconds, input.DurationSeconds, input.Completed, mediaType), &progress)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrConflict
	}
	if err != nil {
		return Progress{}, fmt.Errorf("update playback progress: %w", err)
	}
	if err := clearContinueDismissalTx(ctx, tx, profileID, titleID); err != nil {
		return Progress{}, err
	}
	if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("progress:%s:%d", titleID, progress.Version), tracking.Event{
		Type: "progress", TitleID: titleID, Completed: progress.Completed,
		PositionSeconds: progress.PositionSeconds, DurationSeconds: progress.DurationSeconds,
		Version: progress.Version, OccurredAt: progress.UpdatedAt,
	}); err != nil {
		return Progress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Progress{}, fmt.Errorf("commit playback progress update: %w", err)
	}
	return progress, nil
}

func (s *Service) SetWatched(ctx context.Context, principal auth.Principal, titleID string, completed bool, input CompletionInput) (Progress, error) {
	return s.setWatched(ctx, principal, titleID, completed, input, false)
}

// SetWatchedForLinkedSession revalidates protocol-linked authority while
// holding the same transaction open through the watchstate commit.
func (s *Service) SetWatchedForLinkedSession(ctx context.Context, principal auth.Principal, titleID string, completed bool, input CompletionInput) (Progress, error) {
	return s.setWatched(ctx, principal, titleID, completed, input, true)
}

func (s *Service) setWatched(ctx context.Context, principal auth.Principal, titleID string, completed bool, input CompletionInput, linked bool) (Progress, error) {
	titleID, err := normalizeTitleID(titleID)
	if err != nil {
		return Progress{}, err
	}
	if input.ExpectedVersion < 0 {
		return Progress{}, fmt.Errorf("%w: expectedVersion must be zero or greater", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Progress{}, fmt.Errorf("begin watched state update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := s.mutationProfileID(ctx, tx, principal, linked)
	if err != nil {
		return Progress{}, err
	}
	if err := lockUserDataMutation(ctx, tx, profileID, titleID); err != nil {
		return Progress{}, err
	}
	mediaType, err := accessibleTitleMediaType(ctx, tx, profileID, titleID)
	if err != nil {
		return Progress{}, err
	}
	if mediaType != "movie" && mediaType != "episode" {
		return Progress{}, fmt.Errorf("%w: watched titles must be movies or episodes", ErrInvalidInput)
	}
	var progress Progress
	if input.ExpectedVersion == 0 {
		err = scanProgress(tx.QueryRow(ctx, `
			INSERT INTO profile_progress (profile_id, title_id, position_seconds, duration_seconds, completed)
			VALUES ($1::uuid, $2::uuid, 0, 0, $3)
			ON CONFLICT (profile_id, title_id) DO NOTHING
			RETURNING title_id::text, $4::text, position_seconds, duration_seconds,
			          completed, version, last_watched_at, updated_at
		`, profileID, titleID, completed, mediaType), &progress)
	} else {
		err = scanProgress(tx.QueryRow(ctx, `
			UPDATE profile_progress
			SET position_seconds = CASE WHEN $4 THEN duration_seconds ELSE 0 END,
			    completed = $4, version = version + 1, last_watched_at = now(), updated_at = now()
			WHERE profile_id = $1::uuid AND title_id = $2::uuid AND version = $3
			RETURNING title_id::text, $5::text, position_seconds, duration_seconds,
			          completed, version, last_watched_at, updated_at
		`, profileID, titleID, input.ExpectedVersion, completed, mediaType), &progress)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrConflict
	}
	if err != nil {
		return Progress{}, fmt.Errorf("set watched state: %w", err)
	}
	if err := clearContinueDismissalTx(ctx, tx, profileID, titleID); err != nil {
		return Progress{}, err
	}
	if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("watched:%s:%d:%t", titleID, progress.Version, completed), tracking.Event{
		Type: "watched", TitleID: titleID, Completed: completed,
		Version: progress.Version, OccurredAt: progress.UpdatedAt,
	}); err != nil {
		return Progress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Progress{}, fmt.Errorf("commit watched state update: %w", err)
	}
	return progress, nil
}

func lockUserDataMutation(ctx context.Context, tx pgx.Tx, profileID, titleID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "user-data:"+profileID+":"+titleID); err != nil {
		return fmt.Errorf("lock user data mutation: %w", err)
	}
	return nil
}

func lockUserDataMutations(ctx context.Context, tx pgx.Tx, profileID string, titleIDs []string) error {
	ordered := append([]string(nil), titleIDs...)
	sort.Strings(ordered)
	for _, titleID := range ordered {
		if err := lockUserDataMutation(ctx, tx, profileID, titleID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) mutationProfileID(ctx context.Context, tx pgx.Tx, principal auth.Principal, linked bool) (string, error) {
	if !linked {
		return authorizedActiveProfileID(ctx, tx, principal)
	}
	location := s.runtimeLocation(ctx)
	reloaded, valid, err := auth.ReloadAndLockLinkedPrincipal(ctx, tx, principal, time.Now().UTC(), location)
	if err != nil {
		return "", fmt.Errorf("revalidate linked watchstate authority: %w", err)
	}
	if !valid || reloaded.ActiveProfileID == nil {
		return "", ErrForbidden
	}
	return *reloaded.ActiveProfileID, nil
}

func (s *Service) ClearProgress(ctx context.Context, principal auth.Principal, titleID string, expectedVersion int64) error {
	titleID, err := normalizeTitleID(titleID)
	if err != nil {
		return err
	}
	if expectedVersion < 0 {
		return fmt.Errorf("%w: expectedVersion must be zero or greater", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin playback progress clear: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return err
	}
	if err := lockUserDataMutation(ctx, tx, profileID, titleID); err != nil {
		return err
	}
	if _, err := accessibleTitleMediaType(ctx, tx, profileID, titleID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		DELETE FROM profile_progress
		WHERE profile_id = $1::uuid AND title_id = $2::uuid AND version = $3
	`, profileID, titleID, expectedVersion)
	if err != nil {
		return fmt.Errorf("clear playback progress: %w", err)
	}
	if result.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM profile_progress WHERE profile_id = $1::uuid AND title_id = $2::uuid)`, profileID, titleID).Scan(&exists); err != nil {
			return fmt.Errorf("query playback progress after clear: %w", err)
		}
		if exists || expectedVersion != 0 {
			return ErrConflict
		}
	}
	occurredAt := time.Now().UTC()
	if err := s.enqueueTrackingTx(ctx, tx, profileID, titleID, fmt.Sprintf("progress:clear:%s:%d:%d", titleID, expectedVersion, occurredAt.UnixNano()), tracking.Event{
		Type: "progress", TitleID: titleID, Cleared: true, Version: expectedVersion, OccurredAt: occurredAt,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit playback progress clear: %w", err)
	}
	return nil
}

func (s *Service) DismissContinue(ctx context.Context, principal auth.Principal, titleID string) error {
	titleID, err := normalizeTitleID(titleID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin continue watching dismissal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return err
	}
	mediaType, err := accessibleTitleMediaType(ctx, tx, profileID, titleID)
	if err != nil {
		return err
	}
	var targetID string
	err = tx.QueryRow(ctx, `
		WITH accessible_titles AS (`+accessibleTitlesSQL+`)
		SELECT COALESCE(CASE WHEN title.media_type = 'episode' THEN series.id END, title.id)::text
		FROM titles title
		LEFT JOIN titles season
		  ON title.media_type = 'episode' AND season.id = title.parent_id AND season.media_type = 'season'
		LEFT JOIN titles series
		  ON season.parent_id = series.id AND series.media_type = 'series'
		WHERE title.id = $2::uuid
		  AND (
		      title.media_type <> 'episode'
		      OR EXISTS (SELECT 1 FROM accessible_titles accessible WHERE accessible.id = series.id)
		  )
	`, profileID, titleID).Scan(&targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("resolve continue watching dismissal target: %w", err)
	}
	if mediaType != "movie" && mediaType != "episode" {
		return fmt.Errorf("%w: continue watching titles must be movies or episodes", ErrInvalidInput)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile_continue_dismissals (profile_id, title_id, dismissed_at)
		VALUES ($1::uuid, $2::uuid, now())
		ON CONFLICT (profile_id, title_id) DO UPDATE SET dismissed_at = EXCLUDED.dismissed_at
	`, profileID, targetID); err != nil {
		return fmt.Errorf("dismiss continue watching title: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit continue watching dismissal: %w", err)
	}
	return nil
}

func clearContinueDismissalTx(ctx context.Context, tx pgx.Tx, profileID, titleID string) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM profile_continue_dismissals dismissal
		WHERE dismissal.profile_id = $1::uuid
		  AND dismissal.title_id = (
			  SELECT COALESCE(CASE WHEN title.media_type = 'episode' THEN series.id END, title.id)
			  FROM titles title
			  LEFT JOIN titles season
			    ON title.media_type = 'episode' AND season.id = title.parent_id AND season.media_type = 'season'
			  LEFT JOIN titles series
			    ON season.parent_id = series.id AND series.media_type = 'series'
			  WHERE title.id = $2::uuid
		  )
	`, profileID, titleID); err != nil {
		return fmt.Errorf("restore continue watching title: %w", err)
	}
	return nil
}

func (s *Service) ContinueWatching(ctx context.Context, principal auth.Principal, metadataLanguage string, limit int) (ContinuePage, error) {
	metadataLanguage, err := normalizeContinueMetadataLanguage(metadataLanguage)
	if err != nil {
		return ContinuePage{}, err
	}
	if limit == 0 {
		limit = defaultPageSize
	}
	if limit < 1 || limit > maximumPageSize {
		return ContinuePage{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maximumPageSize)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ContinuePage{}, fmt.Errorf("begin continue watching query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := disableTransactionJIT(ctx, tx); err != nil {
		return ContinuePage{}, err
	}
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return ContinuePage{}, err
	}

	items, activeSeries, err := resumeItems(ctx, tx, profileID, limit)
	if err != nil {
		return ContinuePage{}, err
	}
	if len(items) < limit {
		next, err := nextEpisodeItems(ctx, tx, profileID, activeSeries, limit-len(items))
		if err != nil {
			return ContinuePage{}, err
		}
		items = append(items, next...)
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].LastWatchedAt.After(items[right].LastWatchedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	if err := overlayLocalizedContinueItems(ctx, tx, metadataLanguage, items); err != nil {
		return ContinuePage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContinuePage{}, fmt.Errorf("commit continue watching query: %w", err)
	}
	return ContinuePage{Items: items}, nil
}

type continueMetadataSnapshot struct {
	languageRank int
	provider     string
	payload      json.RawMessage
}

func normalizeContinueMetadataLanguage(language string) (string, error) {
	language = strings.TrimSpace(language)
	if language == "" || strings.EqualFold(language, "auto") {
		return "", nil
	}
	if !continueMetadataLanguagePattern.MatchString(language) {
		return "", fmt.Errorf("%w: metadata language must be a BCP 47 language tag", ErrInvalidInput)
	}
	parts := strings.Split(language, "-")
	for index := range parts {
		parts[index] = strings.ToLower(parts[index])
		if index == 0 {
			continue
		}
		if len(parts[index]) == 4 {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		} else if len(parts[index]) == 2 {
			parts[index] = strings.ToUpper(parts[index])
		}
	}
	return strings.Join(parts, "-"), nil
}

func overlayLocalizedContinueItems(ctx context.Context, tx pgx.Tx, language string, items []ContinueItem) error {
	if language == "" || len(items) == 0 {
		return nil
	}
	titleIDs := make([]string, 0, len(items)*2)
	seen := make(map[string]struct{}, len(items)*2)
	for _, item := range items {
		var itemTitleIDs [2]string
		count := 1
		itemTitleIDs[0] = item.TitleID
		if item.MediaType == "episode" {
			itemTitleIDs[0], itemTitleIDs[1], count = item.SeriesID, item.SeasonID, 2
		}
		for _, titleID := range itemTitleIDs[:count] {
			if titleID == "" {
				continue
			}
			if _, exists := seen[titleID]; exists {
				continue
			}
			seen[titleID] = struct{}{}
			titleIDs = append(titleIDs, titleID)
		}
	}
	sort.Strings(titleIDs)
	rows, err := tx.Query(ctx, `
		/* watchstate.continue_localization */
		SELECT title_id::text,
		       CASE WHEN lower(language) = lower($1) THEN 0
		            WHEN lower(language) = split_part(lower($1), '-', 1) THEN 1
		            ELSE 2 END AS language_rank,
		       provider, payload
		FROM title_metadata
		WHERE split_part(lower(language), '-', 1) = split_part(lower($1), '-', 1)
		  AND title_id = ANY($2::uuid[])
		ORDER BY title_id, language_rank, language, updated_at DESC, provider
	`, language, titleIDs)
	if err != nil {
		return fmt.Errorf("query localized continue metadata: %w", err)
	}
	defer rows.Close()
	snapshots := make(map[string][]continueMetadataSnapshot, len(titleIDs))
	for rows.Next() {
		var titleID string
		var snapshot continueMetadataSnapshot
		if err := rows.Scan(&titleID, &snapshot.languageRank, &snapshot.provider, &snapshot.payload); err != nil {
			return fmt.Errorf("scan localized continue metadata: %w", err)
		}
		snapshots[titleID] = append(snapshots[titleID], snapshot)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate localized continue metadata: %w", err)
	}
	for index := range items {
		overlayLocalizedContinueItem(&items[index], snapshots)
	}
	return nil
}

func overlayLocalizedContinueItem(item *ContinueItem, snapshots map[string][]continueMetadataSnapshot) {
	if item.MediaType == "movie" {
		if movie, ok := bestLocalizedMovie(snapshots[item.TitleID], item.ResourceProvider); ok {
			replaceNonEmpty(&item.Title, movie.Title)
			replaceNonEmpty(&item.PosterURL, movie.PosterURL)
			replaceNonEmpty(&item.BackgroundURL, movie.BackdropURL)
			replaceNonEmpty(&item.ReleaseInfo, releaseInfoFromMetadataDate(movie.ReleaseDate))
		}
		return
	}
	if item.MediaType != "episode" {
		return
	}
	if series, ok := bestLocalizedSeries(snapshots[item.SeriesID], item.ResourceProvider); ok {
		replaceNonEmpty(&item.Title, series.Name)
		replaceNonEmpty(&item.PosterURL, series.PosterURL)
		replaceNonEmpty(&item.BackgroundURL, series.BackdropURL)
		replaceNonEmpty(&item.ReleaseInfo, releaseInfoFromMetadataDate(series.FirstAirDate))
	}
	if episode, ok := bestLocalizedEpisode(snapshots[item.SeasonID], item.ResourceProvider, *item); ok {
		replaceNonEmpty(&item.EpisodeTitle, episode.Name)
		replaceNonEmpty(&item.EpisodeStillURL, episode.StillURL)
		replaceNonEmpty(&item.BackgroundURL, episode.BackdropURL)
		replaceNonEmpty(&item.EpisodeAirDate, episode.AirDate)
	}
}

func bestLocalizedMovie(snapshots []continueMetadataSnapshot, preferredProvider string) (metadata.Movie, bool) {
	for start := 0; start < len(snapshots); {
		end := continueMetadataLanguageGroupEnd(snapshots, start)
		for _, preferred := range []bool{true, false} {
			for _, snapshot := range snapshots[start:end] {
				if (snapshot.provider == preferredProvider) != preferred {
					continue
				}
				var movie metadata.Movie
				if json.Unmarshal(snapshot.payload, &movie) == nil && nonEmptyCount(movie.Title, movie.PosterURL, movie.BackdropURL, movie.ReleaseDate) > 0 {
					return movie, true
				}
			}
		}
		start = end
	}
	return metadata.Movie{}, false
}

func bestLocalizedSeries(snapshots []continueMetadataSnapshot, preferredProvider string) (metadata.Series, bool) {
	for start := 0; start < len(snapshots); {
		end := continueMetadataLanguageGroupEnd(snapshots, start)
		for _, preferred := range []bool{true, false} {
			for _, snapshot := range snapshots[start:end] {
				if (snapshot.provider == preferredProvider) != preferred {
					continue
				}
				var series metadata.Series
				if json.Unmarshal(snapshot.payload, &series) == nil && nonEmptyCount(series.Name, series.PosterURL, series.BackdropURL, series.FirstAirDate) > 0 {
					return series, true
				}
			}
		}
		start = end
	}
	return metadata.Series{}, false
}

func bestLocalizedEpisode(snapshots []continueMetadataSnapshot, preferredProvider string, item ContinueItem) (metadata.Episode, bool) {
	for start := 0; start < len(snapshots); {
		end := continueMetadataLanguageGroupEnd(snapshots, start)
		for _, preferred := range []bool{true, false} {
			for _, snapshot := range snapshots[start:end] {
				if (snapshot.provider == preferredProvider) != preferred {
					continue
				}
				var season metadata.Season
				if json.Unmarshal(snapshot.payload, &season) != nil {
					continue
				}
				for _, episode := range season.Episodes {
					if episode.ID != item.TitleID && !matchesContinueEpisodeOrdinal(episode, item) {
						continue
					}
					if nonEmptyCount(episode.Name, episode.StillURL, episode.BackdropURL, episode.AirDate) > 0 {
						return episode, true
					}
				}
			}
		}
		start = end
	}
	return metadata.Episode{}, false
}

func matchesContinueEpisodeOrdinal(episode metadata.Episode, item ContinueItem) bool {
	return item.SeasonNumber != nil && item.EpisodeNumber != nil &&
		episode.SeasonNumber == *item.SeasonNumber && episode.EpisodeNumber == *item.EpisodeNumber
}

func continueMetadataLanguageGroupEnd(snapshots []continueMetadataSnapshot, start int) int {
	end := start + 1
	for end < len(snapshots) && snapshots[end].languageRank == snapshots[start].languageRank {
		end++
	}
	return end
}

func nonEmptyCount(values ...string) int {
	count := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			count++
		}
	}
	return count
}

func replaceNonEmpty(destination *string, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*destination = value
	}
}

func releaseInfoFromMetadataDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 4 {
		if _, err := strconv.Atoi(value[:4]); err == nil {
			return value[:4]
		}
	}
	return value
}

func disableTransactionJIT(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SET LOCAL jit = off`); err != nil {
		return fmt.Errorf("disable PostgreSQL JIT for continue transaction: %w", err)
	}
	return nil
}

func resumeItems(ctx context.Context, tx pgx.Tx, profileID string, limit int) ([]ContinueItem, map[string]struct{}, error) {
	rows, err := tx.Query(ctx, `
		WITH accessible_titles AS (`+accessibleTitlesSQL+`),
		active_hierarchy AS (
			SELECT DISTINCT ON (series.id)
			       series.id AS series_id,
			       season.hierarchy_variant
			FROM profile_progress hierarchy_progress
			JOIN titles episode
			  ON episode.id = hierarchy_progress.title_id
			 AND episode.media_type = 'episode'
			JOIN accessible_titles accessible_episode ON accessible_episode.id = episode.id
			JOIN titles season ON season.id = episode.parent_id AND season.media_type = 'season'
			JOIN accessible_titles accessible_season ON accessible_season.id = season.id
			JOIN titles series ON series.id = season.parent_id AND series.media_type = 'series'
			JOIN accessible_titles accessible_series ON accessible_series.id = series.id
			WHERE hierarchy_progress.profile_id = $1::uuid
			  AND series.source_addon_id IS NULL
			ORDER BY series.id, hierarchy_progress.last_watched_at DESC, episode.id
		),
		resumable AS (
			SELECT title.id,
			       title.media_type,
			       series.id AS series_id,
			       season.id AS season_id,
			       season.ordinal AS season_number,
			       title.ordinal AS episode_number,
			       CASE WHEN title.media_type = 'episode' AND season.hierarchy_variant <> ''
			            THEN COALESCE(episode_order.provider, '') ELSE '' END AS mapping_provider,
			       CASE WHEN title.media_type = 'episode' AND season.hierarchy_variant <> ''
			            THEN COALESCE(episode_order.order_id, '') ELSE '' END AS episode_order_id,
			       CASE WHEN title.media_type = 'episode' AND season.hierarchy_variant <> ''
			            THEN COALESCE(season_order.provider, '') || ':' || series.id::text || ':' ||
			                 COALESCE(season_order.external_id, '')
			            ELSE '' END AS metadata_season_id,
			       progress.position_seconds,
			       progress.duration_seconds,
			       progress.version,
			       progress.last_watched_at,
			       COALESCE(CASE WHEN title.media_type = 'episode' THEN series.display_title ELSE title.display_title END, '') AS display_title,
			       COALESCE(CASE WHEN title.media_type = 'episode' THEN series.poster_url ELSE title.poster_url END, '') AS poster_url,
			       COALESCE(CASE WHEN title.media_type = 'episode' THEN series.background_url ELSE title.background_url END, '') AS background_url,
			       COALESCE(CASE WHEN title.media_type = 'episode' THEN series.release_info ELSE title.release_info END, '') AS release_info,
			       CASE
			           WHEN title.media_type = 'episode' AND season.hierarchy_variant <> ''
			             THEN COALESCE(episode_order.provider, '') || ':' || COALESCE(episode_order.external_id, '')
			           WHEN title.media_type = 'episode'
			             THEN COALESCE(series.resource_id, '') || ':' || season.ordinal::text || ':' || title.ordinal::text
			           ELSE COALESCE(title.resource_id, '')
			       END AS resource_id,
			       CASE
			           WHEN title.media_type = 'episode' AND season.hierarchy_variant <> ''
			             THEN COALESCE(episode_order.provider, '')
			           WHEN title.media_type = 'episode' THEN COALESCE(series.resource_provider, '')
			           ELSE COALESCE(title.resource_provider, '')
			       END AS resource_provider,
			       COALESCE(CASE WHEN title.media_type = 'episode' THEN title.display_title END, '') AS episode_title,
			       COALESCE(CASE WHEN title.media_type = 'episode' THEN NULLIF(title.poster_url, '') END, CASE WHEN title.media_type = 'episode' THEN NULLIF(series.background_url, '') END, '') AS episode_still_url,
			       COALESCE(CASE WHEN title.media_type = 'episode' THEN title.release_date::text END, '') AS episode_air_date,
			       row_number() OVER (
			           PARTITION BY CASE
			               WHEN title.media_type = 'episode' THEN series.id
			               ELSE title.id
			           END
			           ORDER BY progress.last_watched_at DESC, title.id
			       ) AS series_rank
			FROM profile_progress progress
			JOIN titles title ON title.id = progress.title_id
			JOIN accessible_titles accessible_title ON accessible_title.id = title.id
			LEFT JOIN titles season
			  ON title.media_type = 'episode' AND season.id = title.parent_id
			LEFT JOIN accessible_titles accessible_season ON accessible_season.id = season.id
			LEFT JOIN titles series
			  ON season.media_type = 'season' AND series.id = season.parent_id
			LEFT JOIN accessible_titles accessible_series ON accessible_series.id = series.id
			LEFT JOIN active_hierarchy active
			  ON title.media_type = 'episode' AND active.series_id = series.id
			LEFT JOIN title_episode_order_identities season_order
			  ON season_order.title_id = season.id AND season_order.namespace = 'season'
			LEFT JOIN title_episode_order_identities episode_order
			  ON episode_order.title_id = title.id AND episode_order.namespace = 'episode'
			WHERE progress.profile_id = $1::uuid
			  AND NOT progress.completed
			  AND progress.position_seconds > 0
			  AND (
			      title.media_type <> 'episode'
			      OR (
			          accessible_season.id IS NOT NULL
			          AND accessible_series.id IS NOT NULL
			          AND series.source_addon_id IS NULL
			          AND active.series_id IS NOT NULL
			          AND season.hierarchy_variant = active.hierarchy_variant
			      )
			  )
			  AND NOT EXISTS (
				  SELECT 1
				  FROM profile_continue_dismissals dismissal
				  WHERE dismissal.profile_id = progress.profile_id
				    AND dismissal.title_id = COALESCE(series.id, title.id)
			  )
		)
		SELECT id::text, media_type, series_id::text, season_id::text,
		       season_number, episode_number, mapping_provider, episode_order_id,
		       metadata_season_id, position_seconds, duration_seconds,
		       version, display_title, poster_url, background_url, release_info,
		       resource_id, resource_provider, episode_title, episode_still_url,
		       episode_air_date, last_watched_at
		FROM resumable
		WHERE series_rank = 1
		ORDER BY last_watched_at DESC, id
		LIMIT $2
	`, profileID, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("query resumable titles: %w", err)
	}
	defer rows.Close()
	items := make([]ContinueItem, 0)
	activeSeries := make(map[string]struct{})
	for rows.Next() {
		var item ContinueItem
		var seriesID, seasonID *string
		if err := rows.Scan(
			&item.TitleID, &item.MediaType, &seriesID, &seasonID,
			&item.SeasonNumber, &item.EpisodeNumber, &item.MappingProvider,
			&item.EpisodeOrderID, &item.MetadataSeasonID, &item.PositionSeconds,
			&item.DurationSeconds, &item.Version, &item.Title, &item.PosterURL,
			&item.BackgroundURL, &item.ReleaseInfo, &item.ResourceID,
			&item.ResourceProvider, &item.EpisodeTitle, &item.EpisodeStillURL,
			&item.EpisodeAirDate, &item.LastWatchedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("scan resumable title: %w", err)
		}
		if seriesID != nil {
			item.SeriesID = *seriesID
			activeSeries[*seriesID] = struct{}{}
		}
		if seasonID != nil {
			item.SeasonID = *seasonID
		}
		item.Reason = "resume"
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate resumable titles: %w", err)
	}
	return items, activeSeries, nil
}

const nextEpisodeQuery = `
		WITH accessible_titles AS (` + accessibleTitlesSQL + `),
		active_hierarchy AS (
			SELECT DISTINCT ON (series.id)
			       series.id AS series_id,
			       season.hierarchy_variant
			FROM profile_progress hierarchy_progress
			JOIN titles episode
			  ON episode.id = hierarchy_progress.title_id
			 AND episode.media_type = 'episode'
			JOIN accessible_titles accessible_episode ON accessible_episode.id = episode.id
			JOIN titles season ON season.id = episode.parent_id AND season.media_type = 'season'
			JOIN accessible_titles accessible_season ON accessible_season.id = season.id
			JOIN titles series ON series.id = season.parent_id AND series.media_type = 'series'
			JOIN accessible_titles accessible_series ON accessible_series.id = series.id
			WHERE hierarchy_progress.profile_id = $1::uuid
			  AND series.source_addon_id IS NULL
			ORDER BY series.id, hierarchy_progress.last_watched_at DESC, episode.id
		),
		latest_completed AS (
			SELECT DISTINCT ON (series.id)
			       series.id AS series_id,
			       season.ordinal AS season_number,
			       episode.ordinal AS episode_number,
			       progress.last_watched_at,
			       active.hierarchy_variant
			FROM profile_progress progress
			JOIN titles episode ON episode.id = progress.title_id AND episode.media_type = 'episode'
			JOIN accessible_titles accessible_episode ON accessible_episode.id = episode.id
			JOIN titles season ON season.id = episode.parent_id AND season.media_type = 'season'
			JOIN accessible_titles accessible_season ON accessible_season.id = season.id
			JOIN titles series ON series.id = season.parent_id AND series.media_type = 'series'
			JOIN accessible_titles accessible_series ON accessible_series.id = series.id
			JOIN active_hierarchy active
			  ON active.series_id = series.id
			 AND active.hierarchy_variant = season.hierarchy_variant
			WHERE progress.profile_id = $1::uuid
			  AND progress.completed
			  AND season.ordinal > 0
			  AND series.source_addon_id IS NULL
			  AND NOT EXISTS (
				  SELECT 1
				  FROM profile_continue_dismissals dismissal
				  WHERE dismissal.profile_id = progress.profile_id
				    AND dismissal.title_id = series.id
			  )
			ORDER BY series.id, progress.last_watched_at DESC, episode.id
		)
		SELECT next_episode.id::text,
		       latest.series_id::text,
		       next_season.id::text,
		       next_season.ordinal,
		       next_episode.ordinal,
		       CASE WHEN next_season.hierarchy_variant <> ''
		            THEN COALESCE(episode_order.provider, '') ELSE '' END,
		       CASE WHEN next_season.hierarchy_variant <> ''
		            THEN COALESCE(episode_order.order_id, '') ELSE '' END,
		       CASE WHEN next_season.hierarchy_variant <> ''
		            THEN COALESCE(season_order.provider, '') || ':' || latest.series_id::text || ':' ||
		                 COALESCE(season_order.external_id, '')
		            ELSE '' END,
		       COALESCE(series_title.display_title, ''),
		       COALESCE(series_title.poster_url, ''),
		       COALESCE(series_title.background_url, ''),
		       COALESCE(series_title.release_info, ''),
		       CASE WHEN next_season.hierarchy_variant <> ''
		            THEN COALESCE(episode_order.provider, '') || ':' || COALESCE(episode_order.external_id, '')
		            ELSE COALESCE(series_title.resource_id, '') || ':' || next_season.ordinal::text || ':' || next_episode.ordinal::text
		       END,
		       CASE WHEN next_season.hierarchy_variant <> ''
		            THEN COALESCE(episode_order.provider, '')
		            ELSE COALESCE(series_title.resource_provider, '')
		       END,
		       COALESCE(next_episode.display_title, ''),
		       COALESCE(NULLIF(next_episode.poster_url, ''), NULLIF(series_title.background_url, ''), ''),
		       COALESCE(next_episode.release_date::text, ''),
		       latest.last_watched_at
		FROM latest_completed latest
		JOIN LATERAL (
			SELECT candidate_episode.id, candidate_episode.parent_id, candidate_episode.ordinal,
			       candidate_episode.display_title, candidate_episode.poster_url,
			       candidate_episode.release_date
			FROM titles candidate_episode
			JOIN accessible_titles accessible_candidate_episode ON accessible_candidate_episode.id = candidate_episode.id
			JOIN titles candidate_season
			  ON candidate_season.id = candidate_episode.parent_id
			 AND candidate_season.media_type = 'season'
			JOIN accessible_titles accessible_candidate_season ON accessible_candidate_season.id = candidate_season.id
			WHERE candidate_episode.media_type = 'episode'
			  AND candidate_season.parent_id = latest.series_id
			  AND candidate_season.hierarchy_variant = latest.hierarchy_variant
			  AND candidate_season.ordinal > 0
			  AND (candidate_season.ordinal, candidate_episode.ordinal) >
			      (latest.season_number, latest.episode_number)
			  AND (candidate_season.release_date IS NULL OR candidate_season.release_date <= CURRENT_DATE)
			  AND (candidate_episode.release_date IS NULL OR candidate_episode.release_date <= CURRENT_DATE)
			  AND NOT EXISTS (
				SELECT 1
				FROM profile_progress existing
				WHERE existing.profile_id = $1::uuid
				  AND existing.title_id = candidate_episode.id
				  AND existing.completed
			  )
			ORDER BY candidate_season.ordinal, candidate_episode.ordinal, candidate_episode.id
			LIMIT 1
		) next_episode ON true
		JOIN titles next_season ON next_season.id = next_episode.parent_id
		JOIN titles series_title ON series_title.id = latest.series_id
		LEFT JOIN title_episode_order_identities season_order
		  ON season_order.title_id = next_season.id AND season_order.namespace = 'season'
		LEFT JOIN title_episode_order_identities episode_order
		  ON episode_order.title_id = next_episode.id AND episode_order.namespace = 'episode'
		ORDER BY latest.last_watched_at DESC, latest.series_id
		LIMIT $2
	`

func nextEpisodeItems(ctx context.Context, tx pgx.Tx, profileID string, activeSeries map[string]struct{}, limit int) ([]ContinueItem, error) {
	if limit < 1 {
		return []ContinueItem{}, nil
	}
	rows, err := tx.Query(ctx, nextEpisodeQuery, profileID, limit+len(activeSeries))
	if err != nil {
		return nil, fmt.Errorf("query next episodes: %w", err)
	}
	defer rows.Close()
	items := make([]ContinueItem, 0)
	for rows.Next() {
		var item ContinueItem
		if err := rows.Scan(
			&item.TitleID, &item.SeriesID, &item.SeasonID, &item.SeasonNumber,
			&item.EpisodeNumber, &item.MappingProvider, &item.EpisodeOrderID,
			&item.MetadataSeasonID, &item.Title, &item.PosterURL,
			&item.BackgroundURL, &item.ReleaseInfo, &item.ResourceID,
			&item.ResourceProvider, &item.EpisodeTitle, &item.EpisodeStillURL,
			&item.EpisodeAirDate, &item.LastWatchedAt,
		); err != nil {
			return nil, fmt.Errorf("scan next episode: %w", err)
		}
		if _, exists := activeSeries[item.SeriesID]; exists {
			continue
		}
		item.MediaType = "episode"
		item.Reason = "next_episode"
		items = append(items, item)
		if len(items) == limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate next episodes: %w", err)
	}
	return items, nil
}

func progressForProfile(ctx context.Context, tx pgx.Tx, profileID, titleID string) (Progress, error) {
	var progress Progress
	err := scanProgress(tx.QueryRow(ctx, `
		SELECT progress.title_id::text, title.media_type,
		       progress.position_seconds, progress.duration_seconds,
		       progress.completed, progress.version,
		       progress.last_watched_at, progress.updated_at
		FROM profile_progress progress
		JOIN titles title ON title.id = progress.title_id
		WHERE progress.profile_id = $1::uuid AND progress.title_id = $2::uuid
	`, profileID, titleID), &progress)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Progress{}, err
		}
		return Progress{}, fmt.Errorf("query playback progress: %w", err)
	}
	return progress, nil
}

func accessibleTitleMediaType(ctx context.Context, tx pgx.Tx, profileID, titleID string) (string, error) {
	var (
		mediaType     string
		sourceAddonID *string
	)
	if err := tx.QueryRow(ctx, `
		SELECT accessible.media_type, title.source_addon_id::text
		FROM (`+accessibleTitlesSQL+`) accessible
		JOIN titles title ON title.id = accessible.id
		WHERE accessible.id = $2::uuid
	`, profileID, titleID).Scan(&mediaType, &sourceAddonID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("authorize title access: %w", err)
	}
	if sourceAddonID == nil {
		return mediaType, nil
	}
	var lockedAddonID string
	if err := tx.QueryRow(ctx, `
		/* watchstate.lock_title_addon */
		SELECT addon.id::text
		FROM profile_addons addon
		JOIN profiles profile ON profile.id = $1::uuid
		WHERE addon.id = $2::uuid
		  AND addon.enabled = true
		  AND (
		      EXISTS (
		          SELECT 1
		          FROM addon_profile_access access
		          WHERE access.addon_id = addon.id
		            AND access.profile_id = profile.id
		      ) OR EXISTS (
		          SELECT 1
		          FROM addon_category_access access
		          WHERE access.addon_id = addon.id
		            AND access.category_id = profile.category_id
		      )
		  )
		FOR SHARE OF addon
	`, profileID, *sourceAddonID).Scan(&lockedAddonID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errAddonAccessChanged
		}
		return "", fmt.Errorf("lock title addon access: %w", err)
	}
	return mediaType, nil
}

func authorizeTitleSnapshotProfiles(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, titleID string) error {
	var profileIDs []string
	if err := tx.QueryRow(ctx, `
		SELECT ARRAY(
			SELECT profile_id::text
			FROM (
				SELECT $2::uuid AS profile_id
				UNION
				SELECT profile_id FROM profile_library WHERE title_id = $1::uuid
				UNION
				SELECT profile_id FROM profile_progress WHERE title_id = $1::uuid
				UNION
				SELECT profile_id FROM profile_favorites WHERE title_id = $1::uuid
			) affected
			ORDER BY profile_id::text
		)
	`, titleID, activeProfileID).Scan(&profileIDs); err != nil {
		return fmt.Errorf("query profiles affected by title snapshot: %w", err)
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, profileIDs, true)
	if err != nil {
		return fmt.Errorf("authorize affected title snapshot profiles: %w", err)
	}
	if !authorized {
		return ErrForbidden
	}
	return nil
}

func authorizedActiveManagerProfileID(ctx context.Context, tx pgx.Tx, principal auth.Principal) (string, error) {
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil {
		return "", err
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return "", fmt.Errorf("authorize active watchstate profile management: %w", err)
	}
	if !authorized {
		return "", ErrForbidden
	}
	return profileID, nil
}

func authorizedActiveProfileID(ctx context.Context, tx pgx.Tx, principal auth.Principal) (string, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return "", err
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil {
		return "", fmt.Errorf("authorize active watchstate profile: %w", err)
	}
	if !authorized {
		return "", ErrProfileRequired
	}
	valid, err := auth.LockActiveProfileSelection(ctx, tx, principal)
	if err != nil {
		return "", fmt.Errorf("lock active watchstate selection: %w", err)
	}
	if !valid {
		return "", ErrProfileRequired
	}
	return profileID, nil
}

func activeProfileID(principal auth.Principal) (string, error) {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(time.Now().UTC()) {
		return "", ErrProfileRequired
	}
	return *principal.ActiveProfileID, nil
}

func tvExternalID(sourceAddonID, resourceID string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(sourceAddonID+"\x00"+resourceID)))
}

func normalizeTitleID(titleID string) (string, error) {
	titleID = strings.TrimSpace(titleID)
	if !uuidPattern.MatchString(titleID) {
		return "", fmt.Errorf("%w: titleId must be a UUID", ErrInvalidInput)
	}
	return strings.ToLower(titleID), nil
}

func normalizeProgressBatchTitleIDs(titleIDs []string) ([]string, error) {
	if len(titleIDs) < 1 || len(titleIDs) > MaximumProgressBatchSize {
		return nil, fmt.Errorf("%w: titleIds must contain between 1 and %d items", ErrInvalidInput, MaximumProgressBatchSize)
	}
	normalized := make([]string, len(titleIDs))
	seen := make(map[string]struct{}, len(titleIDs))
	for index, titleID := range titleIDs {
		titleID, err := normalizeTitleID(titleID)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[titleID]; exists {
			return nil, fmt.Errorf("%w: duplicate titleId", ErrInvalidInput)
		}
		seen[titleID] = struct{}{}
		normalized[index] = titleID
	}
	return normalized, nil
}

func normalizeSetWatchedBatchInput(input []SetWatchedBatchItem) ([]SetWatchedBatchItem, error) {
	titleIDs := make([]string, len(input))
	for index := range input {
		titleIDs[index] = input[index].TitleID
		if input[index].ExpectedVersion < 0 {
			return nil, fmt.Errorf("%w: expectedVersion must be zero or greater", ErrInvalidInput)
		}
	}
	normalized, err := normalizeProgressBatchTitleIDs(titleIDs)
	if err != nil {
		return nil, err
	}
	result := make([]SetWatchedBatchItem, len(input))
	for index := range input {
		result[index] = input[index]
		result[index].TitleID = normalized[index]
	}
	return result, nil
}

func normalizeLibraryQuery(mediaType string, page, pageSize int) (string, int, int, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "" && mediaType != "movie" && mediaType != "series" && mediaType != "tv" {
		return "", 0, 0, fmt.Errorf("%w: mediaType must be movie, series, or tv", ErrInvalidInput)
	}
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if page < 1 || pageSize < 1 || pageSize > maximumPageSize {
		return "", 0, 0, fmt.Errorf("%w: invalid pagination", ErrInvalidInput)
	}
	return mediaType, page, pageSize, nil
}

func validateProgressInput(input UpdateProgressInput) error {
	if input.ExpectedVersion < 0 {
		return fmt.Errorf("%w: expectedVersion must be zero or greater", ErrInvalidInput)
	}
	if input.PositionSeconds < 0 || input.DurationSeconds < 0 {
		return fmt.Errorf("%w: playback times must be zero or greater", ErrInvalidInput)
	}
	if input.DurationSeconds == 0 && input.PositionSeconds != 0 {
		return fmt.Errorf("%w: positionSeconds requires a duration", ErrInvalidInput)
	}
	if input.DurationSeconds > 0 && input.PositionSeconds > input.DurationSeconds {
		return fmt.Errorf("%w: positionSeconds cannot exceed durationSeconds", ErrInvalidInput)
	}
	return nil
}

type progressScanner interface {
	Scan(dest ...any) error
}

func scanProgress(scanner progressScanner, progress *Progress) error {
	return scanner.Scan(
		&progress.TitleID,
		&progress.MediaType,
		&progress.PositionSeconds,
		&progress.DurationSeconds,
		&progress.Completed,
		&progress.Version,
		&progress.LastWatchedAt,
		&progress.UpdatedAt,
	)
}
