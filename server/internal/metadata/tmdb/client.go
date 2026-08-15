package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/requestwork"
)

const (
	defaultBaseURL    = "https://api.themoviedb.org/3"
	imageBaseURL      = "https://image.tmdb.org/t/p"
	posterImageSize   = "w780"
	backdropImageSize = "original"
	profileImageSize  = "w185"
	maxBodyBytes      = 2 * 1024 * 1024
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
	ID               int64           `json:"id"`
	Title            string          `json:"title"`
	OriginalTitle    string          `json:"original_title"`
	OriginalLanguage string          `json:"original_language"`
	Overview         string          `json:"overview"`
	ReleaseDate      string          `json:"release_date"`
	PosterPath       string          `json:"poster_path"`
	BackdropPath     string          `json:"backdrop_path"`
	Tagline          string          `json:"tagline"`
	Runtime          int             `json:"runtime"`
	Genres           []genre         `json:"genres"`
	VoteAverage      float64         `json:"vote_average"`
	VoteCount        int             `json:"vote_count"`
	IMDBID           string          `json:"imdb_id"`
	Credits          creditsResponse `json:"credits"`
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
	Credits          creditsResponse  `json:"credits"`
}

type creditsResponse struct {
	Cast []castMemberResponse `json:"cast"`
}

type castMemberResponse struct {
	ID          int64              `json:"id"`
	Name        string             `json:"name"`
	Character   string             `json:"character"`
	ProfilePath string             `json:"profile_path"`
	Roles       []castRoleResponse `json:"roles"`
}

type castRoleResponse struct {
	Character string `json:"character"`
}

type seasonResponse struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Overview     string            `json:"overview"`
	SeasonNumber int               `json:"season_number"`
	EpisodeCount int               `json:"episode_count"`
	AirDate      string            `json:"air_date"`
	PosterPath   string            `json:"poster_path"`
	BackdropPath string            `json:"backdrop_path"`
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
		httpClient = metadata.NewProviderHTTPClient(baseURL, 10*time.Second)
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
	if err := c.get(ctx, "/movie/"+strconv.FormatInt(movieID, 10), url.Values{"append_to_response": {"credits"}, "language": {language}}, &response); err != nil {
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

func (c *Client) Trailers(ctx context.Context, mediaType, externalID, language string, seasonNumber *int) ([]metadata.ProviderTrailer, error) {
	titleID, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
	if err != nil || titleID < 1 {
		return nil, fmt.Errorf("%w: invalid TMDB title ID", metadata.ErrProviderFailure)
	}
	var endpoint string
	switch mediaType {
	case metadata.MediaTypeMovie:
		if seasonNumber != nil {
			return nil, fmt.Errorf("%w: seasons are not valid for TMDB movies", metadata.ErrProviderFailure)
		}
		endpoint = "/movie/" + strconv.FormatInt(titleID, 10) + "/videos"
	case metadata.MediaTypeSeries:
		endpoint = "/tv/" + strconv.FormatInt(titleID, 10)
		if seasonNumber != nil {
			if *seasonNumber < 0 {
				return nil, fmt.Errorf("%w: invalid TMDB season number", metadata.ErrProviderFailure)
			}
			endpoint += "/season/" + strconv.Itoa(*seasonNumber)
		}
		endpoint += "/videos"
	default:
		return nil, fmt.Errorf("%w: unsupported TMDB trailer media type", metadata.ErrProviderFailure)
	}
	var response videosResponse
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
	endpoint := "/find/" + url.PathEscape(externalID)
	if err := c.get(ctx, endpoint, url.Values{"external_source": {externalSource}}, &response); err != nil {
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
	resource := endpoint + "?external_source=" + url.QueryEscape(externalSource)
	return "", metadata.NewProviderError(
		metadata.ErrProviderNotFound,
		fmt.Errorf("TMDB external-ID lookup returned no matching %s", mediaType),
		http.StatusOK,
		resource,
	)
}

func (c *Client) SeriesDetails(ctx context.Context, externalID, language string) (metadata.ProviderSeries, error) {
	seriesID, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
	if err != nil || seriesID < 1 {
		return metadata.ProviderSeries{}, fmt.Errorf("%w: invalid TMDB series ID", metadata.ErrProviderFailure)
	}
	var response seriesResponse
	query := url.Values{"append_to_response": {"external_ids,credits"}, "language": {language}}
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
	if response.SeasonNumber != seasonNumber {
		return metadata.ProviderSeason{}, fmt.Errorf("%w: TMDB returned season %d for requested season %d", metadata.ErrProviderFailure, response.SeasonNumber, seasonNumber)
	}
	for _, episode := range response.Episodes {
		if episode.SeasonNumber != seasonNumber {
			return metadata.ProviderSeason{}, fmt.Errorf("%w: TMDB returned an episode from season %d for requested season %d", metadata.ErrProviderFailure, episode.SeasonNumber, seasonNumber)
		}
	}
	return normalizeSeason(response), nil
}

func (c *Client) get(ctx context.Context, endpoint string, query url.Values, destination any) error {
	requestURL, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return metadata.NewProviderError(metadata.ErrProviderFailure, fmt.Errorf("construct TMDB URL: %w", err), 0, endpoint)
	}
	requestURL.RawQuery = query.Encode()
	resource := requestURL.RequestURI()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return metadata.NewProviderError(metadata.ErrProviderFailure, fmt.Errorf("construct TMDB request: %w", err), 0, resource)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.accessToken)
	requestwork.PropagateRequestID(request)
	requestwork.BeginOutbound(ctx, requestwork.Now())
	response, err := c.httpClient.Do(request)
	if err != nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
	} else if response.Body == nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
		response.Body = http.NoBody
	} else {
		response.Body = requestwork.ObserveBody(ctx, response.Body)
	}
	if err != nil {
		return metadata.NewProviderError(metadata.ErrProviderFailure, err, 0, resource)
	}
	defer response.Body.Close()
	source := addon.BudgetedPayloadReader(ctx, response.Body)

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return metadata.NewProviderError(metadata.ErrProviderUnauthorized, fmt.Errorf("TMDB returned HTTP %d", response.StatusCode), response.StatusCode, resource)
	case http.StatusNotFound:
		return metadata.NewProviderError(metadata.ErrProviderNotFound, fmt.Errorf("TMDB returned HTTP %d", response.StatusCode), response.StatusCode, resource)
	case http.StatusTooManyRequests:
		return metadata.NewProviderError(metadata.ErrProviderRateLimited, fmt.Errorf("TMDB returned HTTP %d", response.StatusCode), response.StatusCode, resource)
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(source, 4096))
		return metadata.NewProviderError(
			metadata.ErrProviderFailure,
			fmt.Errorf("TMDB returned HTTP %d", response.StatusCode),
			response.StatusCode,
			resource,
		)
	}

	decoder := json.NewDecoder(io.LimitReader(source, maxBodyBytes))
	if err := decoder.Decode(destination); err != nil {
		return metadata.NewProviderError(metadata.ErrProviderFailure, fmt.Errorf("decode TMDB response: %w", err), response.StatusCode, resource)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return metadata.NewProviderError(metadata.ErrProviderFailure, errors.New("decode TMDB response: trailing content"), response.StatusCode, resource)
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
		PosterURL:        imageURL(posterImageSize, movie.PosterPath),
		BackdropURL:      imageURL(backdropImageSize, movie.BackdropPath),
		Tagline:          movie.Tagline,
		RuntimeMinutes:   movie.Runtime,
		Genres:           genres,
		Cast:             normalizeCast(movie.Credits.Cast),
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
		PosterURL:        imageURL(posterImageSize, series.PosterPath),
		BackdropURL:      imageURL(backdropImageSize, series.BackdropPath),
		Tagline:          series.Tagline,
		Status:           series.Status,
		NumberOfSeasons:  series.NumberOfSeasons,
		NumberOfEpisodes: series.NumberOfEpisodes,
		Genres:           genres,
		Cast:             normalizeCast(series.Credits.Cast),
		VoteAverage:      series.VoteAverage,
		VoteCount:        series.VoteCount,
		Seasons:          seasons,
		AdditionalIDs:    additionalIDs,
	}
}

func normalizeCast(cast []castMemberResponse) []metadata.CastMember {
	const maximumCastMembers = 100
	members := make([]metadata.CastMember, 0, min(len(cast), maximumCastMembers))
	seen := make(map[int64]struct{}, min(len(cast), maximumCastMembers))
	for _, person := range cast {
		name := strings.TrimSpace(person.Name)
		if person.ID < 1 || name == "" {
			continue
		}
		if _, duplicate := seen[person.ID]; duplicate {
			continue
		}
		character := strings.TrimSpace(person.Character)
		if character == "" {
			for _, role := range person.Roles {
				if character = strings.TrimSpace(role.Character); character != "" {
					break
				}
			}
		}
		members = append(members, metadata.CastMember{
			ID:         strconv.FormatInt(person.ID, 10),
			Name:       name,
			Character:  character,
			ProfileURL: imageURL(profileImageSize, person.ProfilePath),
		})
		seen[person.ID] = struct{}{}
		if len(members) == maximumCastMembers {
			break
		}
	}
	return members
}

func normalizeSeasonSummary(season seasonResponse) metadata.ProviderSeasonSummary {
	return metadata.ProviderSeasonSummary{
		ExternalID:   strconv.FormatInt(season.ID, 10),
		Name:         season.Name,
		Overview:     season.Overview,
		SeasonNumber: season.SeasonNumber,
		EpisodeCount: season.EpisodeCount,
		AirDate:      season.AirDate,
		PosterURL:    imageURL(posterImageSize, season.PosterPath),
		BackdropURL:  imageURL(backdropImageSize, season.BackdropPath),
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
			StillURL:       imageURL(backdropImageSize, episode.StillPath),
			BackdropURL:    imageURL(backdropImageSize, episode.StillPath),
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
		PosterURL:    imageURL(posterImageSize, season.PosterPath),
		BackdropURL:  imageURL(backdropImageSize, season.BackdropPath),
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
