package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

const providerName = "tmdb"

var (
	languagePattern         = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z]{2})?$`)
	regionPattern           = regexp.MustCompile(`^[A-Za-z]{2}$`)
	externalProviderPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

type Service struct {
	pool     *pgxpool.Pool
	provider Provider
	cacheTTL time.Duration
	enricher TelevisionEnricher
	logger   *slog.Logger
}

func NewService(pool *pgxpool.Pool, provider Provider, enricher TelevisionEnricher, cacheTTL time.Duration, logger *slog.Logger) *Service {
	return &Service{pool: pool, provider: provider, enricher: enricher, cacheTTL: cacheTTL, logger: logger}
}

func (s *Service) DiscoverMovies(ctx context.Context, principal auth.Principal, options QueryOptions) (MoviePage, error) {
	if err := requireActiveProfile(principal); err != nil {
		return MoviePage{}, err
	}
	normalized, err := normalizeQueryOptions(options)
	if err != nil {
		return MoviePage{}, err
	}
	if s.provider == nil {
		return MoviePage{}, ErrProviderUnavailable
	}
	page, err := s.provider.DiscoverMovies(ctx, normalized)
	if err != nil {
		return MoviePage{}, err
	}
	return s.persistMoviePage(ctx, page)
}

func (s *Service) SearchMovies(ctx context.Context, principal auth.Principal, options SearchOptions) (MoviePage, error) {
	if err := requireActiveProfile(principal); err != nil {
		return MoviePage{}, err
	}
	normalized, err := normalizeQueryOptions(options.QueryOptions)
	if err != nil {
		return MoviePage{}, err
	}
	query := strings.TrimSpace(options.Query)
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) < 1 || utf8.RuneCountInString(query) > 200 {
		return MoviePage{}, fmt.Errorf("%w: query must contain 1 to 200 characters", ErrInvalidInput)
	}
	if s.provider == nil {
		return MoviePage{}, ErrProviderUnavailable
	}
	page, err := s.provider.SearchMovies(ctx, SearchOptions{QueryOptions: normalized, Query: query})
	if err != nil {
		return MoviePage{}, err
	}
	return s.persistMoviePage(ctx, page)
}

func (s *Service) MovieDetails(ctx context.Context, principal auth.Principal, titleID, language string) (Movie, error) {
	if err := requireActiveProfile(principal); err != nil {
		return Movie{}, err
	}
	normalizedLanguage, err := normalizeLanguage(language)
	if err != nil {
		return Movie{}, err
	}

	var externalID string
	var cachedPayload []byte
	err = s.pool.QueryRow(ctx, `
		SELECT external.external_id,
		       CASE WHEN metadata.expires_at > now() THEN metadata.payload ELSE NULL END
		FROM titles AS title
		JOIN title_external_ids AS external
		  ON external.title_id = title.id AND external.provider = $2
		LEFT JOIN title_metadata AS metadata
		  ON metadata.title_id = title.id
		 AND metadata.provider = external.provider
		 AND metadata.language = $3
		WHERE title.id::text = $1 AND title.media_type = 'movie'
	`, strings.TrimSpace(titleID), providerName, normalizedLanguage).Scan(&externalID, &cachedPayload)
	if errors.Is(err, pgx.ErrNoRows) {
		return Movie{}, ErrNotFound
	}
	if err != nil {
		return Movie{}, fmt.Errorf("query title metadata: %w", err)
	}
	if len(cachedPayload) != 0 {
		var cached Movie
		if err := json.Unmarshal(cachedPayload, &cached); err != nil {
			return Movie{}, fmt.Errorf("decode cached title metadata: %w", err)
		}
		return cached, nil
	}
	if s.provider == nil {
		return Movie{}, ErrProviderUnavailable
	}

	provided, err := s.provider.MovieDetails(ctx, externalID, normalizedLanguage)
	if err != nil {
		if errors.Is(err, ErrProviderNotFound) {
			return Movie{}, ErrNotFound
		}
		return Movie{}, err
	}
	movie := normalizeMovie(titleID, provided)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Movie{}, fmt.Errorf("begin movie persistence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := linkAdditionalIDs(ctx, tx, titleID, MediaTypeMovie, provided.AdditionalIDs); err != nil {
		return Movie{}, err
	}
	if err := cacheTitle(ctx, tx, titleID, normalizedLanguage, movie, s.cacheTTL); err != nil {
		return Movie{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Movie{}, fmt.Errorf("commit movie persistence: %w", err)
	}
	return movie, nil
}

func (s *Service) DiscoverSeries(ctx context.Context, principal auth.Principal, options QueryOptions) (SeriesPage, error) {
	if err := requireActiveProfile(principal); err != nil {
		return SeriesPage{}, err
	}
	normalized, err := normalizeQueryOptions(options)
	if err != nil {
		return SeriesPage{}, err
	}
	if s.provider == nil {
		return SeriesPage{}, ErrProviderUnavailable
	}
	page, err := s.provider.DiscoverSeries(ctx, normalized)
	if err != nil {
		return SeriesPage{}, err
	}
	return s.persistSeriesPage(ctx, page)
}

func (s *Service) SearchSeries(ctx context.Context, principal auth.Principal, options SearchOptions) (SeriesPage, error) {
	if err := requireActiveProfile(principal); err != nil {
		return SeriesPage{}, err
	}
	normalized, err := normalizeQueryOptions(options.QueryOptions)
	if err != nil {
		return SeriesPage{}, err
	}
	query := strings.TrimSpace(options.Query)
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) < 1 || utf8.RuneCountInString(query) > 200 {
		return SeriesPage{}, fmt.Errorf("%w: query must contain 1 to 200 characters", ErrInvalidInput)
	}
	if s.provider == nil {
		return SeriesPage{}, ErrProviderUnavailable
	}
	page, err := s.provider.SearchSeries(ctx, SearchOptions{QueryOptions: normalized, Query: query})
	if err != nil {
		return SeriesPage{}, err
	}
	return s.persistSeriesPage(ctx, page)
}

func (s *Service) SeriesDetails(ctx context.Context, principal auth.Principal, titleID, language string) (Series, error) {
	if err := requireActiveProfile(principal); err != nil {
		return Series{}, err
	}
	normalizedLanguage, err := normalizeLanguage(language)
	if err != nil {
		return Series{}, err
	}
	externalID, cachedPayload, err := s.loadTitleMetadata(ctx, titleID, MediaTypeSeries, normalizedLanguage)
	resolvedExternalID := false
	if errors.Is(err, ErrNotFound) {
		externalID, err = s.resolveProviderExternalID(ctx, titleID, MediaTypeSeries)
		resolvedExternalID = err == nil
	}
	if err != nil {
		return Series{}, err
	}
	if len(cachedPayload) != 0 {
		var cached Series
		if err := json.Unmarshal(cachedPayload, &cached); err != nil {
			return Series{}, fmt.Errorf("decode cached series metadata: %w", err)
		}
		return cached, nil
	}
	if s.provider == nil {
		return Series{}, ErrProviderUnavailable
	}
	provided, err := s.provider.SeriesDetails(ctx, externalID, normalizedLanguage)
	if err != nil {
		if errors.Is(err, ErrProviderNotFound) {
			return Series{}, ErrNotFound
		}
		return Series{}, err
	}

	if s.enricher != nil {
		enriched, enrichErr := s.enricher.EnrichSeries(ctx, provided)
		if enrichErr != nil {
			s.logEnrichmentFailure("series", titleID, enrichErr)
		} else {
			provided = enriched
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Series{}, fmt.Errorf("begin series persistence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if resolvedExternalID {
		if err := linkAdditionalIDs(ctx, tx, titleID, MediaTypeSeries, map[string]string{providerName: externalID}); err != nil {
			return Series{}, err
		}
	}
	if err := linkAdditionalIDs(ctx, tx, titleID, MediaTypeSeries, provided.AdditionalIDs); err != nil {
		return Series{}, err
	}
	seasons := make([]SeasonSummary, 0, len(provided.Seasons))
	for _, season := range provided.Seasons {
		if strings.TrimSpace(season.ExternalID) == "" || season.SeasonNumber < 0 {
			return Series{}, errors.New("metadata provider returned an invalid season")
		}
		seasonNumber := season.SeasonNumber
		seasonID, err := ensureTitle(ctx, tx, season.ExternalID, MediaTypeSeason, &titleID, &seasonNumber)
		if err != nil {
			return Series{}, err
		}
		seasons = append(seasons, normalizeSeasonSummary(titleID, seasonID, season))
	}
	series := normalizeSeries(titleID, provided)
	series.Seasons = seasons
	if err := cacheTitle(ctx, tx, titleID, normalizedLanguage, series, s.cacheTTL); err != nil {
		return Series{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Series{}, fmt.Errorf("commit series persistence: %w", err)
	}
	return series, nil
}

func (s *Service) SeasonDetails(ctx context.Context, principal auth.Principal, seasonID, language string) (Season, error) {
	if err := requireActiveProfile(principal); err != nil {
		return Season{}, err
	}
	normalizedLanguage, err := normalizeLanguage(language)
	if err != nil {
		return Season{}, err
	}

	var seriesID string
	var seriesExternalID string
	var seriesTVDBID *string
	var seasonExternalID string
	var seasonNumber int
	var cachedPayload []byte
	err = s.pool.QueryRow(ctx, `
		SELECT series.id::text,
		       series_external.external_id,
		       season_external.external_id,
		       season.ordinal,
		       tvdb_external.external_id,
		       CASE WHEN metadata.expires_at > now() THEN metadata.payload ELSE NULL END
		FROM titles AS season
		JOIN titles AS series
		  ON series.id = season.parent_id AND series.media_type = 'series'
		JOIN title_external_ids AS season_external
		  ON season_external.title_id = season.id
		 AND season_external.provider = $2
		 AND season_external.namespace = 'season'
		JOIN title_external_ids AS series_external
		  ON series_external.title_id = series.id
		 AND series_external.provider = $2
		 AND series_external.namespace = 'series'
		LEFT JOIN title_external_ids AS tvdb_external
		  ON tvdb_external.title_id = series.id
		 AND tvdb_external.provider = 'tvdb'
		 AND tvdb_external.namespace = 'series'
		LEFT JOIN title_metadata AS metadata
		  ON metadata.title_id = season.id
		 AND metadata.provider = $2
		 AND metadata.language = $3
		WHERE season.id::text = $1 AND season.media_type = 'season'
	`, strings.TrimSpace(seasonID), providerName, normalizedLanguage).Scan(
		&seriesID, &seriesExternalID, &seasonExternalID, &seasonNumber, &seriesTVDBID, &cachedPayload,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Season{}, ErrNotFound
	}
	if err != nil {
		return Season{}, fmt.Errorf("query season metadata: %w", err)
	}
	if len(cachedPayload) != 0 {
		var cached Season
		if err := json.Unmarshal(cachedPayload, &cached); err != nil {
			return Season{}, fmt.Errorf("decode cached season metadata: %w", err)
		}
		return cached, nil
	}
	if s.provider == nil {
		return Season{}, ErrProviderUnavailable
	}
	provided, err := s.provider.SeasonDetails(ctx, seriesExternalID, seasonNumber, normalizedLanguage)
	if err != nil {
		if errors.Is(err, ErrProviderNotFound) {
			return Season{}, ErrNotFound
		}
		return Season{}, err
	}
	if s.enricher != nil && seriesTVDBID != nil {
		enriched, enrichErr := s.enricher.EnrichSeason(ctx, *seriesTVDBID, provided)
		if enrichErr != nil {
			s.logEnrichmentFailure("season", seasonID, enrichErr)
		} else {
			provided = enriched
		}
	}
	if provided.ExternalID != seasonExternalID || provided.SeasonNumber != seasonNumber {
		return Season{}, errors.New("metadata provider returned a mismatched season")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Season{}, fmt.Errorf("begin season persistence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	episodes := make([]Episode, 0, len(provided.Episodes))
	for _, episode := range provided.Episodes {
		if strings.TrimSpace(episode.ExternalID) == "" || episode.EpisodeNumber < 0 || episode.SeasonNumber != seasonNumber {
			return Season{}, errors.New("metadata provider returned an invalid episode")
		}
		episodeNumber := episode.EpisodeNumber
		episodeID, err := ensureTitle(ctx, tx, episode.ExternalID, MediaTypeEpisode, &seasonID, &episodeNumber)
		if err != nil {
			return Season{}, err
		}
		if err := linkAdditionalIDs(ctx, tx, episodeID, MediaTypeEpisode, episode.AdditionalIDs); err != nil {
			return Season{}, err
		}
		episodes = append(episodes, normalizeEpisode(seasonID, episodeID, episode))
	}
	season := normalizeSeason(seriesID, seasonID, provided)
	season.Episodes = episodes
	if err := cacheTitle(ctx, tx, seasonID, normalizedLanguage, season, s.cacheTTL); err != nil {
		return Season{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Season{}, fmt.Errorf("commit season persistence: %w", err)
	}
	return season, nil
}

func (s *Service) persistMoviePage(ctx context.Context, provided ProviderMoviePage) (MoviePage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MoviePage{}, fmt.Errorf("begin title persistence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	items := make([]Movie, 0, len(provided.Items))
	for _, item := range provided.Items {
		if strings.TrimSpace(item.ExternalID) == "" {
			return MoviePage{}, errors.New("metadata provider returned an empty external ID")
		}
		titleID, err := ensureTitle(ctx, tx, item.ExternalID, MediaTypeMovie, nil, nil)
		if err != nil {
			return MoviePage{}, err
		}
		items = append(items, normalizeMovie(titleID, item))
	}
	if err := tx.Commit(ctx); err != nil {
		return MoviePage{}, fmt.Errorf("commit title persistence: %w", err)
	}
	return MoviePage{
		Items:        items,
		Page:         provided.Page,
		TotalPages:   provided.TotalPages,
		TotalResults: provided.TotalResults,
	}, nil
}

func (s *Service) persistSeriesPage(ctx context.Context, provided ProviderSeriesPage) (SeriesPage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SeriesPage{}, fmt.Errorf("begin series title persistence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	items := make([]Series, 0, len(provided.Items))
	for _, item := range provided.Items {
		if strings.TrimSpace(item.ExternalID) == "" {
			return SeriesPage{}, errors.New("metadata provider returned an empty external ID")
		}
		titleID, err := ensureTitle(ctx, tx, item.ExternalID, MediaTypeSeries, nil, nil)
		if err != nil {
			return SeriesPage{}, err
		}
		items = append(items, normalizeSeries(titleID, item))
	}
	if err := tx.Commit(ctx); err != nil {
		return SeriesPage{}, fmt.Errorf("commit series title persistence: %w", err)
	}
	return SeriesPage{
		Items:        items,
		Page:         provided.Page,
		TotalPages:   provided.TotalPages,
		TotalResults: provided.TotalResults,
	}, nil
}

func (s *Service) loadTitleMetadata(ctx context.Context, titleID, mediaType, language string) (string, []byte, error) {
	var externalID string
	var cachedPayload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT external.external_id,
		       CASE WHEN metadata.expires_at > now() THEN metadata.payload ELSE NULL END
		FROM titles AS title
		JOIN title_external_ids AS external
		  ON external.title_id = title.id
		 AND external.provider = $2
		 AND external.namespace = $3
		LEFT JOIN title_metadata AS metadata
		  ON metadata.title_id = title.id
		 AND metadata.provider = external.provider
		 AND metadata.language = $4
		WHERE title.id::text = $1 AND title.media_type = $3
	`, strings.TrimSpace(titleID), providerName, mediaType, language).Scan(&externalID, &cachedPayload)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("query title metadata: %w", err)
	}
	return externalID, cachedPayload, nil
}

func (s *Service) resolveProviderExternalID(ctx context.Context, titleID, mediaType string) (string, error) {
	resolver, ok := s.provider.(ExternalIDResolver)
	if !ok {
		return "", ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT external.provider, external.external_id
		FROM titles AS title
		JOIN title_external_ids AS external ON external.title_id = title.id
		WHERE title.id::text = $1
		  AND title.media_type = $2
		  AND external.namespace = $2
		  AND external.provider <> $3
		ORDER BY CASE external.provider WHEN 'imdb' THEN 0 WHEN 'tvdb' THEN 1 ELSE 2 END,
		         external.provider
	`, strings.TrimSpace(titleID), mediaType, providerName)
	if err != nil {
		return "", fmt.Errorf("query external title identities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var provider string
		var externalID string
		if err := rows.Scan(&provider, &externalID); err != nil {
			return "", fmt.Errorf("scan external title identity: %w", err)
		}
		resolved, resolveErr := resolver.ResolveExternalID(ctx, mediaType, provider, externalID)
		if resolveErr == nil {
			return resolved, nil
		}
		if !errors.Is(resolveErr, ErrProviderNotFound) {
			return "", resolveErr
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate external title identities: %w", err)
	}
	return "", ErrNotFound
}

func cacheTitle(ctx context.Context, tx pgx.Tx, titleID, language string, value any, ttl time.Duration) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode title metadata: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (title_id, provider, language) DO UPDATE
		SET payload = EXCLUDED.payload,
		    expires_at = EXCLUDED.expires_at,
		    updated_at = now()
	`, titleID, providerName, language, payload, time.Now().UTC().Add(ttl)); err != nil {
		return fmt.Errorf("cache title metadata: %w", err)
	}
	return nil
}

func ensureTitle(ctx context.Context, tx pgx.Tx, externalID, mediaType string, parentID *string, ordinal *int) (string, error) {
	lockKey := providerName + ":" + mediaType + ":" + externalID
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return "", fmt.Errorf("lock external title: %w", err)
	}

	var titleID string
	var existingParentID string
	var existingOrdinal int
	err := tx.QueryRow(ctx, `
		SELECT external.title_id::text,
		       COALESCE(title.parent_id::text, ''),
		       COALESCE(title.ordinal, -1)
		FROM title_external_ids AS external
		JOIN titles AS title ON title.id = external.title_id
		WHERE external.provider = $1 AND external.namespace = $2 AND external.external_id = $3
	`, providerName, mediaType, externalID).Scan(&titleID, &existingParentID, &existingOrdinal)
	if err == nil {
		expectedParentID := ""
		if parentID != nil {
			expectedParentID = *parentID
		}
		expectedOrdinal := -1
		if ordinal != nil {
			expectedOrdinal = *ordinal
		}
		if existingParentID != expectedParentID || existingOrdinal != expectedOrdinal {
			return "", errors.New("metadata provider returned a conflicting title hierarchy")
		}
		return titleID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("query external title: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO titles (media_type, parent_id, ordinal)
		VALUES ($1, $2::uuid, $3)
		RETURNING id::text
	`, mediaType, parentID, ordinal).Scan(&titleID); err != nil {
		return "", fmt.Errorf("create title: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, $2, $3, $4)
	`, titleID, providerName, mediaType, externalID); err != nil {
		return "", fmt.Errorf("link external title: %w", err)
	}
	return titleID, nil
}

func linkAdditionalIDs(ctx context.Context, tx pgx.Tx, titleID, namespace string, additionalIDs map[string]string) error {
	type providerIdentity struct {
		provider   string
		externalID string
	}
	identities := make([]providerIdentity, 0, len(additionalIDs))
	for rawProvider, rawExternalID := range additionalIDs {
		provider := strings.ToLower(strings.TrimSpace(rawProvider))
		externalID := strings.TrimSpace(rawExternalID)
		if provider == "" || externalID == "" {
			continue
		}
		if !externalProviderPattern.MatchString(provider) {
			return errors.New("metadata provider returned an invalid external ID provider")
		}
		identities = append(identities, providerIdentity{provider: provider, externalID: externalID})
	}
	sort.Slice(identities, func(left, right int) bool {
		return identities[left].provider < identities[right].provider
	})

	for _, identity := range identities {
		titleLockKey := "title:" + titleID + ":" + identity.provider + ":" + namespace
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", titleLockKey); err != nil {
			return fmt.Errorf("lock canonical title identity: %w", err)
		}
		externalLockKey := identity.provider + ":" + namespace + ":" + identity.externalID
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", externalLockKey); err != nil {
			return fmt.Errorf("lock provider identity: %w", err)
		}

		var existingExternalID string
		err := tx.QueryRow(ctx, `
			SELECT external_id
			FROM title_external_ids
			WHERE title_id = $1::uuid AND provider = $2 AND namespace = $3
		`, titleID, identity.provider, namespace).Scan(&existingExternalID)
		if err == nil {
			if existingExternalID != identity.externalID {
				return errors.New("metadata provider returned a conflicting external ID")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query canonical title identity: %w", err)
		}

		var mappedTitleID string
		err = tx.QueryRow(ctx, `
			SELECT title_id::text
			FROM title_external_ids
			WHERE provider = $1 AND namespace = $2 AND external_id = $3
		`, identity.provider, namespace, identity.externalID).Scan(&mappedTitleID)
		if err == nil {
			if mappedTitleID != titleID {
				return errors.New("metadata provider returned a conflicting external ID")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query provider identity: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
			VALUES ($1::uuid, $2, $3, $4)
		`, titleID, identity.provider, namespace, identity.externalID); err != nil {
			return fmt.Errorf("link provider identity: %w", err)
		}
	}
	return nil
}

func (s *Service) logEnrichmentFailure(mediaType, titleID string, err error) {
	if s.logger != nil {
		s.logger.Warn("optional metadata enrichment failed", "provider", "tvdb", "mediaType", mediaType, "titleId", titleID, "error", err)
	}
}

func normalizeMovie(titleID string, provided ProviderMovie) Movie {
	externalIDs := make(map[string]string, len(provided.AdditionalIDs)+1)
	externalIDs[providerName] = provided.ExternalID
	for provider, externalID := range provided.AdditionalIDs {
		if provider != "" && externalID != "" {
			externalIDs[provider] = externalID
		}
	}
	genres := provided.Genres
	if genres == nil {
		genres = []Genre{}
	}
	return Movie{
		ID:               titleID,
		MediaType:        MediaTypeMovie,
		Title:            provided.Title,
		OriginalTitle:    provided.OriginalTitle,
		OriginalLanguage: provided.OriginalLanguage,
		Overview:         provided.Overview,
		ReleaseDate:      provided.ReleaseDate,
		PosterURL:        provided.PosterURL,
		BackdropURL:      provided.BackdropURL,
		Tagline:          provided.Tagline,
		RuntimeMinutes:   provided.RuntimeMinutes,
		Genres:           genres,
		VoteAverage:      provided.VoteAverage,
		VoteCount:        provided.VoteCount,
		ExternalIDs:      externalIDs,
	}
}

func normalizeSeries(titleID string, provided ProviderSeries) Series {
	genres := provided.Genres
	if genres == nil {
		genres = []Genre{}
	}
	aliases := provided.Aliases
	if aliases == nil {
		aliases = []Alias{}
	}
	episodeOrders := provided.EpisodeOrders
	if episodeOrders == nil {
		episodeOrders = []EpisodeOrder{}
	}
	return Series{
		ID:               titleID,
		MediaType:        MediaTypeSeries,
		Name:             provided.Name,
		OriginalName:     provided.OriginalName,
		OriginalLanguage: provided.OriginalLanguage,
		Overview:         provided.Overview,
		FirstAirDate:     provided.FirstAirDate,
		LastAirDate:      provided.LastAirDate,
		PosterURL:        provided.PosterURL,
		BackdropURL:      provided.BackdropURL,
		Tagline:          provided.Tagline,
		Status:           provided.Status,
		NumberOfSeasons:  provided.NumberOfSeasons,
		NumberOfEpisodes: provided.NumberOfEpisodes,
		Genres:           genres,
		VoteAverage:      provided.VoteAverage,
		VoteCount:        provided.VoteCount,
		Seasons:          []SeasonSummary{},
		Aliases:          aliases,
		EpisodeOrders:    episodeOrders,
		ExternalIDs:      normalizeExternalIDs(provided.ExternalID, provided.AdditionalIDs),
	}
}

func normalizeSeasonSummary(seriesID, seasonID string, provided ProviderSeasonSummary) SeasonSummary {
	return SeasonSummary{
		ID:           seasonID,
		MediaType:    MediaTypeSeason,
		SeriesID:     seriesID,
		Name:         provided.Name,
		Overview:     provided.Overview,
		SeasonNumber: provided.SeasonNumber,
		EpisodeCount: provided.EpisodeCount,
		AirDate:      provided.AirDate,
		PosterURL:    provided.PosterURL,
		VoteAverage:  provided.VoteAverage,
		ExternalIDs:  normalizeExternalIDs(provided.ExternalID, nil),
	}
}

func normalizeSeason(seriesID, seasonID string, provided ProviderSeason) Season {
	return Season{
		ID:           seasonID,
		MediaType:    MediaTypeSeason,
		SeriesID:     seriesID,
		Name:         provided.Name,
		Overview:     provided.Overview,
		SeasonNumber: provided.SeasonNumber,
		AirDate:      provided.AirDate,
		PosterURL:    provided.PosterURL,
		VoteAverage:  provided.VoteAverage,
		Episodes:     []Episode{},
		ExternalIDs:  normalizeExternalIDs(provided.ExternalID, nil),
	}
}

func normalizeEpisode(seasonID, episodeID string, provided ProviderEpisode) Episode {
	return Episode{
		ID:             episodeID,
		MediaType:      MediaTypeEpisode,
		SeasonID:       seasonID,
		Name:           provided.Name,
		Overview:       provided.Overview,
		SeasonNumber:   provided.SeasonNumber,
		EpisodeNumber:  provided.EpisodeNumber,
		AirDate:        provided.AirDate,
		StillURL:       provided.StillURL,
		RuntimeMinutes: provided.RuntimeMinutes,
		VoteAverage:    provided.VoteAverage,
		VoteCount:      provided.VoteCount,
		ExternalIDs:    normalizeExternalIDs(provided.ExternalID, provided.AdditionalIDs),
	}
}

func normalizeExternalIDs(externalID string, additional map[string]string) map[string]string {
	externalIDs := make(map[string]string, len(additional)+1)
	externalIDs[providerName] = externalID
	for provider, value := range additional {
		if provider != "" && value != "" {
			externalIDs[provider] = value
		}
	}
	return externalIDs
}

func requireActiveProfile(principal auth.Principal) error {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(time.Now().UTC()) {
		return ErrProfileRequired
	}
	return nil
}

func normalizeQueryOptions(options QueryOptions) (QueryOptions, error) {
	page := options.Page
	if page == 0 {
		page = 1
	}
	if page < 1 || page > 500 {
		return QueryOptions{}, fmt.Errorf("%w: page must be between 1 and 500", ErrInvalidInput)
	}
	language, err := normalizeLanguage(options.Language)
	if err != nil {
		return QueryOptions{}, err
	}
	region := strings.ToUpper(strings.TrimSpace(options.Region))
	if region != "" && !regionPattern.MatchString(region) {
		return QueryOptions{}, fmt.Errorf("%w: region must be a two-letter country code", ErrInvalidInput)
	}
	return QueryOptions{Page: page, Language: language, Region: region}, nil
}

func normalizeLanguage(language string) (string, error) {
	language = strings.TrimSpace(language)
	if language == "" {
		return "en-US", nil
	}
	if !languagePattern.MatchString(language) {
		return "", fmt.Errorf("%w: language must be a language tag such as en-US", ErrInvalidInput)
	}
	parts := strings.Split(language, "-")
	parts[0] = strings.ToLower(parts[0])
	if len(parts) == 2 {
		parts[1] = strings.ToUpper(parts[1])
	}
	return strings.Join(parts, "-"), nil
}
