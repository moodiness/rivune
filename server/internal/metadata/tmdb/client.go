package tmdb

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
	defaultBaseURL = "https://api.themoviedb.org/3"
	imageBaseURL   = "https://image.tmdb.org/t/p"
	maxBodyBytes   = 2 * 1024 * 1024
)

type Client struct {
	baseURL     string
	accessToken string
	httpClient  *http.Client
}

type listResponse struct {
	Page         int             `json:"page"`
	Results      []movieResponse `json:"results"`
	TotalPages   int             `json:"total_pages"`
	TotalResults int             `json:"total_results"`
}

type movieResponse struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"original_title"`
	OriginalLanguage string  `json:"original_language"`
	Overview         string  `json:"overview"`
	ReleaseDate      string  `json:"release_date"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	Tagline          string  `json:"tagline"`
	Runtime          int     `json:"runtime"`
	Genres           []genre `json:"genres"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
	IMDBID           string  `json:"imdb_id"`
}

type seriesListResponse struct {
	Page         int              `json:"page"`
	Results      []seriesResponse `json:"results"`
	TotalPages   int              `json:"total_pages"`
	TotalResults int              `json:"total_results"`
}

type videosResponse struct {
	Results []videoResponse `json:"results"`
}

type videoResponse struct {
	Language    string `json:"iso_639_1"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Site        string `json:"site"`
	Type        string `json:"type"`
	Official    bool   `json:"official"`
	PublishedAt string `json:"published_at"`
}

type findResponse struct {
	MovieResults []movieResponse  `json:"movie_results"`
	TVResults    []seriesResponse `json:"tv_results"`
}

type seriesResponse struct {
	ID               int64            `json:"id"`
	Name             string           `json:"name"`
	OriginalName     string           `json:"original_name"`
	OriginalLanguage string           `json:"original_language"`
	Overview         string           `json:"overview"`
	FirstAirDate     string           `json:"first_air_date"`
	LastAirDate      string           `json:"last_air_date"`
	PosterPath       string           `json:"poster_path"`
	BackdropPath     string           `json:"backdrop_path"`
	Tagline          string           `json:"tagline"`
	Status           string           `json:"status"`
	NumberOfSeasons  int              `json:"number_of_seasons"`
	NumberOfEpisodes int              `json:"number_of_episodes"`
	Genres           []genre          `json:"genres"`
	VoteAverage      float64          `json:"vote_average"`
	VoteCount        int              `json:"vote_count"`
	Seasons          []seasonResponse `json:"seasons"`
	ExternalIDs      externalIDs      `json:"external_ids"`
}

type seasonResponse struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Overview     string            `json:"overview"`
	SeasonNumber int               `json:"season_number"`
	EpisodeCount int               `json:"episode_count"`
	AirDate      string            `json:"air_date"`
	PosterPath   string            `json:"poster_path"`
	VoteAverage  float64           `json:"vote_average"`
	Episodes     []episodeResponse `json:"episodes"`
}

type episodeResponse struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	SeasonNumber  int     `json:"season_number"`
	EpisodeNumber int     `json:"episode_number"`
	AirDate       string  `json:"air_date"`
	StillPath     string  `json:"still_path"`
	Runtime       int     `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
}

type externalIDs struct {
	IMDBID     string `json:"imdb_id"`
	TVDBID     int    `json:"tvdb_id"`
	WikidataID string `json:"wikidata_id"`
}

type genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func New(accessToken string, httpClient *http.Client) *Client {
	return newWithBaseURL(accessToken, defaultBaseURL, httpClient)
}

func newWithBaseURL(accessToken, baseURL string, httpClient *http.Client) *Client {
	accessToken = strings.TrimSpace(accessToken)
	if len(accessToken) > 7 && strings.EqualFold(accessToken[:7], "Bearer ") {
		accessToken = strings.TrimSpace(accessToken[7:])
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		accessToken: accessToken,
		httpClient:  httpClient,
	}
}

func (c *Client) DiscoverMovies(ctx context.Context, options metadata.QueryOptions) (metadata.ProviderMoviePage, error) {
	query := url.Values{
		"include_adult": {"false"},
		"include_video": {"false"},
		"language":      {options.Language},
		"page":          {strconv.Itoa(options.Page)},
		"sort_by":       {"popularity.desc"},
	}
	if options.Region != "" {
		query.Set("region", options.Region)
	}
	var response listResponse
	if err := c.get(ctx, "/discover/movie", query, &response); err != nil {
		return metadata.ProviderMoviePage{}, err
	}
	return normalizePage(response), nil
}

func (c *Client) SearchMovies(ctx context.Context, options metadata.SearchOptions) (metadata.ProviderMoviePage, error) {
	query := url.Values{
		"include_adult": {"false"},
		"language":      {options.Language},
		"page":          {strconv.Itoa(options.Page)},
		"query":         {options.Query},
	}
	if options.Region != "" {
		query.Set("region", options.Region)
	}
	var response listResponse
	if err := c.get(ctx, "/search/movie", query, &response); err != nil {
		return metadata.ProviderMoviePage{}, err
	}
	return normalizePage(response), nil
}

func (c *Client) MovieDetails(ctx context.Context, externalID, language string) (metadata.ProviderMovie, error) {
	movieID, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
	if err != nil || movieID < 1 {
		return metadata.ProviderMovie{}, fmt.Errorf("%w: invalid TMDB movie ID", metadata.ErrProviderFailure)
	}
	var response movieResponse
	if err := c.get(ctx, "/movie/"+strconv.FormatInt(movieID, 10), url.Values{"language": {language}}, &response); err != nil {
		return metadata.ProviderMovie{}, err
	}
	return normalizeMovie(response), nil
}

func (c *Client) DiscoverSeries(ctx context.Context, options metadata.QueryOptions) (metadata.ProviderSeriesPage, error) {
	query := url.Values{
		"include_adult":                {"false"},
		"include_null_first_air_dates": {"false"},
		"language":                     {options.Language},
		"page":                         {strconv.Itoa(options.Page)},
		"sort_by":                      {"popularity.desc"},
	}
	var response seriesListResponse
	if err := c.get(ctx, "/discover/tv", query, &response); err != nil {
		return metadata.ProviderSeriesPage{}, err
	}
	return normalizeSeriesPage(response), nil
}

func (c *Client) SearchSeries(ctx context.Context, options metadata.SearchOptions) (metadata.ProviderSeriesPage, error) {
	query := url.Values{
		"include_adult": {"false"},
		"language":      {options.Language},
		"page":          {strconv.Itoa(options.Page)},
		"query":         {options.Query},
	}
	var response seriesListResponse
	if err := c.get(ctx, "/search/tv", query, &response); err != nil {
		return metadata.ProviderSeriesPage{}, err
	}
	return normalizeSeriesPage(response), nil
}

func (c *Client) Trailers(ctx context.Context, mediaType, externalID, language string) ([]metadata.ProviderTrailer, error) {
	titleID, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
	if err != nil || titleID < 1 {
		return nil, fmt.Errorf("%w: invalid TMDB title ID", metadata.ErrProviderFailure)
	}
	var titlePath string
	switch mediaType {
	case metadata.MediaTypeMovie:
		titlePath = "/movie/"
	case metadata.MediaTypeSeries:
		titlePath = "/tv/"
	default:
		return nil, fmt.Errorf("%w: unsupported TMDB trailer media type", metadata.ErrProviderFailure)
	}
	var response videosResponse
	endpoint := titlePath + strconv.FormatInt(titleID, 10) + "/videos"
	if err := c.get(ctx, endpoint, url.Values{"language": {language}}, &response); err != nil {
		return nil, err
	}
	trailers := make([]metadata.ProviderTrailer, 0, len(response.Results))
	for _, video := range response.Results {
		publishedAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(video.PublishedAt))
		trailers = append(trailers, metadata.ProviderTrailer{
			YouTubeID: strings.TrimSpace(video.Key), Name: strings.TrimSpace(video.Name),
			Language: strings.TrimSpace(video.Language), Site: strings.TrimSpace(video.Site),
			Type: strings.TrimSpace(video.Type), Official: video.Official, PublishedAt: publishedAt,
		})
	}
	return trailers, nil
}

func (c *Client) ResolveExternalID(ctx context.Context, mediaType, provider, externalID string) (string, error) {
	externalSource := ""
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "imdb":
		externalSource = "imdb_id"
	case "tvdb":
		externalSource = "tvdb_id"
	default:
		return "", metadata.ErrProviderNotFound
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return "", metadata.ErrProviderNotFound
	}
	var response findResponse
	if err := c.get(ctx, "/find/"+url.PathEscape(externalID), url.Values{"external_source": {externalSource}}, &response); err != nil {
		return "", err
	}
	switch mediaType {
	case metadata.MediaTypeMovie:
		if len(response.MovieResults) > 0 && response.MovieResults[0].ID > 0 {
			return strconv.FormatInt(response.MovieResults[0].ID, 10), nil
		}
	case metadata.MediaTypeSeries:
		if len(response.TVResults) > 0 && response.TVResults[0].ID > 0 {
			return strconv.FormatInt(response.TVResults[0].ID, 10), nil
		}
	}
	return "", metadata.ErrProviderNotFound
}

func (c *Client) SeriesDetails(ctx context.Context, externalID, language string) (metadata.ProviderSeries, error) {
	seriesID, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
	if err != nil || seriesID < 1 {
		return metadata.ProviderSeries{}, fmt.Errorf("%w: invalid TMDB series ID", metadata.ErrProviderFailure)
	}
	var response seriesResponse
	query := url.Values{"append_to_response": {"external_ids"}, "language": {language}}
	if err := c.get(ctx, "/tv/"+strconv.FormatInt(seriesID, 10), query, &response); err != nil {
		return metadata.ProviderSeries{}, err
	}
	return normalizeSeries(response), nil
}

func (c *Client) SeasonDetails(ctx context.Context, seriesExternalID string, seasonNumber int, language string) (metadata.ProviderSeason, error) {
	seriesID, err := strconv.ParseInt(strings.TrimSpace(seriesExternalID), 10, 64)
	if err != nil || seriesID < 1 || seasonNumber < 0 {
		return metadata.ProviderSeason{}, fmt.Errorf("%w: invalid TMDB season reference", metadata.ErrProviderFailure)
	}
	var response seasonResponse
	endpoint := "/tv/" + strconv.FormatInt(seriesID, 10) + "/season/" + strconv.Itoa(seasonNumber)
	if err := c.get(ctx, endpoint, url.Values{"language": {language}}, &response); err != nil {
		return metadata.ProviderSeason{}, err
	}
	return normalizeSeason(response), nil
}

func (c *Client) get(ctx context.Context, endpoint string, query url.Values, destination any) error {
	requestURL, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return fmt.Errorf("%w: construct TMDB URL: %v", metadata.ErrProviderFailure, err)
	}
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return fmt.Errorf("%w: construct TMDB request: %v", metadata.ErrProviderFailure, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.accessToken)
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
		return fmt.Errorf("%w: TMDB returned HTTP %d", metadata.ErrProviderFailure, response.StatusCode)
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, maxBodyBytes))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode TMDB response: %v", metadata.ErrProviderFailure, err)
	}
	return nil
}

func normalizePage(response listResponse) metadata.ProviderMoviePage {
	items := make([]metadata.ProviderMovie, 0, len(response.Results))
	for _, movie := range response.Results {
		if movie.ID < 1 {
			continue
		}
		items = append(items, normalizeMovie(movie))
	}
	return metadata.ProviderMoviePage{
		Items:        items,
		Page:         response.Page,
		TotalPages:   response.TotalPages,
		TotalResults: response.TotalResults,
	}
}

func normalizeMovie(movie movieResponse) metadata.ProviderMovie {
	genres := make([]metadata.Genre, 0, len(movie.Genres))
	for _, value := range movie.Genres {
		genres = append(genres, metadata.Genre{ID: value.ID, Name: value.Name})
	}
	additionalIDs := make(map[string]string, 1)
	if movie.IMDBID != "" {
		additionalIDs["imdb"] = movie.IMDBID
	}
	return metadata.ProviderMovie{
		ExternalID:       strconv.FormatInt(movie.ID, 10),
		Title:            movie.Title,
		OriginalTitle:    movie.OriginalTitle,
		OriginalLanguage: movie.OriginalLanguage,
		Overview:         movie.Overview,
		ReleaseDate:      movie.ReleaseDate,
		PosterURL:        imageURL("w500", movie.PosterPath),
		BackdropURL:      imageURL("w1280", movie.BackdropPath),
		Tagline:          movie.Tagline,
		RuntimeMinutes:   movie.Runtime,
		Genres:           genres,
		VoteAverage:      movie.VoteAverage,
		VoteCount:        movie.VoteCount,
		AdditionalIDs:    additionalIDs,
	}
}

func normalizeSeriesPage(response seriesListResponse) metadata.ProviderSeriesPage {
	items := make([]metadata.ProviderSeries, 0, len(response.Results))
	for _, series := range response.Results {
		if series.ID < 1 {
			continue
		}
		items = append(items, normalizeSeries(series))
	}
	return metadata.ProviderSeriesPage{
		Items:        items,
		Page:         response.Page,
		TotalPages:   response.TotalPages,
		TotalResults: response.TotalResults,
	}
}

func normalizeSeries(series seriesResponse) metadata.ProviderSeries {
	genres := make([]metadata.Genre, 0, len(series.Genres))
	for _, value := range series.Genres {
		genres = append(genres, metadata.Genre{ID: value.ID, Name: value.Name})
	}
	seasons := make([]metadata.ProviderSeasonSummary, 0, len(series.Seasons))
	for _, season := range series.Seasons {
		if season.ID < 1 || season.SeasonNumber < 0 {
			continue
		}
		seasons = append(seasons, normalizeSeasonSummary(season))
	}
	additionalIDs := make(map[string]string, 3)
	if series.ExternalIDs.IMDBID != "" {
		additionalIDs["imdb"] = series.ExternalIDs.IMDBID
	}
	if series.ExternalIDs.TVDBID > 0 {
		additionalIDs["tvdb"] = strconv.Itoa(series.ExternalIDs.TVDBID)
	}
	if series.ExternalIDs.WikidataID != "" {
		additionalIDs["wikidata"] = series.ExternalIDs.WikidataID
	}
	return metadata.ProviderSeries{
		ExternalID:       strconv.FormatInt(series.ID, 10),
		Name:             series.Name,
		OriginalName:     series.OriginalName,
		OriginalLanguage: series.OriginalLanguage,
		Overview:         series.Overview,
		FirstAirDate:     series.FirstAirDate,
		LastAirDate:      series.LastAirDate,
		PosterURL:        imageURL("w500", series.PosterPath),
		BackdropURL:      imageURL("w1280", series.BackdropPath),
		Tagline:          series.Tagline,
		Status:           series.Status,
		NumberOfSeasons:  series.NumberOfSeasons,
		NumberOfEpisodes: series.NumberOfEpisodes,
		Genres:           genres,
		VoteAverage:      series.VoteAverage,
		VoteCount:        series.VoteCount,
		Seasons:          seasons,
		AdditionalIDs:    additionalIDs,
	}
}

func normalizeSeasonSummary(season seasonResponse) metadata.ProviderSeasonSummary {
	return metadata.ProviderSeasonSummary{
		ExternalID:   strconv.FormatInt(season.ID, 10),
		Name:         season.Name,
		Overview:     season.Overview,
		SeasonNumber: season.SeasonNumber,
		EpisodeCount: season.EpisodeCount,
		AirDate:      season.AirDate,
		PosterURL:    imageURL("w500", season.PosterPath),
		VoteAverage:  season.VoteAverage,
	}
}

func normalizeSeason(season seasonResponse) metadata.ProviderSeason {
	episodes := make([]metadata.ProviderEpisode, 0, len(season.Episodes))
	for _, episode := range season.Episodes {
		if episode.ID < 1 || episode.EpisodeNumber < 0 {
			continue
		}
		episodes = append(episodes, metadata.ProviderEpisode{
			ExternalID:     strconv.FormatInt(episode.ID, 10),
			Name:           episode.Name,
			Overview:       episode.Overview,
			SeasonNumber:   episode.SeasonNumber,
			EpisodeNumber:  episode.EpisodeNumber,
			AirDate:        episode.AirDate,
			StillURL:       imageURL("w780", episode.StillPath),
			RuntimeMinutes: episode.Runtime,
			VoteAverage:    episode.VoteAverage,
			VoteCount:      episode.VoteCount,
		})
	}
	return metadata.ProviderSeason{
		ExternalID:   strconv.FormatInt(season.ID, 10),
		Name:         season.Name,
		Overview:     season.Overview,
		SeasonNumber: season.SeasonNumber,
		AirDate:      season.AirDate,
		PosterURL:    imageURL("w500", season.PosterPath),
		VoteAverage:  season.VoteAverage,
		Episodes:     episodes,
	}
}

func imageURL(size, imagePath string) string {
	if !strings.HasPrefix(imagePath, "/") {
		return ""
	}
	return imageBaseURL + "/" + size + imagePath
}
