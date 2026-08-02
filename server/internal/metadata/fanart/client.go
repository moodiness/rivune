package fanart

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/metadata"
)

const (
	defaultBaseURL = "https://webservice.fanart.tv/v3.2"
	maxBodyBytes   = 8 * 1024 * 1024
)

type Client struct {
	baseURL    string
	apiKey     string
	clientKey  string
	httpClient *http.Client
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
	if !isPositiveID(tmdbID) {
		return movie, fmt.Errorf("%w: invalid TMDB identifier for Fanart", metadata.ErrProviderFailure)
	}

	var response movieResponse
	if err := c.get(ctx, "/movies/"+tmdbID, &response); err != nil {
		return movie, err
	}
	if selected := bestImage(language, response.MoviePosters); selected != "" {
		movie.PosterURL = selected
	}
	if selected := bestImage(language, response.MovieBackgrounds); selected != "" {
		movie.BackdropURL = selected
	}
	if selected := bestLocalizedImage(language, response.HDMovieLogos, response.MovieLogos); selected != "" {
		movie.LogoURL = selected
	}
	return movie, nil
}

func (c *Client) EnrichSeries(ctx context.Context, series metadata.ProviderSeries, language string) (metadata.ProviderSeries, error) {
	tvdbID := strings.TrimSpace(series.AdditionalIDs["tvdb"])
	if tvdbID == "" {
		return series, nil
	}
	if !isPositiveID(tvdbID) {
		return series, fmt.Errorf("%w: invalid TVDB identifier for Fanart", metadata.ErrProviderFailure)
	}

	response, err := c.series(ctx, tvdbID)
	if err != nil {
		return series, err
	}
	if selected := bestImage(language, response.TVPosters); selected != "" {
		series.PosterURL = selected
	}
	if selected := bestImage(language, response.ShowBackgrounds); selected != "" {
		series.BackdropURL = selected
	}
	if selected := bestLocalizedImage(language, response.HDTVLogos, response.ClearLogos); selected != "" {
		series.LogoURL = selected
	}
	for index := range series.Seasons {
		if selected := bestSeasonImage(language, series.Seasons[index].SeasonNumber, response.SeasonPosters); selected != "" {
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
	response, err := c.series(ctx, tvdbID)
	if err != nil {
		return season, err
	}
	if selected := bestSeasonImage(language, season.SeasonNumber, response.SeasonPosters); selected != "" {
		season.PosterURL = selected
	}
	return season, nil
}

func (c *Client) series(ctx context.Context, tvdbID string) (seriesResponse, error) {
	var response seriesResponse
	if err := c.get(ctx, "/tv/"+tvdbID, &response); err != nil {
		return seriesResponse{}, err
	}
	return response, nil
}

func (c *Client) get(ctx context.Context, endpoint string, destination any) error {
	requestURL, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return fmt.Errorf("%w: construct Fanart URL: %v", metadata.ErrProviderFailure, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return fmt.Errorf("%w: construct Fanart request: %v", metadata.ErrProviderFailure, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("api-key", c.apiKey)
	if c.clientKey != "" {
		request.Header.Set("client-key", c.clientKey)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", metadata.ErrProviderFailure, err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return metadata.ErrProviderUnauthorized
	case http.StatusNotFound:
		return metadata.ErrProviderNotFound
	case http.StatusTooManyRequests:
		return metadata.ErrProviderRateLimited
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%w: Fanart returned HTTP %d", metadata.ErrProviderFailure, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxBodyBytes))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode Fanart response: %v", metadata.ErrProviderFailure, err)
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
	likes, _ := strconv.Atoi(strings.TrimSpace(artwork.Likes))
	width, _ := strconv.ParseInt(strings.TrimSpace(artwork.Width), 10, 32)
	height, _ := strconv.ParseInt(strings.TrimSpace(artwork.Height), 10, 32)
	return candidate{
		url:          candidateURL,
		languageRank: artworkLanguageRank(artwork.Lang, preferredLanguage),
		tier:         tier,
		likes:        likes,
		pixels:       width * height,
	}, true
}

func betterCandidate(left, right candidate) bool {
	if left.tier != right.tier {
		return left.tier > right.tier
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
	case language == "" || language == "00":
		return 3
	case language == "en":
		return 2
	default:
		return 1
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
