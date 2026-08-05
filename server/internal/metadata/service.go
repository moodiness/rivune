package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
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
	mappedSeasonPattern     = regexp.MustCompile(`^tvdb:([0-9a-fA-F-]{36}):([1-9][0-9]*)$`)
	episodeOrderPattern     = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
)

type Service struct {
	pool            *pgxpool.Pool
	provider        Provider
	resolver        ExternalIDResolver
	trailerProvider TrailerProvider
	cacheTTL        time.Duration
	enricher        TelevisionEnricher
	mapper          TelevisionMapper
	artwork         ArtworkEnricher
	logger          *slog.Logger
}

func NewService(pool *pgxpool.Pool, provider Provider, enricher TelevisionEnricher, artwork ArtworkEnricher, cacheTTL time.Duration, logger *slog.Logger) *Service {
	trailerProvider, _ := provider.(TrailerProvider)
	resolver, _ := provider.(ExternalIDResolver)
	mapper, _ := enricher.(TelevisionMapper)
	return &Service{
		pool: pool, provider: withEnglishOverviewFallback(provider), resolver: resolver, trailerProvider: trailerProvider,
		enricher: enricher, mapper: mapper, artwork: artwork, cacheTTL: cacheTTL, logger: logger,
	}
}

func (s *Service) DiscoverMovies(ctx context.Context, principal auth.Principal, options QueryOptions) (MoviePage, error) {
	if err := s.requireActiveProfile(ctx, principal); err != nil {
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
	return s.persistMoviePage(ctx, principal, page)
}

func (s *Service) SearchMovies(ctx context.Context, principal auth.Principal, options SearchOptions) (MoviePage, error) {
	if err := s.requireActiveProfile(ctx, principal); err != nil {
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
	return s.persistMoviePage(ctx, principal, page)
}

func (s *Service) MovieDetails(ctx context.Context, principal auth.Principal, titleID, language string) (Movie, error) {
	if err := s.requireActiveProfile(ctx, principal); err != nil {
		return Movie{}, err
	}
	return s.movieDetails(ctx, titleID, language, &principal)
}

func (s *Service) movieDetails(ctx context.Context, titleID, language string, principals ...*auth.Principal) (Movie, error) {
	normalizedLanguage, err := normalizeLanguage(language)
	if err != nil {
		return Movie{}, err
	}
	readTx, err := s.beginMetadataWorkTx(ctx, firstPrincipal(principals))
	if err != nil {
		return Movie{}, err
	}
	defer func() { _ = readTx.Rollback(ctx) }()
	externalID, cachedPayload, err := s.loadTitleMetadata(ctx, readTx, titleID, MediaTypeMovie, normalizedLanguage)
	titleMissing := errors.Is(err, ErrNotFound)
	if titleMissing {
		if commitErr := readTx.Commit(ctx); commitErr != nil {
			return Movie{}, fmt.Errorf("commit movie metadata read: %w", commitErr)
		}
		externalID, err = s.resolveProviderExternalID(ctx, titleID, MediaTypeMovie, firstPrincipal(principals))
	}
	if err != nil {
		return Movie{}, err
	}
	if len(cachedPayload) != 0 {
		var cached Movie
		if err := json.Unmarshal(cachedPayload, &cached); err != nil {
			return Movie{}, fmt.Errorf("decode cached title metadata: %w", err)
		}
		if cached.Cast != nil {
			if err := s.persistCachedMovieSnapshot(ctx, readTx, cached); err != nil {
				return Movie{}, err
			}
			if err := readTx.Commit(ctx); err != nil {
				return Movie{}, fmt.Errorf("commit cached movie snapshot persistence: %w", err)
			}
			return cached, nil
		}
	}
	if !titleMissing {
		if err := readTx.Commit(ctx); err != nil {
			return Movie{}, fmt.Errorf("commit movie metadata read: %w", err)
		}
	}
	if s.provider == nil {
		return Movie{}, ErrProviderUnavailable
	}

	provided, err := s.provider.MovieDetails(ctx, externalID, normalizedLanguage)
	if err != nil {
		if errors.Is(err, ErrProviderNotFound) {
			return Movie{}, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return Movie{}, err
	}
	if s.artwork != nil {
		enriched, enrichErr := s.artwork.EnrichMovie(ctx, provided, normalizedLanguage)
		if enrichErr != nil {
			if !errors.Is(enrichErr, ErrProviderNotFound) {
				s.logEnrichmentFailure("fanart", "movie", titleID, enrichErr)
			}
		} else {
			provided = enriched
		}
	}
	if strings.TrimSpace(provided.ExternalID) == "" || strings.TrimSpace(provided.Title) == "" {
		return Movie{}, fmt.Errorf("%w: metadata provider returned an invalid movie payload", ErrProviderFailure)
	}
	if returnedExternalID := strings.TrimSpace(provided.ExternalID); returnedExternalID != "" && returnedExternalID != externalID {
		return Movie{}, errors.New("metadata provider returned a conflicting external ID")
	}
	movie := normalizeMovie(titleID, provided)
	writeTx, err := s.beginMetadataWorkTx(ctx, firstPrincipal(principals))
	if err != nil {
		return Movie{}, err
	}
	defer func() { _ = writeTx.Rollback(ctx) }()
	canonicalIDs := make(map[string]string, len(provided.AdditionalIDs)+1)
	for provider, additionalID := range provided.AdditionalIDs {
		if strings.ToLower(strings.TrimSpace(provider)) == providerName &&
			strings.TrimSpace(additionalID) != "" &&
			strings.TrimSpace(additionalID) != externalID {
			return Movie{}, errors.New("metadata provider returned a conflicting external ID")
		}
		canonicalIDs[provider] = additionalID
	}
	canonicalIDs[providerName] = externalID
	if err := consolidateCanonicalTitle(ctx, writeTx, titleID, MediaTypeMovie, canonicalIDs); err != nil {
		return Movie{}, err
	}
	if err := persistTitleSnapshot(ctx, writeTx, titleID, provided.Title, provided.PosterURL, provided.BackdropURL, provided.ReleaseDate); err != nil {
		return Movie{}, err
	}
	if err := linkAdditionalIDs(ctx, writeTx, titleID, MediaTypeMovie, canonicalIDs); err != nil {
		return Movie{}, err
	}
	if err := cacheTitle(ctx, writeTx, titleID, providerName, normalizedLanguage, movie, s.cacheTTL); err != nil {
		return Movie{}, err
	}
	if err := writeTx.Commit(ctx); err != nil {
		return Movie{}, fmt.Errorf("commit movie persistence: %w", err)
	}
	return movie, nil
}

func (s *Service) DiscoverSeries(ctx context.Context, principal auth.Principal, options QueryOptions) (SeriesPage, error) {
	if err := s.requireActiveProfile(ctx, principal); err != nil {
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
	return s.persistSeriesPage(ctx, principal, page)
}

func (s *Service) SearchSeries(ctx context.Context, principal auth.Principal, options SearchOptions) (SeriesPage, error) {
	if err := s.requireActiveProfile(ctx, principal); err != nil {
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
	return s.persistSeriesPage(ctx, principal, page)
}

func (s *Service) SeriesDetails(ctx context.Context, principal auth.Principal, titleID string, options SeriesDetailsOptions) (Series, error) {
	if err := s.requireActiveProfile(ctx, principal); err != nil {
		return Series{}, err
	}
	mappingProvider, err := normalizeSeriesMappingProvider(options.MappingProvider)
	if err != nil {
		return Series{}, err
	}
	episodeOrderID, err := normalizeEpisodeOrderID(options.EpisodeOrderID)
	if err != nil {
		return Series{}, err
	}
	if episodeOrderID != "" && mappingProvider != "tvdb" {
		return Series{}, fmt.Errorf("%w: episodeOrder requires mappingProvider=tvdb", ErrInvalidInput)
	}
	if mappingProvider == "tvdb" {
		options.MappingProvider = mappingProvider
		options.EpisodeOrderID = episodeOrderID
		return s.mappedSeriesDetails(ctx, principal, titleID, options)
	}
	return s.seriesDetails(ctx, titleID, options, &principal)
}

func (s *Service) seriesDetails(ctx context.Context, titleID string, options SeriesDetailsOptions, principals ...*auth.Principal) (Series, error) {
	normalizedLanguage, err := normalizeLanguage(options.Language)
	if err != nil {
		return Series{}, err
	}
	readTx, err := s.beginMetadataWorkTx(ctx, firstPrincipal(principals))
	if err != nil {
		return Series{}, err
	}
	defer func() { _ = readTx.Rollback(ctx) }()
	externalID, cachedPayload, err := s.loadTitleMetadata(ctx, readTx, titleID, MediaTypeSeries, normalizedLanguage)
	titleMissing := errors.Is(err, ErrNotFound)
	if titleMissing {
		if commitErr := readTx.Commit(ctx); commitErr != nil {
			return Series{}, fmt.Errorf("commit series metadata read: %w", commitErr)
		}
		externalID, err = s.resolveProviderExternalID(ctx, titleID, MediaTypeSeries, firstPrincipal(principals))
	}
	if err != nil {
		return Series{}, err
	}
	if len(cachedPayload) != 0 {
		var cached Series
		if err := json.Unmarshal(cachedPayload, &cached); err != nil {
			return Series{}, fmt.Errorf("decode cached series metadata: %w", err)
		}
		if cached.Cast != nil {
			if cached.MappingProvider == "" {
				cached.MappingProvider = providerName
			}
			cached.EpisodeOrders = normalizeEpisodeOrders(cached.EpisodeOrders)
			if err := s.persistCachedSeriesSnapshots(ctx, readTx, cached); err != nil {
				return Series{}, err
			}
			if err := readTx.Commit(ctx); err != nil {
				return Series{}, fmt.Errorf("commit cached series snapshot persistence: %w", err)
			}
			return cached, nil
		}
	}
	if !titleMissing {
		if err := readTx.Commit(ctx); err != nil {
			return Series{}, fmt.Errorf("commit series metadata read: %w", err)
		}
	}
	if s.provider == nil {
		return Series{}, ErrProviderUnavailable
	}
	provided, err := s.provider.SeriesDetails(ctx, externalID, normalizedLanguage)
	if err != nil {
		if errors.Is(err, ErrProviderNotFound) {
			return Series{}, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return Series{}, err
	}

	if s.enricher != nil {
		enriched, enrichErr := s.enricher.EnrichSeries(ctx, provided)
		if enrichErr != nil {
			s.logEnrichmentFailure("tvdb", "series", titleID, enrichErr)
		} else {
			provided = enriched
		}
	}
	if s.artwork != nil {
		enriched, enrichErr := s.artwork.EnrichSeries(ctx, provided, normalizedLanguage)
		if enrichErr != nil {
			if !errors.Is(enrichErr, ErrProviderNotFound) {
				s.logEnrichmentFailure("fanart", "series", titleID, enrichErr)
			}
		} else {
			provided = enriched
		}
	}
	if strings.TrimSpace(provided.ExternalID) == "" || strings.TrimSpace(provided.Name) == "" {
		return Series{}, fmt.Errorf("%w: metadata provider returned an invalid series payload", ErrProviderFailure)
	}
	if returnedExternalID := strings.TrimSpace(provided.ExternalID); returnedExternalID != "" && returnedExternalID != externalID {
		return Series{}, errors.New("metadata provider returned a conflicting external ID")
	}
	writeTx, err := s.beginMetadataWorkTx(ctx, firstPrincipal(principals))
	if err != nil {
		return Series{}, err
	}
	defer func() { _ = writeTx.Rollback(ctx) }()
	canonicalIDs := make(map[string]string, len(provided.AdditionalIDs)+1)
	for provider, additionalID := range provided.AdditionalIDs {
		if strings.ToLower(strings.TrimSpace(provider)) == providerName &&
			strings.TrimSpace(additionalID) != "" &&
			strings.TrimSpace(additionalID) != externalID {
			return Series{}, errors.New("metadata provider returned a conflicting external ID")
		}
		canonicalIDs[provider] = additionalID
	}
	canonicalIDs[providerName] = externalID
	if err := consolidateCanonicalTitle(ctx, writeTx, titleID, MediaTypeSeries, canonicalIDs); err != nil {
		return Series{}, err
	}
	if err := linkAdditionalIDs(ctx, writeTx, titleID, MediaTypeSeries, canonicalIDs); err != nil {
		return Series{}, err
	}
	if err := persistTitleSnapshot(ctx, writeTx, titleID, provided.Name, provided.PosterURL, provided.BackdropURL, provided.FirstAirDate); err != nil {
		return Series{}, err
	}
	seasons := make([]SeasonSummary, 0, len(provided.Seasons))
	for _, season := range provided.Seasons {
		if strings.TrimSpace(season.ExternalID) == "" || season.SeasonNumber < 0 {
			return Series{}, errors.New("metadata provider returned an invalid season")
		}
		seasonNumber := season.SeasonNumber
		seasonID, err := ensureCanonicalSeasonTitle(ctx, writeTx, season.ExternalID, &titleID, &seasonNumber)
		if err != nil {
			return Series{}, err
		}
		if err := persistTitleSnapshot(ctx, writeTx, seasonID, season.Name, season.PosterURL, season.BackdropURL, season.AirDate); err != nil {
			return Series{}, err
		}
		seasons = append(seasons, normalizeSeasonSummary(titleID, seasonID, season))
	}
	series := normalizeSeries(titleID, provided)
	series.Seasons = seasons
	if err := cacheTitle(ctx, writeTx, titleID, providerName, normalizedLanguage, series, s.cacheTTL); err != nil {
		return Series{}, err
	}
	if err := writeTx.Commit(ctx); err != nil {
		return Series{}, fmt.Errorf("commit series persistence: %w", err)
	}
	return series, nil
}

// RefreshMissing refreshes canonical localized metadata that is absent or
// expired. A bounded run processes one batch; an exhaustive run keeps loading
// batches until every existing or newly discovered payload has been attempted
// once.
func (s *Service) RefreshMissing(ctx context.Context, options RefreshMissingOptions) (RefreshResult, error) {
	language, err := normalizeLanguage(options.Language)
	if err != nil {
		return RefreshResult{}, err
	}
	if options.BatchSize < 1 || options.BatchSize > 100 {
		return RefreshResult{}, fmt.Errorf("%w: batch size must be between 1 and 100", ErrInvalidInput)
	}

	var result RefreshResult
	failedTitleSet := make(map[string]struct{})
	phase := int16(0)
	var phaseBoundary time.Time
	var phaseUpperBoundary time.Time
	phaseHadCandidates := false
	if options.Exhaustive {
		if err := s.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&phaseUpperBoundary); err != nil {
			return result, fmt.Errorf("query metadata refresh boundary: %w", err)
		}
		phase = -1
	}
	var cursor metadataRefreshCursor
	for {
		candidates, err := s.missingRefreshCandidates(
			ctx, language, options.BatchSize, phase, phaseBoundary, phaseUpperBoundary, cursor,
		)
		if err != nil {
			return result, err
		}
		if len(candidates) == 0 {
			if options.Exhaustive && (phase == -1 || phaseHadCandidates) {
				phase = 1
				phaseBoundary = phaseUpperBoundary
				if err := s.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&phaseUpperBoundary); err != nil {
					return result, fmt.Errorf("query metadata refresh boundary: %w", err)
				}
				phaseHadCandidates = false
				cursor = metadataRefreshCursor{}
				continue
			}
			return result, nil
		}
		result.Candidates += len(candidates)
		phaseHadCandidates = true
		for index, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				result.Failed += len(candidates) - index
				for _, remaining := range candidates[index:] {
					appendFailedTitle(&result, failedTitleSet, remaining.title)
				}
				return result, err
			}
			refreshErr, attempts := s.refreshMissingCandidate(ctx, candidate, language)
			if refreshErr == nil {
				result.Refreshed++
				continue
			}
			if ctx.Err() != nil {
				result.Failed += len(candidates) - index
				for _, remaining := range candidates[index:] {
					appendFailedTitle(&result, failedTitleSet, remaining.title)
				}
				return result, ctx.Err()
			}
			result.Failed++
			appendFailedTitle(&result, failedTitleSet, candidate.title)
			s.logMetadataRefreshFailure(candidate, refreshErr, attempts)
		}
		if !options.Exhaustive {
			return result, nil
		}
		cursor = candidates[len(candidates)-1].cursor()
	}
}

func (s *Service) missingRefreshCandidates(
	ctx context.Context,
	language string,
	batchSize int,
	phase int16,
	phaseBoundary time.Time,
	phaseUpperBoundary time.Time,
	cursor metadataRefreshCursor,
) ([]metadataRefreshCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT title.id::text,
		       btrim(title.display_title),
		       title.media_type,
		       identity.provider,
		       identity.external_id,
		       title.id::text,
		       COALESCE(series_identity.external_id, ''),
		       COALESCE(title.ordinal, -1),
		       COALESCE(cached.expires_at, '0001-01-01T00:00:00Z'::timestamptz)
		FROM titles AS title
		JOIN LATERAL (
		    SELECT external.provider, external.external_id
		    FROM title_external_ids AS external
		    WHERE external.title_id = title.id
		      AND external.namespace = title.media_type
		      AND (
		          (
		              external.provider = $1
		              AND external.external_id ~ '^[1-9][0-9]*$'
		              AND char_length(external.external_id) <= 18
		          )
		          OR (
		              title.media_type IN ('movie', 'series')
		              AND external.provider = 'imdb'
		              AND external.external_id ~ '^tt[0-9]+$'
		              AND char_length(external.external_id) <= 18
		          )
		      )
		    ORDER BY CASE external.provider WHEN $1 THEN 0 ELSE 1 END
		    LIMIT 1
		) AS identity ON true
		LEFT JOIN titles AS series
		  ON series.id = title.parent_id
		 AND series.media_type = 'series'
		LEFT JOIN title_external_ids AS series_identity
		  ON series_identity.title_id = series.id
		 AND series_identity.provider = $1
		 AND series_identity.namespace = 'series'
		 AND series_identity.external_id ~ '^[1-9][0-9]*$'
		 AND char_length(series_identity.external_id) <= 18
		LEFT JOIN title_metadata AS cached
		  ON cached.title_id = title.id
		 AND cached.provider = $1
		 AND cached.language = $2
		WHERE title.media_type IN ('movie', 'series', 'season')
		  AND (title.media_type IN ('movie', 'series') OR series_identity.external_id IS NOT NULL)
		  AND (cached.title_id IS NULL OR cached.expires_at <= now())
		  AND (
			$4::smallint = 0
			OR ($4::smallint = -1 AND title.created_at < $6)
			OR ($4::smallint = 1 AND title.created_at >= $5 AND title.created_at < $6)
		  )
		  AND (
			NOT $7::boolean
			OR ROW(COALESCE(cached.expires_at, '0001-01-01T00:00:00Z'::timestamptz), title.id)
			   > ROW($8::timestamptz, NULLIF($9, '')::uuid)
		  )
		ORDER BY cached.expires_at NULLS FIRST, title.id
		LIMIT $3
	`, providerName, language, batchSize, phase, phaseBoundary, phaseUpperBoundary, cursor.valid, cursor.expiresAt, cursor.titleID)
	if err != nil {
		return nil, fmt.Errorf("query metadata refresh candidates: %w", err)
	}
	candidates := make([]metadataRefreshCandidate, 0, batchSize)
	for rows.Next() {
		var value metadataRefreshCandidate
		if err := rows.Scan(
			&value.titleID,
			&value.title,
			&value.mediaType,
			&value.identityProvider,
			&value.externalID,
			&value.refreshTitleID,
			&value.seriesExternalID,
			&value.seasonNumber,
			&value.refreshOrderExpiresAt,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan metadata refresh candidate: %w", err)
		}
		value.requestedResource = value.resource(language)
		candidates = append(candidates, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate metadata refresh candidates: %w", err)
	}
	rows.Close()
	return candidates, nil
}

type metadataRefreshCandidate struct {
	titleID               string
	title                 string
	mediaType             string
	identityProvider      string
	externalID            string
	refreshTitleID        string
	seriesExternalID      string
	seasonNumber          int
	requestedResource     string
	refreshOrderExpiresAt time.Time
}

type metadataRefreshCursor struct {
	valid     bool
	expiresAt time.Time
	titleID   string
}

func (candidate metadataRefreshCandidate) cursor() metadataRefreshCursor {
	return metadataRefreshCursor{
		valid:     true,
		expiresAt: candidate.refreshOrderExpiresAt,
		titleID:   candidate.titleID,
	}
}

func (candidate metadataRefreshCandidate) resource(language string) string {
	var endpoint string
	query := url.Values{"language": {language}}
	if candidate.identityProvider == "imdb" {
		endpoint = "/find/" + candidate.externalID
		query = url.Values{"external_source": {"imdb_id"}}
	} else {
		switch candidate.mediaType {
		case MediaTypeMovie:
			endpoint = "/movie/" + candidate.externalID
			query.Set("append_to_response", "credits")
		case MediaTypeSeries:
			endpoint = "/tv/" + candidate.externalID
			query.Set("append_to_response", "external_ids,credits")
		case MediaTypeSeason:
			endpoint = "/tv/" + candidate.seriesExternalID + "/season/" + strconv.Itoa(candidate.seasonNumber)
		default:
			return candidate.mediaType
		}
	}
	if encoded := query.Encode(); encoded != "" {
		return endpoint + "?" + encoded
	}
	return endpoint
}

func (s *Service) refreshMissingCandidate(ctx context.Context, candidate metadataRefreshCandidate, language string) (error, int) {
	for attempt := 1; attempt <= 2; attempt++ {
		var refreshErr error
		switch candidate.mediaType {
		case MediaTypeMovie:
			_, refreshErr = s.movieDetails(ctx, candidate.titleID, language)
		case MediaTypeSeries:
			_, refreshErr = s.seriesDetails(ctx, candidate.titleID, SeriesDetailsOptions{Language: language})
		case MediaTypeSeason:
			_, refreshErr = s.seasonDetails(ctx, candidate.refreshTitleID, language)
		default:
			refreshErr = fmt.Errorf("%w: unsupported refresh media type %q", ErrInvalidInput, candidate.mediaType)
		}
		if refreshErr == nil {
			return nil, attempt
		}
		_, temporary, _ := ProviderErrorDetails(refreshErr)
		if attempt == 2 || !temporary || ctx.Err() != nil {
			return refreshErr, attempt
		}
		s.logMetadataRefreshError("metadata refresh candidate retrying", candidate, refreshErr, attempt)
	}
	panic("unreachable metadata refresh retry")
}

func (s *Service) logMetadataRefreshFailure(candidate metadataRefreshCandidate, refreshErr error, attempts int) {
	s.logMetadataRefreshError("metadata refresh candidate failed", candidate, refreshErr, attempts)
}

func (s *Service) logMetadataRefreshError(message string, candidate metadataRefreshCandidate, refreshErr error, attempts int) {
	if s.logger == nil {
		return
	}
	statusCode, temporary, resource := ProviderErrorDetails(refreshErr)
	if resource == "" {
		resource = candidate.requestedResource
	}
	attributes := []any{
		"titleId", candidate.titleID,
		"title", candidate.title,
		"mediaType", candidate.mediaType,
		"provider", providerName,
		"requestedResource", resource,
		"error", refreshErr,
		"temporary", temporary,
		"attempts", attempts,
	}
	if statusCode > 0 {
		attributes = append(attributes, "httpStatus", statusCode)
	}
	s.logger.Warn(message, attributes...)
}

const (
	maximumFailedTitles     = 10
	maximumFailedTitleRunes = 120
)

func appendFailedTitle(result *RefreshResult, seen map[string]struct{}, title string) {
	if len(result.FailedTitles) >= maximumFailedTitles {
		return
	}
	title = strings.Join(strings.Fields(strings.ToValidUTF8(title, "")), " ")
	if title == "" {
		return
	}
	runes := []rune(title)
	if len(runes) > maximumFailedTitleRunes {
		title = string(runes[:maximumFailedTitleRunes-3]) + "..."
	}
	key := strings.ToLower(title)
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	result.FailedTitles = append(result.FailedTitles, title)
}

func (s *Service) SeasonDetails(ctx context.Context, principal auth.Principal, seasonID, language, mappingProvider string) (Season, error) {
	if err := s.requireActiveProfile(ctx, principal); err != nil {
		return Season{}, err
	}
	mappingProvider, err := normalizeSeriesMappingProvider(mappingProvider)
	if err != nil {
		return Season{}, err
	}
	if mappingProvider == "tvdb" || strings.HasPrefix(strings.TrimSpace(seasonID), "tvdb:") {
		return s.mappedSeasonDetails(ctx, principal, seasonID, language)
	}
	return s.seasonDetails(ctx, seasonID, language, &principal)
}

func (s *Service) seasonDetails(ctx context.Context, seasonID, language string, principals ...*auth.Principal) (Season, error) {
	normalizedLanguage, err := normalizeLanguage(language)
	if err != nil {
		return Season{}, err
	}
	readTx, err := s.beginMetadataWorkTx(ctx, firstPrincipal(principals))
	if err != nil {
		return Season{}, err
	}
	defer func() { _ = readTx.Rollback(ctx) }()
	var seriesID string
	var seriesExternalID string
	var seriesTVDBID *string
	var seasonExternalID string
	var seasonNumber int
	var cachedPayload []byte
	err = readTx.QueryRow(ctx, `
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
		if cachedSeasonMatchesHierarchy(cached, seasonID, seriesID, seasonNumber) {
			if err := s.persistCachedSeasonSnapshot(ctx, readTx, cached); err != nil {
				return Season{}, err
			}
			if err := readTx.Commit(ctx); err != nil {
				return Season{}, fmt.Errorf("commit cached season snapshot persistence: %w", err)
			}
			return cached, nil
		}
	}
	if err := readTx.Commit(ctx); err != nil {
		return Season{}, fmt.Errorf("commit season metadata read: %w", err)
	}
	if s.provider == nil {
		return Season{}, ErrProviderUnavailable
	}
	provided, err := s.provider.SeasonDetails(ctx, seriesExternalID, seasonNumber, normalizedLanguage)
	if err != nil {
		if errors.Is(err, ErrProviderNotFound) {
			return Season{}, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return Season{}, err
	}
	if err := validateProviderSeasonHierarchy(provided, seasonExternalID, seasonNumber); err != nil {
		return Season{}, err
	}
	if s.enricher != nil && seriesTVDBID != nil {
		enriched, enrichErr := s.enricher.EnrichSeason(ctx, *seriesTVDBID, provided)
		if enrichErr != nil {
			s.logEnrichmentFailure("tvdb", "season", seasonID, enrichErr)
		} else if hierarchyErr := validateProviderSeasonHierarchy(enriched, seasonExternalID, seasonNumber); hierarchyErr != nil {
			s.logEnrichmentFailure("tvdb", "season", seasonID, hierarchyErr)
		} else {
			provided = enriched
		}
	}
	if s.artwork != nil && seriesTVDBID != nil {
		enriched, enrichErr := s.artwork.EnrichSeason(ctx, *seriesTVDBID, provided, normalizedLanguage)
		if enrichErr != nil {
			if !errors.Is(enrichErr, ErrProviderNotFound) {
				s.logEnrichmentFailure("fanart", "season", seasonID, enrichErr)
			}
		} else {
			provided = enriched
		}
	}

	writeTx, err := s.beginMetadataWorkTx(ctx, firstPrincipal(principals))
	if err != nil {
		return Season{}, err
	}
	defer func() { _ = writeTx.Rollback(ctx) }()
	if err := persistTitleSnapshot(ctx, writeTx, seasonID, provided.Name, provided.PosterURL, provided.BackdropURL, provided.AirDate); err != nil {
		return Season{}, err
	}
	episodes := make([]Episode, 0, len(provided.Episodes))
	for _, episode := range provided.Episodes {
		if strings.TrimSpace(episode.ExternalID) == "" || episode.EpisodeNumber < 0 || episode.SeasonNumber != seasonNumber {
			return Season{}, errors.New("metadata provider returned an invalid episode")
		}
		episodeNumber := episode.EpisodeNumber
		episodeID, err := ensureTitle(ctx, writeTx, episode.ExternalID, MediaTypeEpisode, &seasonID, &episodeNumber)
		if err != nil {
			return Season{}, fmt.Errorf("resolve episode %d canonical identity: %w", episodeNumber, err)
		}
		if tvdbID := strings.TrimSpace(episode.AdditionalIDs["tvdb"]); tvdbID != "" {
			if err := replaceTVDBEpisodeID(ctx, writeTx, episodeID, tvdbID); err != nil {
				return Season{}, fmt.Errorf("repair episode %d TVDB identity: %w", episodeNumber, err)
			}
		}
		if err := linkAdditionalIDs(ctx, writeTx, episodeID, MediaTypeEpisode, episode.AdditionalIDs); err != nil {
			return Season{}, fmt.Errorf("link episode %d provider identities: %w", episodeNumber, err)
		}
		if err := persistTitleSnapshot(ctx, writeTx, episodeID, episode.Name, episode.StillURL, episode.BackdropURL, episode.AirDate); err != nil {
			return Season{}, err
		}
		episodes = append(episodes, normalizeEpisode(seasonID, episodeID, episode))
	}
	season := normalizeSeason(seriesID, seasonID, provided)
	season.Episodes = episodes
	if err := cacheTitle(ctx, writeTx, seasonID, providerName, normalizedLanguage, season, s.cacheTTL); err != nil {
		return Season{}, err
	}
	if err := writeTx.Commit(ctx); err != nil {
		return Season{}, fmt.Errorf("commit season persistence: %w", err)
	}
	return season, nil
}

func (s *Service) mappedSeriesDetails(ctx context.Context, principal auth.Principal, titleID string, options SeriesDetailsOptions) (Series, error) {
	base, err := s.SeriesDetails(ctx, principal, titleID, SeriesDetailsOptions{
		Language:        options.Language,
		MappingProvider: providerName,
	})
	if err != nil {
		return Series{}, err
	}
	if s.mapper == nil {
		if options.EpisodeOrderID == "" {
			s.logTVDBMappingFallback(base.ID, ErrProviderUnavailable)
			return base, s.requireActiveProfile(ctx, principal)
		}
		return Series{}, ErrProviderUnavailable
	}
	normalizedLanguage, err := normalizeLanguage(options.Language)
	if err != nil {
		return Series{}, err
	}
	if options.EpisodeOrderID == "" {
		cacheTx, err := s.beginAuthorizedProfileTx(ctx, principal)
		if err != nil {
			return Series{}, err
		}
		defer func() { _ = cacheTx.Rollback(ctx) }()
		cachedPayload, err := s.loadCachedTitleMetadata(ctx, cacheTx, base.ID, MediaTypeSeries, "tvdb", normalizedLanguage)
		if err != nil {
			return Series{}, err
		}
		if len(cachedPayload) != 0 {
			var cached Series
			if err := json.Unmarshal(cachedPayload, &cached); err != nil {
				return Series{}, fmt.Errorf("decode cached TVDB series mapping: %w", err)
			}
			cached.Cast = base.Cast
			cached.EpisodeOrders = normalizeEpisodeOrders(cached.EpisodeOrders)
			if cached.SelectedEpisodeOrderID == "" {
				cached.SelectedEpisodeOrderID = defaultEpisodeOrderID(cached.EpisodeOrders)
			}
			if len(base.EpisodeOrders) == 0 || len(cached.EpisodeOrders) > 0 {
				if err := cacheTx.Commit(ctx); err != nil {
					return Series{}, fmt.Errorf("commit cached TVDB series mapping read: %w", err)
				}
				return cached, nil
			}
		}
		if err := cacheTx.Commit(ctx); err != nil {
			return Series{}, fmt.Errorf("commit cached TVDB series mapping read: %w", err)
		}
	}
	seriesTVDBID := strings.TrimSpace(base.ExternalIDs["tvdb"])
	if seriesTVDBID == "" {
		if options.EpisodeOrderID == "" {
			s.logTVDBMappingFallback(base.ID, ErrProviderNotFound)
			return base, s.requireActiveProfile(ctx, principal)
		}
		return Series{}, ErrProviderNotFound
	}
	providedSeasons, err := s.mapper.SeriesSeasons(ctx, seriesTVDBID, options.EpisodeOrderID)
	if err != nil {
		if options.EpisodeOrderID == "" && isProviderMappingUnavailable(err) {
			s.logTVDBMappingFallback(base.ID, err)
			return base, s.requireActiveProfile(ctx, principal)
		}
		return Series{}, err
	}
	if s.artwork != nil {
		enriched, enrichErr := s.artwork.EnrichSeries(ctx, ProviderSeries{
			AdditionalIDs: map[string]string{"tvdb": seriesTVDBID},
			Seasons:       providedSeasons,
		}, normalizedLanguage)
		if enrichErr != nil {
			if !errors.Is(enrichErr, ErrProviderNotFound) {
				s.logEnrichmentFailure("fanart", "series", titleID, enrichErr)
			}
		} else {
			providedSeasons = enriched.Seasons
		}
	}
	mapped := base
	mapped.MappingProvider = "tvdb"
	mapped.EpisodeOrders = normalizeEpisodeOrders(base.EpisodeOrders)
	mapped.SelectedEpisodeOrderID = options.EpisodeOrderID
	if mapped.SelectedEpisodeOrderID == "" {
		mapped.SelectedEpisodeOrderID = defaultEpisodeOrderID(mapped.EpisodeOrders)
	}
	mapped.Seasons = make([]SeasonSummary, 0, len(providedSeasons))
	mapped.NumberOfSeasons = 0
	mapped.NumberOfEpisodes = 0
	for _, provided := range providedSeasons {
		if strings.TrimSpace(provided.ExternalID) == "" || provided.SeasonNumber < 0 || provided.EpisodeCount < 0 {
			return Series{}, fmt.Errorf("%w: TVDB returned an invalid season", ErrProviderFailure)
		}
		if provided.SeasonNumber > 0 {
			mapped.NumberOfSeasons++
		}
		mapped.NumberOfEpisodes += provided.EpisodeCount
		seasonID := mappedSeasonID(base.ID, provided.ExternalID)
		mapped.Seasons = append(mapped.Seasons, SeasonSummary{
			ID:           seasonID,
			MediaType:    MediaTypeSeason,
			SeriesID:     base.ID,
			Name:         provided.Name,
			Overview:     provided.Overview,
			SeasonNumber: provided.SeasonNumber,
			EpisodeCount: provided.EpisodeCount,
			AirDate:      provided.AirDate,
			PosterURL:    provided.PosterURL,
			BackdropURL:  provided.BackdropURL,
			VoteAverage:  provided.VoteAverage,
			ExternalIDs:  map[string]string{"tvdb": provided.ExternalID},
		})
	}
	if options.EpisodeOrderID == "" {
		tx, err := s.beginAuthorizedProfileTx(ctx, principal)
		if err != nil {
			return Series{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := cacheTitle(ctx, tx, base.ID, "tvdb", normalizedLanguage, mapped, s.cacheTTL); err != nil {
			return Series{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Series{}, fmt.Errorf("commit TVDB series mapping cache: %w", err)
		}
		return mapped, nil
	}
	return mapped, s.requireActiveProfile(ctx, principal)
}

func isProviderMappingUnavailable(err error) bool {
	return errors.Is(err, ErrProviderUnavailable) ||
		errors.Is(err, ErrProviderUnauthorized) ||
		errors.Is(err, ErrProviderNotFound) ||
		errors.Is(err, ErrProviderRateLimited) ||
		errors.Is(err, ErrProviderFailure)
}

func (s *Service) logTVDBMappingFallback(titleID string, err error) {
	if s.logger != nil {
		s.logger.Warn("TVDB mapping unavailable; using canonical TMDB mapping", "titleId", titleID, "error", err)
	}
}

func (s *Service) mappedSeasonDetails(ctx context.Context, principal auth.Principal, seasonID, language string) (Season, error) {
	if s.mapper == nil {
		return Season{}, ErrProviderUnavailable
	}
	normalizedLanguage, err := normalizeLanguage(language)
	if err != nil {
		return Season{}, err
	}
	matches := mappedSeasonPattern.FindStringSubmatch(strings.TrimSpace(seasonID))
	if matches == nil {
		return Season{}, ErrNotFound
	}
	seriesID := matches[1]
	seasonTVDBID := matches[2]
	base, err := s.SeriesDetails(ctx, principal, seriesID, SeriesDetailsOptions{Language: language, MappingProvider: providerName})
	if err != nil {
		return Season{}, err
	}
	seriesTVDBID := strings.TrimSpace(base.ExternalIDs["tvdb"])
	if seriesTVDBID == "" {
		return Season{}, ErrProviderNotFound
	}
	provided, err := s.mapper.SeriesSeason(ctx, seriesTVDBID, seasonTVDBID)
	if err != nil {
		return Season{}, err
	}
	if s.artwork != nil {
		enriched, enrichErr := s.artwork.EnrichSeason(ctx, seriesTVDBID, provided, normalizedLanguage)
		if enrichErr != nil {
			if !errors.Is(enrichErr, ErrProviderNotFound) {
				s.logEnrichmentFailure("fanart", "season", seasonID, enrichErr)
			}
		} else {
			provided = enriched
		}
	}
	canonicalEpisodes := make([]Episode, 0, base.NumberOfEpisodes)
	for _, summary := range base.Seasons {
		season, loadErr := s.SeasonDetails(ctx, principal, summary.ID, language, providerName)
		if loadErr != nil {
			if ctx.Err() != nil {
				return Season{}, ctx.Err()
			}
			if s.logger != nil {
				s.logger.Warn(
					"canonical season unavailable while assembling TVDB season",
					"seriesId", seriesID,
					"selectedSeasonId", seasonID,
					"selectedSeasonNumber", provided.SeasonNumber,
					"canonicalSeasonId", summary.ID,
					"canonicalSeasonNumber", summary.SeasonNumber,
					"error", loadErr,
				)
			}
			continue
		}
		canonicalEpisodes = append(canonicalEpisodes, season.Episodes...)
	}
	episodes, tvdbLinks, err := matchMappedEpisodes(seasonID, provided.Episodes, canonicalEpisodes)
	if err != nil {
		return Season{}, err
	}
	tx, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Season{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for episodeID, tvdbID := range tvdbLinks {
		if err := replaceTVDBEpisodeID(ctx, tx, episodeID, tvdbID); err != nil {
			return Season{}, err
		}
	}
	if len(episodes) < len(provided.Episodes) {
		matchedTVDBIDs := make(map[string]struct{}, len(tvdbLinks))
		for _, tvdbID := range tvdbLinks {
			matchedTVDBIDs[tvdbID] = struct{}{}
		}
		seasonOrdinal := provided.SeasonNumber
		persistedSeasonID, persistErr := ensureProviderTitleHierarchy(
			ctx,
			tx,
			"tvdb",
			provided.ExternalID,
			MediaTypeSeason,
			&seriesID,
			&seasonOrdinal,
			false,
		)
		if persistErr != nil {
			return Season{}, persistErr
		}
		if persistErr := persistTitleSnapshot(ctx, tx, persistedSeasonID, provided.Name, provided.PosterURL, provided.BackdropURL, provided.AirDate); persistErr != nil {
			return Season{}, persistErr
		}
		for _, mapped := range provided.Episodes {
			tvdbID := strings.TrimSpace(mapped.ExternalID)
			if _, matched := matchedTVDBIDs[tvdbID]; matched {
				continue
			}
			if tvdbID == "" || mapped.SeasonNumber != provided.SeasonNumber || mapped.EpisodeNumber < 0 {
				return Season{}, fmt.Errorf("%w: TVDB returned an invalid special episode", ErrProviderFailure)
			}
			episodeOrdinal := mapped.EpisodeNumber
			episodeID, persistErr := ensureProviderTitleHierarchy(
				ctx,
				tx,
				"tvdb",
				tvdbID,
				MediaTypeEpisode,
				&persistedSeasonID,
				&episodeOrdinal,
				false,
			)
			if persistErr != nil {
				return Season{}, persistErr
			}
			if persistErr := linkAdditionalIDs(ctx, tx, episodeID, MediaTypeEpisode, mapped.AdditionalIDs); persistErr != nil {
				return Season{}, persistErr
			}
			if persistErr := persistTitleSnapshot(ctx, tx, episodeID, mapped.Name, mapped.StillURL, mapped.BackdropURL, mapped.AirDate); persistErr != nil {
				return Season{}, persistErr
			}
			externalIDs := make(map[string]string, len(mapped.AdditionalIDs)+1)
			for provider, externalID := range mapped.AdditionalIDs {
				externalIDs[provider] = externalID
			}
			externalIDs["tvdb"] = tvdbID
			episodes = append(episodes, Episode{
				ID:             episodeID,
				MediaType:      MediaTypeEpisode,
				SeasonID:       seasonID,
				Name:           mapped.Name,
				Overview:       mapped.Overview,
				SeasonNumber:   mapped.SeasonNumber,
				EpisodeNumber:  mapped.EpisodeNumber,
				AirDate:        mapped.AirDate,
				StillURL:       mapped.StillURL,
				BackdropURL:    mapped.BackdropURL,
				RuntimeMinutes: mapped.RuntimeMinutes,
				VoteAverage:    mapped.VoteAverage,
				VoteCount:      mapped.VoteCount,
				ExternalIDs:    externalIDs,
			})
		}
	}
	sort.Slice(episodes, func(left, right int) bool {
		return episodes[left].EpisodeNumber < episodes[right].EpisodeNumber
	})
	if err := tx.Commit(ctx); err != nil {
		return Season{}, fmt.Errorf("commit TVDB episode mapping persistence: %w", err)
	}
	return Season{
		ID:           seasonID,
		MediaType:    MediaTypeSeason,
		SeriesID:     seriesID,
		Name:         provided.Name,
		Overview:     provided.Overview,
		SeasonNumber: provided.SeasonNumber,
		AirDate:      provided.AirDate,
		PosterURL:    provided.PosterURL,
		BackdropURL:  provided.BackdropURL,
		VoteAverage:  provided.VoteAverage,
		Episodes:     episodes,
		ExternalIDs:  map[string]string{"tvdb": provided.ExternalID},
	}, nil
}

func matchMappedEpisodes(seasonID string, provided []ProviderEpisode, canonical []Episode) ([]Episode, map[string]string, error) {
	type candidate struct {
		episode Episode
		used    bool
	}
	candidates := make([]candidate, 0, len(canonical))
	for _, episode := range canonical {
		candidates = append(candidates, candidate{episode: episode})
	}
	findUnique := func(predicate func(Episode) bool) int {
		found := -1
		for index := range candidates {
			if candidates[index].used || !predicate(candidates[index].episode) {
				continue
			}
			if found >= 0 {
				return -1
			}
			found = index
		}
		return found
	}
	result := make([]Episode, 0, len(provided))
	links := make(map[string]string, len(provided))
	for _, mapped := range provided {
		tvdbID := strings.TrimSpace(mapped.ExternalID)
		if tvdbID == "" {
			return nil, nil, fmt.Errorf("%w: TVDB returned an episode without an identifier", ErrProviderFailure)
		}
		index := findUnique(func(episode Episode) bool { return episode.ExternalIDs["tvdb"] == tvdbID })
		mappedName := normalizedEpisodeName(mapped.Name)
		if index < 0 && mapped.AirDate != "" && mappedName != "" {
			index = findUnique(func(episode Episode) bool {
				return episode.AirDate == mapped.AirDate && normalizedEpisodeName(episode.Name) == mappedName
			})
		}
		if index < 0 && mapped.AirDate != "" {
			index = findUnique(func(episode Episode) bool { return episode.AirDate == mapped.AirDate })
		}
		if index < 0 && mappedName != "" {
			index = findUnique(func(episode Episode) bool { return normalizedEpisodeName(episode.Name) == mappedName })
		}
		if index < 0 {
			index = findUnique(func(episode Episode) bool {
				return episode.SeasonNumber == mapped.SeasonNumber && episode.EpisodeNumber == mapped.EpisodeNumber
			})
		}
		if index < 0 {
			continue
		}
		candidates[index].used = true
		canonicalEpisode := candidates[index].episode
		externalIDs := make(map[string]string, len(canonicalEpisode.ExternalIDs)+1)
		for provider, externalID := range canonicalEpisode.ExternalIDs {
			externalIDs[provider] = externalID
		}
		externalIDs["tvdb"] = tvdbID
		name := canonicalEpisode.Name
		if name == "" {
			name = mapped.Name
		}
		overview := canonicalEpisode.Overview
		if overview == "" {
			overview = mapped.Overview
		}
		airDate := mapped.AirDate
		if airDate == "" {
			airDate = canonicalEpisode.AirDate
		}
		stillURL := canonicalEpisode.StillURL
		if stillURL == "" {
			stillURL = mapped.StillURL
		}
		backdropURL := canonicalEpisode.BackdropURL
		if backdropURL == "" {
			backdropURL = mapped.BackdropURL
		}
		runtime := canonicalEpisode.RuntimeMinutes
		if runtime == 0 {
			runtime = mapped.RuntimeMinutes
		}
		result = append(result, Episode{
			ID:             canonicalEpisode.ID,
			MediaType:      MediaTypeEpisode,
			SeasonID:       seasonID,
			Name:           name,
			Overview:       overview,
			SeasonNumber:   mapped.SeasonNumber,
			EpisodeNumber:  mapped.EpisodeNumber,
			AirDate:        airDate,
			StillURL:       stillURL,
			BackdropURL:    backdropURL,
			RuntimeMinutes: runtime,
			VoteAverage:    canonicalEpisode.VoteAverage,
			VoteCount:      canonicalEpisode.VoteCount,
			ExternalIDs:    externalIDs,
		})
		links[canonicalEpisode.ID] = tvdbID
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].EpisodeNumber < result[right].EpisodeNumber
	})
	return result, links, nil
}

func normalizedEpisodeName(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			return unicode.ToLower(character)
		}
		return -1
	}, strings.TrimSpace(value))
}

func mappedSeasonID(seriesID, seasonTVDBID string) string {
	return "tvdb:" + seriesID + ":" + strings.TrimSpace(seasonTVDBID)
}

func (s *Service) persistMoviePage(ctx context.Context, principal auth.Principal, provided ProviderMoviePage) (MoviePage, error) {
	tx, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return MoviePage{}, err
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
		if err := persistMissingTitleSnapshot(ctx, tx, titleID, item.Title, item.PosterURL, item.BackdropURL, item.ReleaseDate); err != nil {
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

func (s *Service) persistSeriesPage(ctx context.Context, principal auth.Principal, provided ProviderSeriesPage) (SeriesPage, error) {
	tx, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return SeriesPage{}, err
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
		if err := persistMissingTitleSnapshot(ctx, tx, titleID, item.Name, item.PosterURL, item.BackdropURL, item.FirstAirDate); err != nil {
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

func (s *Service) loadTitleMetadata(ctx context.Context, tx pgx.Tx, titleID, mediaType, language string) (string, []byte, error) {
	var externalID string
	var cachedPayload []byte
	err := tx.QueryRow(ctx, `
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

func (s *Service) loadCachedTitleMetadata(ctx context.Context, tx pgx.Tx, titleID, mediaType, metadataProvider, language string) ([]byte, error) {
	var cachedPayload []byte
	err := tx.QueryRow(ctx, `
		SELECT CASE WHEN metadata.expires_at > now() THEN metadata.payload ELSE NULL END
		FROM titles AS title
		LEFT JOIN title_metadata AS metadata
		  ON metadata.title_id = title.id
		 AND metadata.provider = $3
		 AND metadata.language = $4
		WHERE title.id::text = $1 AND title.media_type = $2
	`, strings.TrimSpace(titleID), mediaType, metadataProvider, language).Scan(&cachedPayload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query cached title metadata: %w", err)
	}
	return cachedPayload, nil
}

func (s *Service) resolveProviderExternalID(ctx context.Context, titleID, mediaType string, principals ...*auth.Principal) (string, error) {
	if s.resolver == nil {
		return "", ErrNotFound
	}
	tx, err := s.beginMetadataWorkTx(ctx, firstPrincipal(principals))
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
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
	type identity struct {
		provider   string
		externalID string
	}
	identities := make([]identity, 0)
	for rows.Next() {
		var value identity
		if err := rows.Scan(&value.provider, &value.externalID); err != nil {
			rows.Close()
			return "", fmt.Errorf("scan external title identity: %w", err)
		}
		identities = append(identities, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", fmt.Errorf("iterate external title identities: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit external title identity read: %w", err)
	}
	var lastNotFound error
	for _, identity := range identities {
		resolved, resolveErr := s.resolver.ResolveExternalID(ctx, mediaType, identity.provider, identity.externalID)
		if resolveErr == nil {
			return resolved, nil
		}
		if !errors.Is(resolveErr, ErrProviderNotFound) {
			return "", resolveErr
		}
		lastNotFound = resolveErr
	}
	if lastNotFound != nil {
		return "", fmt.Errorf("%w: resolve provider identity: %w", ErrNotFound, lastNotFound)
	}
	return "", ErrNotFound
}

func cacheTitle(ctx context.Context, tx pgx.Tx, titleID, metadataProvider, language string, value any, ttl time.Duration) error {
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
	`, titleID, metadataProvider, language, payload, time.Now().UTC().Add(ttl)); err != nil {
		return fmt.Errorf("cache title metadata: %w", err)
	}
	return nil
}

func (s *Service) persistCachedMovieSnapshot(ctx context.Context, tx pgx.Tx, movie Movie) error {
	return persistTitleSnapshot(ctx, tx, movie.ID, movie.Title, movie.PosterURL, movie.BackdropURL, movie.ReleaseDate)
}

func (s *Service) persistCachedSeriesSnapshots(ctx context.Context, tx pgx.Tx, series Series) error {
	snapshots := make([]titleSnapshot, 0, len(series.Seasons)+1)
	snapshots = append(snapshots, titleSnapshot{
		id:            series.ID,
		title:         series.Name,
		posterURL:     series.PosterURL,
		backgroundURL: series.BackdropURL,
		releaseDate:   series.FirstAirDate,
	})
	for _, season := range series.Seasons {
		snapshots = append(snapshots, titleSnapshot{
			id:            season.ID,
			title:         season.Name,
			posterURL:     season.PosterURL,
			backgroundURL: season.BackdropURL,
			releaseDate:   season.AirDate,
		})
	}
	return persistTitleSnapshots(ctx, tx, snapshots)
}

func cachedSeasonMatchesHierarchy(season Season, seasonID, seriesID string, seasonNumber int) bool {
	if season.ID != seasonID || season.SeriesID != seriesID || season.SeasonNumber != seasonNumber {
		return false
	}
	for _, episode := range season.Episodes {
		if episode.SeasonID != seasonID || episode.SeasonNumber != seasonNumber {
			return false
		}
	}
	return true
}

func validateProviderSeasonHierarchy(season ProviderSeason, externalID string, seasonNumber int) error {
	if strings.TrimSpace(season.ExternalID) != externalID || season.SeasonNumber != seasonNumber {
		return fmt.Errorf("%w: metadata provider returned season %d for requested season %d", ErrProviderFailure, season.SeasonNumber, seasonNumber)
	}
	for _, episode := range season.Episodes {
		if strings.TrimSpace(episode.ExternalID) == "" || episode.EpisodeNumber < 0 || episode.SeasonNumber != seasonNumber {
			return fmt.Errorf("%w: metadata provider returned an invalid episode hierarchy for season %d", ErrProviderFailure, seasonNumber)
		}
	}
	return nil
}

func (s *Service) persistCachedSeasonSnapshot(ctx context.Context, tx pgx.Tx, season Season) error {
	snapshots := make([]titleSnapshot, 0, len(season.Episodes)+1)
	snapshots = append(snapshots, titleSnapshot{
		id:            season.ID,
		title:         season.Name,
		posterURL:     season.PosterURL,
		backgroundURL: season.BackdropURL,
		releaseDate:   season.AirDate,
	})
	for _, episode := range season.Episodes {
		snapshots = append(snapshots, titleSnapshot{
			id:            episode.ID,
			title:         episode.Name,
			posterURL:     episode.StillURL,
			backgroundURL: episode.BackdropURL,
			releaseDate:   episode.AirDate,
		})
	}
	return persistMissingTitleSnapshots(ctx, tx, snapshots)
}

func persistReleaseDate(ctx context.Context, tx pgx.Tx, titleID, releaseDate string) error {
	releaseDate = strings.TrimSpace(releaseDate)
	if releaseDate == "" {
		return nil
	}
	parsed, err := time.Parse(time.DateOnly, releaseDate)
	if err != nil || parsed.Year() < 1 || parsed.Format(time.DateOnly) != releaseDate {
		return errors.New("cached metadata contains an invalid release date")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE titles
		SET release_date = $2::date,
		    updated_at = now()
		WHERE id = $1::uuid
		  AND release_date IS DISTINCT FROM $2::date
	`, titleID, releaseDate); err != nil {
		return fmt.Errorf("persist cached release date: %w", err)
	}
	return nil
}

type titleSnapshot struct {
	id            string
	title         string
	posterURL     string
	backgroundURL string
	releaseDate   string
}

func normalizedTitleSnapshotColumns(snapshots []titleSnapshot) ([]string, []string, []string, []string, []string, error) {
	ids := make([]string, len(snapshots))
	titles := make([]string, len(snapshots))
	posterURLs := make([]string, len(snapshots))
	backgroundURLs := make([]string, len(snapshots))
	releaseDates := make([]string, len(snapshots))
	for index, snapshot := range snapshots {
		ids[index] = strings.TrimSpace(snapshot.id)
		titles[index] = strings.TrimSpace(snapshot.title)
		posterURLs[index] = strings.TrimSpace(snapshot.posterURL)
		backgroundURLs[index] = strings.TrimSpace(snapshot.backgroundURL)
		releaseDates[index] = strings.TrimSpace(snapshot.releaseDate)
		if releaseDates[index] == "" {
			continue
		}
		parsed, err := time.Parse(time.DateOnly, releaseDates[index])
		if err != nil || parsed.Year() < 1 || parsed.Format(time.DateOnly) != releaseDates[index] {
			return nil, nil, nil, nil, nil, errors.New("metadata provider returned an invalid release date")
		}
	}
	return ids, titles, posterURLs, backgroundURLs, releaseDates, nil
}

func persistTitleSnapshots(ctx context.Context, tx pgx.Tx, snapshots []titleSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	ids, titles, posterURLs, backgroundURLs, releaseDates, err := normalizedTitleSnapshotColumns(snapshots)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE titles AS title
		SET display_title = COALESCE(NULLIF(snapshot.display_title, ''), title.display_title),
		    poster_url = COALESCE(NULLIF(snapshot.poster_url, ''), title.poster_url),
		    background_url = COALESCE(NULLIF(snapshot.background_url, ''), title.background_url),
		    release_date = COALESCE(NULLIF(snapshot.release_date, '')::date, title.release_date),
		    updated_at = now()
		FROM unnest($1::uuid[], $2::text[], $3::text[], $4::text[], $5::text[])
		     AS snapshot(id, display_title, poster_url, background_url, release_date)
		WHERE title.id = snapshot.id
		  AND (
			title.display_title IS DISTINCT FROM COALESCE(NULLIF(snapshot.display_title, ''), title.display_title)
			OR title.poster_url IS DISTINCT FROM COALESCE(NULLIF(snapshot.poster_url, ''), title.poster_url)
			OR title.background_url IS DISTINCT FROM COALESCE(NULLIF(snapshot.background_url, ''), title.background_url)
			OR title.release_date IS DISTINCT FROM COALESCE(NULLIF(snapshot.release_date, '')::date, title.release_date)
		  )
	`, ids, titles, posterURLs, backgroundURLs, releaseDates); err != nil {
		return fmt.Errorf("persist title snapshots: %w", err)
	}
	return nil
}

func persistMissingTitleSnapshots(ctx context.Context, tx pgx.Tx, snapshots []titleSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	ids, titles, posterURLs, backgroundURLs, releaseDates, err := normalizedTitleSnapshotColumns(snapshots)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		WITH snapshots AS (
			SELECT *
			FROM unnest($1::uuid[], $2::text[], $3::text[], $4::text[], $5::text[])
			     AS snapshot(id, display_title, poster_url, background_url, release_date)
		),
		desired AS (
			SELECT title.id,
			       CASE WHEN btrim(COALESCE(title.display_title, '')) = ''
			            THEN COALESCE(NULLIF(snapshot.display_title, ''), title.display_title)
			            ELSE title.display_title END AS display_title,
			       CASE WHEN btrim(COALESCE(title.poster_url, '')) = ''
			            THEN COALESCE(NULLIF(snapshot.poster_url, ''), title.poster_url)
			            ELSE title.poster_url END AS poster_url,
			       CASE WHEN btrim(COALESCE(title.background_url, '')) = ''
			            THEN COALESCE(NULLIF(snapshot.background_url, ''), title.background_url)
			            ELSE title.background_url END AS background_url,
			       CASE WHEN title.release_date IS NULL
			            THEN COALESCE(NULLIF(snapshot.release_date, '')::date, title.release_date)
			            ELSE title.release_date END AS release_date
			FROM titles AS title
			JOIN snapshots AS snapshot ON snapshot.id = title.id
		)
		UPDATE titles AS title
		SET display_title = desired.display_title,
		    poster_url = desired.poster_url,
		    background_url = desired.background_url,
		    release_date = desired.release_date,
		    updated_at = now()
		FROM desired
		WHERE title.id = desired.id
		  AND (
			title.display_title IS DISTINCT FROM desired.display_title
			OR title.poster_url IS DISTINCT FROM desired.poster_url
			OR title.background_url IS DISTINCT FROM desired.background_url
			OR title.release_date IS DISTINCT FROM desired.release_date
		  )
	`, ids, titles, posterURLs, backgroundURLs, releaseDates); err != nil {
		return fmt.Errorf("persist missing title snapshots: %w", err)
	}
	return nil
}

func persistTitleSnapshot(ctx context.Context, tx pgx.Tx, titleID, title, posterURL, backgroundURL, releaseDate string) error {
	title = strings.TrimSpace(title)
	posterURL = strings.TrimSpace(posterURL)
	backgroundURL = strings.TrimSpace(backgroundURL)
	releaseDate = strings.TrimSpace(releaseDate)
	if releaseDate != "" {
		parsed, err := time.Parse(time.DateOnly, releaseDate)
		if err != nil || parsed.Year() < 1 || parsed.Format(time.DateOnly) != releaseDate {
			return errors.New("metadata provider returned an invalid release date")
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE titles
		SET display_title = COALESCE(NULLIF($2, ''), display_title),
		    poster_url = COALESCE(NULLIF($3, ''), poster_url),
		    background_url = COALESCE(NULLIF($4, ''), background_url),
		    release_date = COALESCE(NULLIF($5, '')::date, release_date),
		    updated_at = now()
		WHERE id = $1::uuid
		  AND (
			display_title IS DISTINCT FROM COALESCE(NULLIF($2, ''), display_title)
			OR poster_url IS DISTINCT FROM COALESCE(NULLIF($3, ''), poster_url)
			OR background_url IS DISTINCT FROM COALESCE(NULLIF($4, ''), background_url)
			OR release_date IS DISTINCT FROM COALESCE(NULLIF($5, '')::date, release_date)
		  )
	`, titleID, title, posterURL, backgroundURL, releaseDate); err != nil {
		return fmt.Errorf("persist title snapshot: %w", err)
	}
	return nil
}

func ensureTitle(ctx context.Context, tx pgx.Tx, externalID, mediaType string, parentID *string, ordinal *int) (string, error) {
	return ensureTitleHierarchy(ctx, tx, externalID, mediaType, parentID, ordinal, false)
}

func persistMissingTitleSnapshot(ctx context.Context, tx pgx.Tx, titleID, title, posterURL, backgroundURL, releaseDate string) error {
	title = strings.TrimSpace(title)
	posterURL = strings.TrimSpace(posterURL)
	backgroundURL = strings.TrimSpace(backgroundURL)
	releaseDate = strings.TrimSpace(releaseDate)
	if releaseDate != "" {
		parsed, err := time.Parse(time.DateOnly, releaseDate)
		if err != nil || parsed.Year() < 1 || parsed.Format(time.DateOnly) != releaseDate {
			return errors.New("metadata provider returned an invalid release date")
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE titles
		SET display_title = COALESCE(display_title, NULLIF($2, '')),
		    poster_url = COALESCE(poster_url, NULLIF($3, '')),
		    background_url = COALESCE(background_url, NULLIF($4, '')),
		    release_date = COALESCE(release_date, NULLIF($5, '')::date),
		    updated_at = now()
		WHERE id = $1::uuid
		  AND (
			(display_title IS NULL AND NULLIF($2, '') IS NOT NULL)
			OR (poster_url IS NULL AND NULLIF($3, '') IS NOT NULL)
			OR (background_url IS NULL AND NULLIF($4, '') IS NOT NULL)
			OR (release_date IS NULL AND NULLIF($5, '') IS NOT NULL)
		  )
	`, titleID, title, posterURL, backgroundURL, releaseDate); err != nil {
		return fmt.Errorf("persist missing title snapshot: %w", err)
	}
	return nil
}
func ensureCanonicalSeasonTitle(ctx context.Context, tx pgx.Tx, externalID string, parentID *string, ordinal *int) (string, error) {
	return ensureTitleHierarchy(ctx, tx, externalID, MediaTypeSeason, parentID, ordinal, true)
}

func ensureTitleHierarchy(ctx context.Context, tx pgx.Tx, externalID, mediaType string, parentID *string, ordinal *int, repairOrdinal bool) (string, error) {
	return ensureProviderTitleHierarchy(ctx, tx, providerName, externalID, mediaType, parentID, ordinal, repairOrdinal)
}

func ensureProviderTitleHierarchy(ctx context.Context, tx pgx.Tx, provider, externalID, mediaType string, parentID *string, ordinal *int, repairOrdinal bool) (string, error) {
	lockKeys := []string{provider + ":" + mediaType + ":" + externalID}
	if parentID != nil && ordinal != nil {
		lockKeys = append(lockKeys, "hierarchy:"+*parentID+":"+mediaType+":"+strconv.Itoa(*ordinal))
	}
	sort.Strings(lockKeys)
	for _, lockKey := range lockKeys {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
			return "", fmt.Errorf("lock external title: %w", err)
		}
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
	`, provider, mediaType, externalID).Scan(&titleID, &existingParentID, &existingOrdinal)
	if err == nil {
		expectedParentID := ""
		if parentID != nil {
			expectedParentID = *parentID
		}
		expectedOrdinal := -1
		if ordinal != nil {
			expectedOrdinal = *ordinal
		}
		if existingParentID == expectedParentID && existingOrdinal == expectedOrdinal {
			return titleID, nil
		}
		if !repairOrdinal || existingParentID != expectedParentID || ordinal == nil {
			return "", errors.New("metadata provider returned a conflicting title hierarchy")
		}
		commandTag, updateErr := tx.Exec(ctx, `
			UPDATE titles AS title
			SET ordinal = $2,
			    updated_at = now()
			WHERE title.id = $1::uuid
			  AND NOT EXISTS (
			      SELECT 1
			      FROM titles AS sibling
			      WHERE sibling.parent_id = title.parent_id
			        AND sibling.media_type = title.media_type
			        AND sibling.ordinal = $2
			        AND sibling.id <> title.id
			  )
		`, titleID, expectedOrdinal)
		if updateErr != nil {
			return "", fmt.Errorf("repair canonical season hierarchy: %w", updateErr)
		}
		if commandTag.RowsAffected() != 1 {
			return "", errors.New("metadata provider returned a conflicting title hierarchy")
		}
		if _, deleteErr := tx.Exec(ctx, `DELETE FROM title_metadata WHERE title_id = $1::uuid`, titleID); deleteErr != nil {
			return "", fmt.Errorf("invalidate repaired season metadata: %w", deleteErr)
		}
		return titleID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("query external title: %w", err)
	}
	if parentID != nil && ordinal != nil {
		err = tx.QueryRow(ctx, `
			SELECT id::text
			FROM titles
			WHERE parent_id = $1::uuid AND media_type = $2 AND ordinal = $3
		`, *parentID, mediaType, *ordinal).Scan(&titleID)
		if err == nil {
			var existingExternalID string
			identityErr := tx.QueryRow(ctx, `
				SELECT external_id
				FROM title_external_ids
				WHERE title_id = $1::uuid AND provider = $2
			`, titleID, provider).Scan(&existingExternalID)
			if identityErr == nil {
				if existingExternalID != externalID {
					return "", errors.New("metadata provider returned a conflicting external ID")
				}
				return titleID, nil
			}
			if !errors.Is(identityErr, pgx.ErrNoRows) {
				return "", fmt.Errorf("query hierarchical title identity: %w", identityErr)
			}
			if _, identityErr = tx.Exec(ctx, `
				INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
				VALUES ($1::uuid, $2, $3, $4)
			`, titleID, provider, mediaType, externalID); identityErr != nil {
				return "", fmt.Errorf("link hierarchical title identity: %w", identityErr)
			}
			return titleID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("query hierarchical title: %w", err)
		}
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
	`, titleID, provider, mediaType, externalID); err != nil {
		return "", fmt.Errorf("link external title: %w", err)
	}
	return titleID, nil
}

type providerIdentity struct {
	provider   string
	externalID string
}

func normalizeProviderIdentities(additionalIDs map[string]string) ([]providerIdentity, error) {
	byProvider := make(map[string]string, len(additionalIDs))
	for rawProvider, rawExternalID := range additionalIDs {
		provider := strings.ToLower(strings.TrimSpace(rawProvider))
		externalID := strings.TrimSpace(rawExternalID)
		if provider == "" || externalID == "" {
			continue
		}
		if !externalProviderPattern.MatchString(provider) {
			return nil, errors.New("metadata provider returned an invalid external ID provider")
		}
		if existing, ok := byProvider[provider]; ok && existing != externalID {
			return nil, errors.New("metadata provider returned a conflicting external ID")
		}
		byProvider[provider] = externalID
	}
	identities := make([]providerIdentity, 0, len(byProvider))
	for provider, externalID := range byProvider {
		identities = append(identities, providerIdentity{provider: provider, externalID: externalID})
	}
	sort.Slice(identities, func(left, right int) bool {
		if identities[left].provider == identities[right].provider {
			return identities[left].externalID < identities[right].externalID
		}
		return identities[left].provider < identities[right].provider
	})
	return identities, nil
}

func consolidateCanonicalTitle(ctx context.Context, tx pgx.Tx, destinationID, mediaType string, additionalIDs map[string]string) error {
	identities, err := normalizeProviderIdentities(additionalIDs)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "canonical-title-consolidation:"+mediaType); err != nil {
		return fmt.Errorf("lock canonical title consolidation: %w", err)
	}
	for _, identity := range identities {
		lockKey := identity.provider + ":" + mediaType + ":" + identity.externalID
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
			return fmt.Errorf("lock provider identity for consolidation: %w", err)
		}
	}

	sourceSet := make(map[string]struct{})
	for _, identity := range identities {
		var destinationExternalID string
		err := tx.QueryRow(ctx, `
			SELECT external_id
			FROM title_external_ids
			WHERE title_id = $1::uuid AND provider = $2
		`, destinationID, identity.provider).Scan(&destinationExternalID)
		if err == nil && destinationExternalID != identity.externalID {
			return errors.New("metadata provider returned a conflicting external ID")
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query canonical provider identity: %w", err)
		}

		var mappedTitleID string
		var mappedMediaType string
		err = tx.QueryRow(ctx, `
			SELECT external.title_id::text, title.media_type
			FROM title_external_ids AS external
			JOIN titles AS title ON title.id = external.title_id
			WHERE external.provider = $1
			  AND external.namespace = $2
			  AND external.external_id = $3
		`, identity.provider, mediaType, identity.externalID).Scan(&mappedTitleID, &mappedMediaType)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("query mapped canonical title: %w", err)
		}
		if mappedMediaType != mediaType {
			return errors.New("metadata provider returned a conflicting title media type")
		}
		if mappedTitleID != destinationID {
			sourceSet[mappedTitleID] = struct{}{}
		}
	}

	titleIDs := make([]string, 0, len(sourceSet)+1)
	titleIDs = append(titleIDs, destinationID)
	for sourceID := range sourceSet {
		titleIDs = append(titleIDs, sourceID)
	}
	sort.Strings(titleIDs)
	for _, titleID := range titleIDs {
		var storedMediaType string
		err := tx.QueryRow(ctx, `
			SELECT media_type
			FROM titles
			WHERE id = $1::uuid
			FOR UPDATE
		`, titleID).Scan(&storedMediaType)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock title for consolidation: %w", err)
		}
		if storedMediaType != mediaType {
			return errors.New("metadata provider returned a conflicting title media type")
		}
	}

	sourceIDs := make([]string, 0, len(sourceSet))
	for sourceID := range sourceSet {
		sourceIDs = append(sourceIDs, sourceID)
	}
	sort.Strings(sourceIDs)
	for _, sourceID := range sourceIDs {
		if err := mergeTitleTree(ctx, tx, destinationID, sourceID, mediaType); err != nil {
			return err
		}
	}
	return nil
}

func mergeTitleTree(ctx context.Context, tx pgx.Tx, destinationID, sourceID, mediaType string) error {
	var conflictingProvider string
	err := tx.QueryRow(ctx, `
		SELECT source.provider
		FROM title_external_ids AS source
		JOIN title_external_ids AS destination
		  ON destination.title_id = $1::uuid
		 AND destination.provider = source.provider
		WHERE source.title_id = $2::uuid
		  AND (source.namespace, source.external_id) <> (destination.namespace, destination.external_id)
		LIMIT 1
	`, destinationID, sourceID).Scan(&conflictingProvider)
	if err == nil {
		return errors.New("metadata provider returned a conflicting external ID")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("query title identity conflicts: %w", err)
	}

	var invalidNamespace bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM title_external_ids
			WHERE title_id IN ($1::uuid, $2::uuid)
			  AND namespace <> $3
		)
	`, destinationID, sourceID, mediaType).Scan(&invalidNamespace); err != nil {
		return fmt.Errorf("validate title identity namespaces: %w", err)
	}
	if invalidNamespace {
		return errors.New("metadata provider returned a conflicting title media type")
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM title_metadata
		WHERE title_id IN ($1::uuid, $2::uuid)
	`, destinationID, sourceID); err != nil {
		return fmt.Errorf("invalidate consolidated title metadata: %w", err)
	}
	if err := mergeProfileState(ctx, tx, destinationID, sourceID); err != nil {
		return err
	}

	rows, err := tx.Query(ctx, `
		SELECT id::text, media_type, ordinal
		FROM titles
		WHERE parent_id = $1::uuid
		ORDER BY media_type, ordinal, id
	`, sourceID)
	if err != nil {
		return fmt.Errorf("query duplicate title children: %w", err)
	}
	type child struct {
		id        string
		mediaType string
		ordinal   int
	}
	children := make([]child, 0)
	for rows.Next() {
		var item child
		if err := rows.Scan(&item.id, &item.mediaType, &item.ordinal); err != nil {
			rows.Close()
			return fmt.Errorf("scan duplicate title child: %w", err)
		}
		children = append(children, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate duplicate title children: %w", err)
	}
	rows.Close()

	for _, sourceChild := range children {
		var destinationChildID string
		err := tx.QueryRow(ctx, `
			SELECT id::text
			FROM titles
			WHERE parent_id = $1::uuid
			  AND media_type = $2
			  AND ordinal = $3
			FOR UPDATE
		`, destinationID, sourceChild.mediaType, sourceChild.ordinal).Scan(&destinationChildID)
		if errors.Is(err, pgx.ErrNoRows) {
			if err := invalidateTitleSubtree(ctx, tx, sourceChild.id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE titles
				SET parent_id = $1::uuid, updated_at = now()
				WHERE id = $2::uuid
			`, destinationID, sourceChild.id); err != nil {
				return fmt.Errorf("move unique title child: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("query canonical title child: %w", err)
		}
		if err := mergeTitleTree(ctx, tx, destinationChildID, sourceChild.id, sourceChild.mediaType); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE titles AS destination
		SET display_title = COALESCE(destination.display_title, source.display_title),
		    poster_url = COALESCE(destination.poster_url, source.poster_url),
		    background_url = COALESCE(destination.background_url, source.background_url),
		    release_info = COALESCE(destination.release_info, source.release_info),
		    resource_id = COALESCE(destination.resource_id, source.resource_id),
		    resource_provider = COALESCE(destination.resource_provider, source.resource_provider),
		    release_date = COALESCE(destination.release_date, source.release_date),
		    created_at = LEAST(destination.created_at, source.created_at),
		    updated_at = GREATEST(destination.updated_at, source.updated_at, now())
		FROM titles AS source
		WHERE destination.id = $1::uuid
		  AND source.id = $2::uuid
	`, destinationID, sourceID); err != nil {
		return fmt.Errorf("merge title snapshots: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE title_external_ids
		SET title_id = $1::uuid
		WHERE title_id = $2::uuid
	`, destinationID, sourceID); err != nil {
		return fmt.Errorf("move title identities: %w", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM titles WHERE id = $1::uuid", sourceID); err != nil {
		return fmt.Errorf("remove duplicate title: %w", err)
	}
	return nil
}

func mergeProfileState(ctx context.Context, tx pgx.Tx, destinationID, sourceID string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile_library AS destination (profile_id, title_id, added_at, updated_at)
		SELECT profile_id, $1::uuid, added_at, updated_at
		FROM profile_library
		WHERE title_id = $2::uuid
		ON CONFLICT (profile_id, title_id) DO UPDATE
		SET added_at = LEAST(destination.added_at, EXCLUDED.added_at),
		    updated_at = GREATEST(destination.updated_at, EXCLUDED.updated_at)
	`, destinationID, sourceID); err != nil {
		return fmt.Errorf("merge profile library state: %w", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM profile_library WHERE title_id = $1::uuid", sourceID); err != nil {
		return fmt.Errorf("remove duplicate profile library state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile_progress AS destination (
			profile_id, title_id, position_seconds, duration_seconds, completed,
			version, last_watched_at, updated_at
		)
		SELECT profile_id, $1::uuid, position_seconds, duration_seconds, completed,
		       version, last_watched_at, updated_at
		FROM profile_progress
		WHERE title_id = $2::uuid
		ON CONFLICT (profile_id, title_id) DO UPDATE
		SET position_seconds = CASE
				WHEN (EXCLUDED.last_watched_at, EXCLUDED.updated_at) >
				     (destination.last_watched_at, destination.updated_at)
				THEN EXCLUDED.position_seconds ELSE destination.position_seconds END,
		    duration_seconds = CASE
				WHEN (EXCLUDED.last_watched_at, EXCLUDED.updated_at) >
				     (destination.last_watched_at, destination.updated_at)
				THEN EXCLUDED.duration_seconds ELSE destination.duration_seconds END,
		    completed = CASE
				WHEN (EXCLUDED.last_watched_at, EXCLUDED.updated_at) >
				     (destination.last_watched_at, destination.updated_at)
				THEN EXCLUDED.completed ELSE destination.completed END,
		    version = GREATEST(destination.version, EXCLUDED.version) + 1,
		    last_watched_at = GREATEST(destination.last_watched_at, EXCLUDED.last_watched_at),
		    updated_at = GREATEST(destination.updated_at, EXCLUDED.updated_at)
	`, destinationID, sourceID); err != nil {
		return fmt.Errorf("merge profile progress state: %w", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM profile_progress WHERE title_id = $1::uuid", sourceID); err != nil {
		return fmt.Errorf("remove duplicate profile progress state: %w", err)
	}
	return nil
}

func invalidateTitleSubtree(ctx context.Context, tx pgx.Tx, titleID string) error {
	if _, err := tx.Exec(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id FROM titles WHERE id = $1::uuid
			UNION ALL
			SELECT child.id
			FROM titles AS child
			JOIN subtree AS parent ON parent.id = child.parent_id
		)
		DELETE FROM title_metadata AS metadata
		USING subtree
		WHERE metadata.title_id = subtree.id
	`, titleID); err != nil {
		return fmt.Errorf("invalidate moved title metadata: %w", err)
	}
	return nil
}

func replaceTVDBEpisodeID(ctx context.Context, tx pgx.Tx, titleID, externalID string) error {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return fmt.Errorf("%w: TVDB returned an episode without an identifier", ErrProviderFailure)
	}
	titleLockKey := "title:" + titleID + ":tvdb:" + MediaTypeEpisode
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", titleLockKey); err != nil {
		return fmt.Errorf("lock canonical TVDB episode identity: %w", err)
	}

	var existingExternalID string
	err := tx.QueryRow(ctx, `
		SELECT external_id
		FROM title_external_ids
		WHERE title_id = $1::uuid AND provider = 'tvdb' AND namespace = 'episode'
	`, titleID).Scan(&existingExternalID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("query canonical TVDB episode identity: %w", err)
	}
	if existingExternalID == externalID {
		return nil
	}

	lockKeys := []string{"tvdb:" + MediaTypeEpisode + ":" + externalID}
	if existingExternalID != "" {
		lockKeys = append(lockKeys, "tvdb:"+MediaTypeEpisode+":"+existingExternalID)
	}
	sort.Strings(lockKeys)
	for _, lockKey := range lockKeys {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
			return fmt.Errorf("lock TVDB episode identity: %w", err)
		}
	}

	var mappedTitleID string
	err = tx.QueryRow(ctx, `
		SELECT title_id::text
		FROM title_external_ids
		WHERE provider = 'tvdb' AND namespace = 'episode' AND external_id = $1
	`, externalID).Scan(&mappedTitleID)
	if err == nil && mappedTitleID != titleID {
		return errors.New("metadata provider returned a conflicting external ID")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("query TVDB episode identity: %w", err)
	}
	if mappedTitleID == titleID {
		return nil
	}

	if existingExternalID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE title_external_ids
			SET external_id = $2
			WHERE title_id = $1::uuid AND provider = 'tvdb' AND namespace = 'episode'
		`, titleID, externalID); err != nil {
			return fmt.Errorf("replace TVDB episode identity: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO title_external_ids (title_id, provider, namespace, external_id)
		VALUES ($1::uuid, 'tvdb', 'episode', $2)
	`, titleID, externalID); err != nil {
		return fmt.Errorf("link TVDB episode identity: %w", err)
	}
	return nil
}

func linkAdditionalIDs(ctx context.Context, tx pgx.Tx, titleID, namespace string, additionalIDs map[string]string) error {
	identities, err := normalizeProviderIdentities(additionalIDs)
	if err != nil {
		return err
	}

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

func (s *Service) logEnrichmentFailure(provider, mediaType, titleID string, err error) {
	if s.logger != nil {
		s.logger.Warn("optional metadata enrichment failed", "provider", provider, "mediaType", mediaType, "titleId", titleID, "error", err)
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
	cast := provided.Cast
	if cast == nil {
		cast = []CastMember{}
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
		LogoURL:          provided.LogoURL,
		Tagline:          provided.Tagline,
		RuntimeMinutes:   provided.RuntimeMinutes,
		Genres:           genres,
		Cast:             cast,
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
	cast := provided.Cast
	if cast == nil {
		cast = []CastMember{}
	}
	aliases := provided.Aliases
	if aliases == nil {
		aliases = []Alias{}
	}
	episodeOrders := normalizeEpisodeOrders(provided.EpisodeOrders)
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
		LogoURL:          provided.LogoURL,
		Tagline:          provided.Tagline,
		Status:           provided.Status,
		NumberOfSeasons:  provided.NumberOfSeasons,
		NumberOfEpisodes: provided.NumberOfEpisodes,
		Genres:           genres,
		Cast:             cast,
		VoteAverage:      provided.VoteAverage,
		VoteCount:        provided.VoteCount,
		Seasons:          []SeasonSummary{},
		Aliases:          aliases,
		EpisodeOrders:    episodeOrders,
		MappingProvider:  providerName,
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
		BackdropURL:  provided.BackdropURL,
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
		BackdropURL:  provided.BackdropURL,
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
		BackdropURL:    provided.BackdropURL,
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

func (s *Service) beginAuthorizedProfileTx(ctx context.Context, principal auth.Principal) (pgx.Tx, error) {
	if err := requireActiveProfile(principal); err != nil {
		return nil, err
	}
	if s.pool == nil {
		return nil, ErrProfileRequired
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin active metadata profile authorization: %w", err)
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{*principal.ActiveProfileID}, false)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("authorize active metadata profile: %w", err)
	}
	if !authorized {
		_ = tx.Rollback(ctx)
		return nil, ErrProfileRequired
	}
	return tx, nil
}

func firstPrincipal(principals []*auth.Principal) *auth.Principal {
	if len(principals) == 0 {
		return nil
	}
	return principals[0]
}

func (s *Service) beginMetadataWorkTx(ctx context.Context, principal *auth.Principal) (pgx.Tx, error) {
	if principal != nil {
		return s.beginAuthorizedProfileTx(ctx, *principal)
	}
	if s.pool == nil {
		return nil, errors.New("metadata database unavailable")
	}
	return s.pool.Begin(ctx)
}

func (s *Service) requireActiveProfile(ctx context.Context, principal auth.Principal) error {
	tx, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit active metadata profile authorization: %w", err)
	}
	return nil
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

func normalizeSeriesMappingProvider(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return providerName, nil
	}
	if provider != providerName && provider != "tvdb" {
		return "", fmt.Errorf("%w: mappingProvider must be tmdb or tvdb", ErrInvalidInput)
	}
	return provider, nil
}

func normalizeEpisodeOrderID(orderID string) (string, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID != "" && !episodeOrderPattern.MatchString(orderID) {
		return "", fmt.Errorf("%w: episodeOrder must be a positive TVDB season type ID", ErrInvalidInput)
	}
	return orderID, nil
}

func normalizeEpisodeOrders(orders []EpisodeOrder) []EpisodeOrder {
	result := make([]EpisodeOrder, 0, len(orders))
	seen := make(map[string]struct{}, len(orders))
	for _, order := range orders {
		order.ID = strings.TrimSpace(order.ID)
		if !episodeOrderPattern.MatchString(order.ID) {
			continue
		}
		if _, exists := seen[order.ID]; exists {
			continue
		}
		seen[order.ID] = struct{}{}
		order.Type = strings.ToLower(strings.TrimSpace(order.Type))
		order.Name = strings.TrimSpace(order.Name)
		switch order.Type {
		case "official":
			order.Name = "Aired Order"
		case "dvd":
			order.Name = "DVD Order"
		case "absolute":
			order.Name = "Absolute Order"
		}
		if order.Name == "" {
			continue
		}
		result = append(result, order)
	}
	sort.SliceStable(result, func(left, right int) bool {
		if len(result[left].ID) != len(result[right].ID) {
			return len(result[left].ID) < len(result[right].ID)
		}
		return result[left].ID < result[right].ID
	})
	return result
}

func defaultEpisodeOrderID(orders []EpisodeOrder) string {
	for _, order := range orders {
		if order.IsDefault {
			return order.ID
		}
	}
	return ""
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
