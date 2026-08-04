package fanart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moodiness/rivune/server/internal/metadata"
	"golang.org/x/sync/singleflight"
)

const (
	defaultBaseURL           = "https://webservice.fanart.tv/v3.2"
	maxBodyBytes             = 8 * 1024 * 1024
	maxMemoryArtworkEntries  = 20_000
	movieArtworkResourceType = "movie"
	tvArtworkResourceType    = "tv"
)

type artworkCacheKey struct {
	resourceType string
	externalID   string
	language     string
}

type cachedArtwork struct {
	snapshot  artworkSnapshot
	available bool
	expiresAt time.Time
}

type Client struct {
	baseURL          string
	apiKey           string
	clientKey        string
	httpClient       *http.Client
	responseCache    artworkResponseCache
	responseCacheTTL time.Duration
	logger           *slog.Logger
	cacheMu          sync.Mutex
	cachedArtwork    map[artworkCacheKey]cachedArtwork
	responseFlights  singleflight.Group
}

type image struct {
	URL    string `json:"url"`
	Lang   string `json:"lang"`
	Likes  string `json:"likes"`
	Width  string `json:"width"`
	Height string `json:"height"`
	Season string `json:"season"`
}

type movieResponse struct {
	MoviePosters     []image `json:"movieposter"`
	MovieBackgrounds []image `json:"moviebackground"`
	HDMovieLogos     []image `json:"hdmovielogo"`
	MovieLogos       []image `json:"movielogo"`
}

type seriesResponse struct {
	TVPosters       []image `json:"tvposter"`
	ShowBackgrounds []image `json:"showbackground"`
	HDTVLogos       []image `json:"hdtvlogo"`
	ClearLogos      []image `json:"clearlogo"`
	SeasonPosters   []image `json:"seasonposter"`
}

type candidate struct {
	url          string
	languageRank int
	tier         int
	likes        int
	pixels       int64
}

func New(apiKey, clientKey string, httpClient *http.Client) *Client {
	return newWithBaseURL(apiKey, clientKey, defaultBaseURL, httpClient)
}

func NewCached(apiKey, clientKey string, httpClient *http.Client, pool *pgxpool.Pool, cacheTTL time.Duration, logger *slog.Logger) *Client {
	client := New(apiKey, clientKey, httpClient)
	if pool != nil && cacheTTL > 0 {
		client.enableResponseCache(&postgresArtworkResponseCache{pool: pool}, cacheTTL, logger)
	}
	return client
}

func newWithBaseURL(apiKey, clientKey, baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     strings.TrimSpace(apiKey),
		clientKey:  strings.TrimSpace(clientKey),
		httpClient: httpClient,
	}
}

func (c *Client) EnrichMovie(ctx context.Context, movie metadata.ProviderMovie, language string) (metadata.ProviderMovie, error) {
	tmdbID := strings.TrimSpace(movie.AdditionalIDs["tmdb"])
	if tmdbID == "" {
		tmdbID = strings.TrimSpace(movie.ExternalID)
	}
	if tmdbID == "" {
		return movie, nil
	}
	artwork, err := c.movieArtwork(ctx, tmdbID, language)
	if err != nil {
		return movie, err
	}
	if artwork.PosterURL != "" {
		movie.PosterURL = artwork.PosterURL
	}
	if artwork.BackdropURL != "" {
		movie.BackdropURL = artwork.BackdropURL
	}
	if artwork.LogoURL != "" {
		movie.LogoURL = artwork.LogoURL
	}
	return movie, nil
}

func (c *Client) EnrichCollection(ctx context.Context, collection metadata.ProviderCollection, language string) (metadata.ProviderCollection, error) {
	tmdbID := strings.TrimSpace(collection.ExternalID)
	if tmdbID == "" {
		return collection, nil
	}
	artwork, err := c.movieArtwork(ctx, tmdbID, language)
	if err != nil {
		return collection, err
	}
	if artwork.PosterURL != "" {
		collection.PosterURL = artwork.PosterURL
	}
	if artwork.BackdropURL != "" {
		collection.BackdropURL = artwork.BackdropURL
	}
	if artwork.LogoURL != "" {
		collection.LogoURL = artwork.LogoURL
	}
	return collection, nil
}

func (c *Client) movieArtwork(ctx context.Context, tmdbID, language string) (metadata.ProviderCollection, error) {
	if !isPositiveID(tmdbID) {
		return metadata.ProviderCollection{}, fmt.Errorf("%w: invalid TMDB identifier for Fanart", metadata.ErrProviderFailure)
	}
	artwork, err := c.resolveArtwork(ctx, artworkCacheKey{
		resourceType: movieArtworkResourceType,
		externalID:   tmdbID,
		language:     normalizedArtworkLanguage(language),
	}, func() (artworkSnapshot, error) {
		var response movieResponse
		if err := c.get(ctx, "/movies/"+tmdbID, &response); err != nil {
			return artworkSnapshot{}, err
		}
		return artworkSnapshot{
			PosterURL:   bestImage(language, response.MoviePosters),
			BackdropURL: bestImage(language, response.MovieBackgrounds),
			LogoURL:     bestLocalizedImage(language, response.HDMovieLogos, response.MovieLogos),
		}, nil
	})
	if err != nil {
		return metadata.ProviderCollection{}, err
	}
	return metadata.ProviderCollection{
		ExternalID:  tmdbID,
		PosterURL:   artwork.PosterURL,
		BackdropURL: artwork.BackdropURL,
		LogoURL:     artwork.LogoURL,
	}, nil
}

func (c *Client) EnrichSeries(ctx context.Context, series metadata.ProviderSeries, language string) (metadata.ProviderSeries, error) {
	tvdbID := strings.TrimSpace(series.AdditionalIDs["tvdb"])
	if tvdbID == "" {
		return series, nil
	}
	if !isPositiveID(tvdbID) {
		return series, fmt.Errorf("%w: invalid TVDB identifier for Fanart", metadata.ErrProviderFailure)
	}

	artwork, err := c.seriesArtwork(ctx, tvdbID, language)
	if err != nil {
		return series, err
	}
	if artwork.PosterURL != "" {
		series.PosterURL = artwork.PosterURL
	}
	if artwork.BackdropURL != "" {
		series.BackdropURL = artwork.BackdropURL
	}
	if artwork.LogoURL != "" {
		series.LogoURL = artwork.LogoURL
	}
	for index := range series.Seasons {
		if selected := artwork.SeasonPosters[series.Seasons[index].SeasonNumber]; selected != "" {
			series.Seasons[index].PosterURL = selected
		}
	}
	return series, nil
}

func (c *Client) EnrichSeason(ctx context.Context, tvdbID string, season metadata.ProviderSeason, language string) (metadata.ProviderSeason, error) {
	tvdbID = strings.TrimSpace(tvdbID)
	if tvdbID == "" {
		return season, nil
	}
	if !isPositiveID(tvdbID) {
		return season, fmt.Errorf("%w: invalid TVDB identifier for Fanart", metadata.ErrProviderFailure)
	}
	artwork, err := c.seriesArtwork(ctx, tvdbID, language)
	if err != nil {
		return season, err
	}
	if selected := artwork.SeasonPosters[season.SeasonNumber]; selected != "" {
		season.PosterURL = selected
	}
	return season, nil
}

func (c *Client) seriesArtwork(ctx context.Context, tvdbID, language string) (artworkSnapshot, error) {
	return c.resolveArtwork(ctx, artworkCacheKey{
		resourceType: tvArtworkResourceType,
		externalID:   tvdbID,
		language:     normalizedArtworkLanguage(language),
	}, func() (artworkSnapshot, error) {
		var response seriesResponse
		if err := c.get(ctx, "/tv/"+tvdbID, &response); err != nil {
			return artworkSnapshot{}, err
		}
		seasonNumbers := make(map[int]struct{})
		for _, image := range response.SeasonPosters {
			seasonNumber, err := strconv.Atoi(strings.TrimSpace(image.Season))
			if err == nil && seasonNumber >= 0 {
				seasonNumbers[seasonNumber] = struct{}{}
			}
		}
		seasonPosters := make(map[int]string, len(seasonNumbers))
		for seasonNumber := range seasonNumbers {
			if selected := bestSeasonImage(language, seasonNumber, response.SeasonPosters); selected != "" {
				seasonPosters[seasonNumber] = selected
			}
		}
		return artworkSnapshot{
			PosterURL:     bestImage(language, response.TVPosters),
			BackdropURL:   bestImage(language, response.ShowBackgrounds),
			LogoURL:       bestLocalizedImage(language, response.HDTVLogos, response.ClearLogos),
			SeasonPosters: seasonPosters,
		}, nil
	})
}

func (c *Client) enableResponseCache(cache artworkResponseCache, cacheTTL time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	c.responseCache = cache
	c.responseCacheTTL = cacheTTL
	c.logger = logger
	c.cachedArtwork = make(map[artworkCacheKey]cachedArtwork)
}

func (c *Client) resolveArtwork(
	ctx context.Context,
	key artworkCacheKey,
	load func() (artworkSnapshot, error),
) (artworkSnapshot, error) {
	if c.responseCache == nil || c.responseCacheTTL <= 0 {
		return load()
	}
	if cached, ok := c.loadRememberedArtwork(key); ok {
		return cached.snapshot, cachedArtworkError(cached.available)
	}

	value, err, _ := c.responseFlights.Do(key.resourceType+":"+key.externalID+":"+key.language, func() (any, error) {
		if cached, ok := c.loadRememberedArtwork(key); ok {
			return cached.snapshot, cachedArtworkError(cached.available)
		}
		snapshot, available, expiresAt, ok, cacheErr := c.responseCache.load(ctx, key)
		if cacheErr != nil {
			return artworkSnapshot{}, fmt.Errorf("%w: load Fanart response cache: %v", metadata.ErrProviderFailure, cacheErr)
		}
		if ok {
			c.rememberArtwork(key, cachedArtwork{snapshot: snapshot, available: available, expiresAt: expiresAt})
			return snapshot, cachedArtworkError(available)
		}

		snapshot, loadErr := load()
		if loadErr != nil && !errors.Is(loadErr, metadata.ErrProviderNotFound) {
			return artworkSnapshot{}, loadErr
		}
		available = loadErr == nil
		expiresAt = time.Now().Add(c.responseCacheTTL)
		c.rememberArtwork(key, cachedArtwork{snapshot: snapshot, available: available, expiresAt: expiresAt})
		if cacheErr := c.responseCache.store(ctx, key, snapshot, available, expiresAt); cacheErr != nil {
			c.logger.Warn("failed to store Fanart response cache",
				"resourceType", key.resourceType,
				"externalID", key.externalID,
				"language", key.language,
				"error", cacheErr,
			)
		}
		return snapshot, loadErr
	})
	snapshot, ok := value.(artworkSnapshot)
	if !ok {
		return artworkSnapshot{}, fmt.Errorf("%w: invalid shared Fanart response", metadata.ErrProviderFailure)
	}
	return snapshot, err
}

func (c *Client) loadRememberedArtwork(key artworkCacheKey) (cachedArtwork, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	cached, ok := c.cachedArtwork[key]
	if !ok {
		return cachedArtwork{}, false
	}
	if !cached.expiresAt.After(time.Now()) {
		delete(c.cachedArtwork, key)
		return cachedArtwork{}, false
	}
	return cached, true
}

func (c *Client) rememberArtwork(key artworkCacheKey, cached cachedArtwork) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if _, exists := c.cachedArtwork[key]; !exists && len(c.cachedArtwork) >= maxMemoryArtworkEntries {
		now := time.Now()
		for cachedKey, cachedValue := range c.cachedArtwork {
			if !cachedValue.expiresAt.After(now) {
				delete(c.cachedArtwork, cachedKey)
			}
		}
		if len(c.cachedArtwork) >= maxMemoryArtworkEntries {
			for cachedKey := range c.cachedArtwork {
				delete(c.cachedArtwork, cachedKey)
				break
			}
		}
	}
	c.cachedArtwork[key] = cached
}

func cachedArtworkError(available bool) error {
	if !available {
		return metadata.ErrProviderNotFound
	}
	return nil
}

func normalizedArtworkLanguage(language string) string {
	language = primaryLanguage(language)
	if language == "" {
		return "00"
	}
	for _, character := range language {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return "00"
		}
	}
	return language
}

func (c *Client) get(ctx context.Context, endpoint string, destination any) error {
	requestURL, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return metadata.NewProviderError(metadata.ErrProviderFailure, fmt.Errorf("construct Fanart URL: %w", err), 0, endpoint)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return metadata.NewProviderError(metadata.ErrProviderFailure, fmt.Errorf("construct Fanart request: %w", err), 0, endpoint)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("api-key", c.apiKey)
	if c.clientKey != "" {
		request.Header.Set("client-key", c.clientKey)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return metadata.NewProviderError(metadata.ErrProviderFailure, err, 0, endpoint)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return metadata.NewProviderError(metadata.ErrProviderUnauthorized, fmt.Errorf("Fanart returned HTTP %d", response.StatusCode), response.StatusCode, endpoint)
	case http.StatusNotFound:
		return metadata.NewProviderError(metadata.ErrProviderNotFound, fmt.Errorf("Fanart returned HTTP %d", response.StatusCode), response.StatusCode, endpoint)
	case http.StatusTooManyRequests:
		return metadata.NewProviderError(metadata.ErrProviderRateLimited, fmt.Errorf("Fanart returned HTTP %d", response.StatusCode), response.StatusCode, endpoint)
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return metadata.NewProviderError(
			metadata.ErrProviderFailure,
			fmt.Errorf("Fanart returned HTTP %d", response.StatusCode),
			response.StatusCode,
			endpoint,
		)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxBodyBytes))
	if err := decoder.Decode(destination); err != nil {
		return metadata.NewProviderError(metadata.ErrProviderFailure, fmt.Errorf("decode Fanart response: %w", err), response.StatusCode, endpoint)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return metadata.NewProviderError(metadata.ErrProviderFailure, errors.New("decode Fanart response: trailing content"), response.StatusCode, endpoint)
	}
	return nil
}

func bestImage(language string, groups ...[]image) string {
	return selectBestImage(language, betterCandidate, groups...)
}

func bestLocalizedImage(language string, groups ...[]image) string {
	return selectBestImage(language, betterLocalizedCandidate, groups...)
}

func selectBestImage(language string, better func(candidate, candidate) bool, groups ...[]image) string {
	preferredLanguage := primaryLanguage(language)
	best := candidate{}
	found := false
	for groupIndex, images := range groups {
		tier := len(groups) - groupIndex
		for _, artwork := range images {
			current, usable := imageCandidate(artwork, preferredLanguage, tier)
			if usable && (!found || better(current, best)) {
				best = current
				found = true
			}
		}
	}
	return best.url
}

func bestSeasonImage(language string, seasonNumber int, images []image) string {
	preferredLanguage := primaryLanguage(language)
	wanted := strconv.Itoa(seasonNumber)
	best := candidate{}
	found := false
	for _, artwork := range images {
		if strings.TrimSpace(artwork.Season) != wanted {
			continue
		}
		current, usable := imageCandidate(artwork, preferredLanguage, 1)
		if usable && (!found || betterCandidate(current, best)) {
			best = current
			found = true
		}
	}
	return best.url
}

func imageCandidate(artwork image, preferredLanguage string, tier int) (candidate, bool) {
	candidateURL := httpsURL(artwork.URL)
	if candidateURL == "" {
		return candidate{}, false
	}
	languageRank := artworkLanguageRank(artwork.Lang, preferredLanguage)
	if languageRank == 0 {
		return candidate{}, false
	}
	likes, _ := strconv.Atoi(strings.TrimSpace(artwork.Likes))
	width, _ := strconv.ParseInt(strings.TrimSpace(artwork.Width), 10, 32)
	height, _ := strconv.ParseInt(strings.TrimSpace(artwork.Height), 10, 32)
	return candidate{
		url:          candidateURL,
		languageRank: languageRank,
		tier:         tier,
		likes:        likes,
		pixels:       width * height,
	}, true
}

func betterCandidate(left, right candidate) bool {
	if left.tier != right.tier {
		return left.tier > right.tier
	}
	leftHasTextLanguage := left.languageRank >= 3
	rightHasTextLanguage := right.languageRank >= 3
	if leftHasTextLanguage != rightHasTextLanguage {
		return leftHasTextLanguage
	}
	if left.likes != right.likes {
		return left.likes > right.likes
	}
	if left.languageRank != right.languageRank {
		return left.languageRank > right.languageRank
	}
	return left.pixels > right.pixels
}

func betterLocalizedCandidate(left, right candidate) bool {
	if left.tier != right.tier {
		return left.tier > right.tier
	}
	if left.languageRank != right.languageRank {
		return left.languageRank > right.languageRank
	}
	if left.likes != right.likes {
		return left.likes > right.likes
	}
	return left.pixels > right.pixels
}

func artworkLanguageRank(language, preferred string) int {
	language = primaryLanguage(language)
	switch {
	case preferred != "" && language == preferred:
		return 4
	case language == "en":
		return 3
	case language == "" || language == "00":
		return 2
	default:
		return 0
	}
}

func primaryLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if separator := strings.IndexAny(language, "-_"); separator >= 0 {
		language = language[:separator]
	}
	return language
}

func httpsURL(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func isPositiveID(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0
}
