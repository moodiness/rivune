package tvdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/metadata"
)

const (
	defaultBaseURL = "https://api4.thetvdb.com/v4"
	maxBodyBytes   = 4 * 1024 * 1024
	tokenLifetime  = 29 * 24 * time.Hour
)

type Client struct {
	baseURL    string
	apiKey     string
	pin        string
	httpClient *http.Client

	tokenMu        sync.Mutex
	accessToken    string
	tokenExpiresAt time.Time
}

type loginResponse struct {
	Status string `json:"status"`
	Data   struct {
		Token string `json:"token"`
	} `json:"data"`
}

type seriesEnvelope struct {
	Status string               `json:"status"`
	Data   seriesExtendedRecord `json:"data"`
}

type seriesExtendedRecord struct {
	ID                int64          `json:"id"`
	Name              string         `json:"name"`
	Overview          string         `json:"overview"`
	Image             string         `json:"image"`
	Country           string         `json:"country"`
	DefaultSeasonType int64          `json:"defaultSeasonType"`
	Aliases           []alias        `json:"aliases"`
	SeasonTypes       []seasonType   `json:"seasonTypes"`
	Seasons           []seasonRecord `json:"seasons"`
}

type alias struct {
	Language string `json:"language"`
	Name     string `json:"name"`
}

type seasonType struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	AlternateName string `json:"alternateName"`
	Type          string `json:"type"`
}

type seasonRecord struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Image     string     `json:"image"`
	ImageType int64      `json:"imageType"`
	Number    int        `json:"number"`
	SeriesID  int64      `json:"seriesId"`
	Type      seasonType `json:"type"`
}

type episodesEnvelope struct {
	Status string `json:"status"`
	Data   struct {
		Episodes []episodeRecord `json:"episodes"`
	} `json:"data"`
}

type seasonExtendedEnvelope struct {
	Status string               `json:"status"`
	Data   seasonExtendedRecord `json:"data"`
}

type seasonExtendedRecord struct {
	seasonRecord
	Artwork  []artworkRecord `json:"artwork"`
	Episodes []episodeRecord `json:"episodes"`
}

type artworkRecord struct {
	ID     int64   `json:"id"`
	Image  string  `json:"image"`
	Type   int64   `json:"type"`
	Width  int64   `json:"width"`
	Height int64   `json:"height"`
	Score  float64 `json:"score"`
}

type episodeRecord struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	Aired         string `json:"aired"`
	Image         string `json:"image"`
	Runtime       *int   `json:"runtime"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"number"`
}

func New(apiKey, pin string, httpClient *http.Client) *Client {
	return newWithBaseURL(apiKey, pin, defaultBaseURL, httpClient)
}

func newWithBaseURL(apiKey, pin, baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     strings.TrimSpace(apiKey),
		pin:        strings.TrimSpace(pin),
		httpClient: httpClient,
	}
}

func (c *Client) EnrichSeries(ctx context.Context, series metadata.ProviderSeries) (metadata.ProviderSeries, error) {
	tvdbID := strings.TrimSpace(series.AdditionalIDs["tvdb"])
	if tvdbID == "" {
		return series, nil
	}
	parsedID, err := parsePositiveID(tvdbID)
	if err != nil {
		return series, err
	}
	response, err := c.seriesExtended(ctx, parsedID)
	if err != nil {
		return series, err
	}
	if response.ID != parsedID {
		return series, fmt.Errorf("%w: TVDB returned a mismatched series", metadata.ErrProviderFailure)
	}

	aliases := make([]metadata.Alias, 0, len(response.Aliases))
	seenAliases := make(map[string]struct{}, len(response.Aliases))
	for _, value := range response.Aliases {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(value.Language) + "\x00" + strings.ToLower(name)
		if _, exists := seenAliases[key]; exists {
			continue
		}
		seenAliases[key] = struct{}{}
		aliases = append(aliases, metadata.Alias{Language: strings.ToLower(strings.TrimSpace(value.Language)), Name: name})
	}
	orders := make([]metadata.EpisodeOrder, 0, len(response.SeasonTypes))
	for _, value := range response.SeasonTypes {
		if value.ID < 1 {
			continue
		}
		name := episodeOrderName(value)
		orders = append(orders, metadata.EpisodeOrder{
			ID:        strconv.FormatInt(value.ID, 10),
			Name:      name,
			Type:      strings.TrimSpace(value.Type),
			IsDefault: value.ID == response.DefaultSeasonType,
		})
	}
	series.Aliases = aliases
	series.EpisodeOrders = orders
	if series.Name == "" {
		series.Name = response.Name
	}
	if series.Overview == "" {
		series.Overview = response.Overview
	}
	if series.PosterURL == "" && isHTTPSURL(response.Image) {
		series.PosterURL = response.Image
	}
	return series, nil
}

func (c *Client) EnrichSeason(ctx context.Context, seriesTVDBID string, season metadata.ProviderSeason) (metadata.ProviderSeason, error) {
	parsedID, err := parsePositiveID(seriesTVDBID)
	if err != nil {
		return season, err
	}
	episodes, err := c.episodes(ctx, parsedID, "official", season.SeasonNumber)
	if err != nil {
		return season, err
	}

	byNumber := make(map[int]episodeRecord, len(episodes))
	for _, episode := range episodes {
		if episode.ID > 0 && episode.SeasonNumber == season.SeasonNumber && episode.EpisodeNumber >= 0 {
			byNumber[episode.EpisodeNumber] = episode
		}
	}
	for index := range season.Episodes {
		episode := &season.Episodes[index]
		value, exists := byNumber[episode.EpisodeNumber]
		if !exists {
			continue
		}
		if tmdbAirDate, tvdbAirDate := strings.TrimSpace(episode.AirDate), strings.TrimSpace(value.Aired); tmdbAirDate != "" && tvdbAirDate != "" && tmdbAirDate != tvdbAirDate {
			continue
		}
		if episode.AdditionalIDs == nil {
			episode.AdditionalIDs = make(map[string]string, 1)
		}
		episode.AdditionalIDs["tvdb"] = strconv.FormatInt(value.ID, 10)
		if episode.Name == "" {
			episode.Name = value.Name
		}
		if episode.Overview == "" {
			episode.Overview = value.Overview
		}
		if episode.AirDate == "" {
			episode.AirDate = value.Aired
		}
		if episode.StillURL == "" && isHTTPSURL(value.Image) {
			episode.StillURL = value.Image
		}
		if episode.RuntimeMinutes == 0 && value.Runtime != nil {
			episode.RuntimeMinutes = *value.Runtime
		}
	}
	return season, nil
}

func (c *Client) SeriesSeasons(ctx context.Context, seriesTVDBID, episodeOrderID string) ([]metadata.ProviderSeasonSummary, error) {
	parsedID, err := parsePositiveID(seriesTVDBID)
	if err != nil {
		return nil, err
	}
	series, err := c.seriesExtended(ctx, parsedID)
	if err != nil {
		return nil, err
	}
	selectedType, err := selectSeasonType(series, episodeOrderID)
	if err != nil {
		return nil, err
	}
	seasons := seasonsForType(series, selectedType)
	result := make([]metadata.ProviderSeasonSummary, 0, len(seasons))
	for _, season := range seasons {
		detailed, err := c.seasonDetails(ctx, season)
		if err != nil {
			return nil, err
		}
		airDate := ""
		for _, episode := range detailed.Episodes {
			if episode.SeasonNumber != season.Number || episode.Aired == "" {
				continue
			}
			if airDate == "" || episode.Aired < airDate {
				airDate = episode.Aired
			}
		}
		name := seasonName(season)
		result = append(result, metadata.ProviderSeasonSummary{
			ExternalID:   strconv.FormatInt(season.ID, 10),
			Name:         name,
			SeasonNumber: season.Number,
			EpisodeCount: len(detailed.Episodes),
			AirDate:      airDate,
			PosterURL:    seasonPosterURL(detailed),
		})
	}
	return result, nil
}

func (c *Client) SeriesSeason(ctx context.Context, seriesTVDBID, seasonTVDBID string) (metadata.ProviderSeason, error) {
	parsedSeriesID, err := parsePositiveID(seriesTVDBID)
	if err != nil {
		return metadata.ProviderSeason{}, err
	}
	parsedSeasonID, err := parsePositiveID(seasonTVDBID)
	if err != nil {
		return metadata.ProviderSeason{}, err
	}
	series, err := c.seriesExtended(ctx, parsedSeriesID)
	if err != nil {
		return metadata.ProviderSeason{}, err
	}
	var selected seasonRecord
	for _, season := range series.Seasons {
		if season.ID == parsedSeasonID && season.Number >= 0 && season.SeriesID == series.ID {
			selected = season
			break
		}
	}
	if selected.ID == 0 {
		return metadata.ProviderSeason{}, metadata.ErrProviderNotFound
	}
	detailed, err := c.seasonDetails(ctx, selected)
	if err != nil {
		return metadata.ProviderSeason{}, err
	}
	records := detailed.Episodes
	episodes := make([]metadata.ProviderEpisode, 0, len(records))
	airDate := ""
	for _, episode := range records {
		if episode.ID < 1 || episode.SeasonNumber != selected.Number || episode.EpisodeNumber < 0 {
			continue
		}
		if episode.Aired != "" && (airDate == "" || episode.Aired < airDate) {
			airDate = episode.Aired
		}
		runtime := 0
		if episode.Runtime != nil {
			runtime = *episode.Runtime
		}
		episodes = append(episodes, metadata.ProviderEpisode{
			ExternalID:     strconv.FormatInt(episode.ID, 10),
			Name:           strings.TrimSpace(episode.Name),
			Overview:       strings.TrimSpace(episode.Overview),
			SeasonNumber:   episode.SeasonNumber,
			EpisodeNumber:  episode.EpisodeNumber,
			AirDate:        strings.TrimSpace(episode.Aired),
			StillURL:       httpsURL(episode.Image),
			RuntimeMinutes: runtime,
		})
	}
	return metadata.ProviderSeason{
		ExternalID:   strconv.FormatInt(selected.ID, 10),
		Name:         seasonName(selected),
		SeasonNumber: selected.Number,
		AirDate:      airDate,
		PosterURL:    seasonPosterURL(detailed),
		Episodes:     episodes,
	}, nil
}

func (c *Client) seriesExtended(ctx context.Context, seriesID int64) (seriesExtendedRecord, error) {
	var response seriesEnvelope
	if err := c.get(ctx, "/series/"+strconv.FormatInt(seriesID, 10)+"/extended", nil, &response); err != nil {
		return seriesExtendedRecord{}, err
	}
	return response.Data, nil
}

func (c *Client) episodes(ctx context.Context, seriesID int64, seasonTypeName string, seasonNumber int) ([]episodeRecord, error) {
	query := url.Values{
		"page":   {"0"},
		"season": {strconv.Itoa(seasonNumber)},
	}
	var response episodesEnvelope
	endpoint := "/series/" + strconv.FormatInt(seriesID, 10) + "/episodes/" + url.PathEscape(strings.ToLower(strings.TrimSpace(seasonTypeName)))
	if err := c.get(ctx, endpoint, query, &response); err != nil {
		return nil, err
	}
	return response.Data.Episodes, nil
}

func (c *Client) seasonDetails(ctx context.Context, expected seasonRecord) (seasonExtendedRecord, error) {
	var response seasonExtendedEnvelope
	endpoint := "/seasons/" + strconv.FormatInt(expected.ID, 10) + "/extended"
	if err := c.get(ctx, endpoint, nil, &response); err != nil {
		return seasonExtendedRecord{}, err
	}
	provided := response.Data
	if provided.ID != expected.ID ||
		provided.SeriesID != expected.SeriesID ||
		provided.Number != expected.Number ||
		(expected.Type.ID > 0 && provided.Type.ID != expected.Type.ID) {
		return seasonExtendedRecord{}, fmt.Errorf("%w: TVDB returned a conflicting season hierarchy", metadata.ErrProviderFailure)
	}
	if strings.TrimSpace(provided.Image) == "" {
		provided.Image = expected.Image
	}
	if provided.ImageType == 0 {
		provided.ImageType = expected.ImageType
	}
	return provided, nil
}

func seasonPosterURL(season seasonExtendedRecord) string {
	var selected *artworkRecord
	baseURL := httpsURL(season.Image)
	if len(season.Artwork) == 0 {
		return baseURL
	}
	for index := range season.Artwork {
		candidate := &season.Artwork[index]
		candidateURL := httpsURL(candidate.Image)
		if candidateURL == "" || candidate.Width < 1 || candidate.Height < 1 ||
			float64(candidate.Height)/float64(candidate.Width) < 1.25 {
			continue
		}
		if baseURL != "" && candidateURL == baseURL {
			return candidateURL
		}
		if selected == nil || betterSeasonPoster(season.ImageType, candidate, selected) {
			selected = candidate
		}
	}
	if selected == nil {
		return ""
	}
	return httpsURL(selected.Image)
}

func betterSeasonPoster(imageType int64, candidate, selected *artworkRecord) bool {
	candidateMatchesType := imageType > 0 && candidate.Type == imageType
	selectedMatchesType := imageType > 0 && selected.Type == imageType
	if candidateMatchesType != selectedMatchesType {
		return candidateMatchesType
	}
	if candidate.Score != selected.Score {
		return candidate.Score > selected.Score
	}
	if candidate.Width != selected.Width {
		return candidate.Width > selected.Width
	}
	return candidate.Height > selected.Height
}

func selectSeasonType(series seriesExtendedRecord, episodeOrderID string) (seasonType, error) {
	selectedID := series.DefaultSeasonType
	if strings.TrimSpace(episodeOrderID) != "" {
		parsedID, err := parsePositiveID(episodeOrderID)
		if err != nil {
			return seasonType{}, err
		}
		selectedID = parsedID
	}
	for _, value := range series.SeasonTypes {
		if value.ID == selectedID && strings.TrimSpace(value.Type) != "" {
			return value, nil
		}
	}
	if strings.TrimSpace(episodeOrderID) == "" {
		for _, value := range series.SeasonTypes {
			if strings.EqualFold(strings.TrimSpace(value.Type), "official") {
				return value, nil
			}
		}
	}
	return seasonType{}, metadata.ErrProviderNotFound
}

func seasonsForType(series seriesExtendedRecord, selectedType seasonType) []seasonRecord {
	seasons := make([]seasonRecord, 0, len(series.Seasons))
	for _, season := range series.Seasons {
		if season.ID < 1 || season.Number < 0 || season.SeriesID != series.ID {
			continue
		}
		if selectedType.ID > 0 {
			if season.Type.ID != selectedType.ID {
				continue
			}
		} else if !strings.EqualFold(strings.TrimSpace(season.Type.Type), strings.TrimSpace(selectedType.Type)) {
			continue
		}
		seasons = append(seasons, season)
	}
	sort.Slice(seasons, func(left, right int) bool {
		if seasons[left].Number != seasons[right].Number {
			return seasons[left].Number < seasons[right].Number
		}
		return seasons[left].ID < seasons[right].ID
	})
	return seasons
}

func seasonName(season seasonRecord) string {
	name := strings.TrimSpace(season.Name)
	if name != "" {
		return name
	}
	if season.Number == 0 {
		return "Specials"
	}
	return "Season " + strconv.Itoa(season.Number)
}

func episodeOrderName(value seasonType) string {
	orderType := strings.ToLower(strings.TrimSpace(value.Type))
	name := strings.TrimSpace(value.Name)
	switch orderType {
	case "official":
		return "Aired Order"
	case "dvd":
		return "DVD Order"
	case "absolute":
		return "Absolute Order"
	}
	if alternateName := strings.TrimSpace(value.AlternateName); alternateName != "" {
		return alternateName
	}
	return name
}

func httpsURL(value string) string {
	value = strings.TrimSpace(value)
	if isHTTPSURL(value) {
		return value
	}
	return ""
}

func (c *Client) get(ctx context.Context, endpoint string, query url.Values, destination any) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.token(ctx, attempt == 1)
		if err != nil {
			return err
		}
		status, err := c.getWithToken(ctx, endpoint, query, token, destination)
		if err == nil {
			return nil
		}
		if status != http.StatusUnauthorized || attempt == 1 {
			return err
		}
	}
	return metadata.ErrProviderUnauthorized
}

func (c *Client) getWithToken(ctx context.Context, endpoint string, query url.Values, token string, destination any) (int, error) {
	requestURL, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return 0, fmt.Errorf("%w: construct TVDB URL: %v", metadata.ErrProviderFailure, err)
	}
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("%w: construct TVDB request: %v", metadata.ErrProviderFailure, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	return c.do(request, destination)
}

func (c *Client) token(ctx context.Context, force bool) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if !force && c.accessToken != "" && time.Now().UTC().Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}

	requestBody := map[string]string{"apikey": c.apiKey}
	if c.pin != "" {
		requestBody["pin"] = c.pin
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("%w: encode TVDB login: %v", metadata.ErrProviderFailure, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/login", bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: construct TVDB login: %v", metadata.ErrProviderFailure, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	var response loginResponse
	status, err := c.do(request, &response)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK || strings.TrimSpace(response.Data.Token) == "" {
		return "", metadata.ErrProviderUnauthorized
	}
	c.accessToken = strings.TrimSpace(response.Data.Token)
	c.tokenExpiresAt = time.Now().UTC().Add(tokenLifetime)
	return c.accessToken, nil
}

func (c *Client) do(request *http.Request, destination any) (int, error) {
	resource := request.URL.RequestURI()
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, metadata.NewProviderError(metadata.ErrProviderFailure, err, 0, resource)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return response.StatusCode, metadata.NewProviderError(metadata.ErrProviderUnauthorized, fmt.Errorf("TVDB returned HTTP %d", response.StatusCode), response.StatusCode, resource)
	case http.StatusNotFound:
		return response.StatusCode, metadata.NewProviderError(metadata.ErrProviderNotFound, fmt.Errorf("TVDB returned HTTP %d", response.StatusCode), response.StatusCode, resource)
	case http.StatusTooManyRequests:
		return response.StatusCode, metadata.NewProviderError(metadata.ErrProviderRateLimited, fmt.Errorf("TVDB returned HTTP %d", response.StatusCode), response.StatusCode, resource)
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return response.StatusCode, metadata.NewProviderError(
			metadata.ErrProviderFailure,
			fmt.Errorf("TVDB returned HTTP %d", response.StatusCode),
			response.StatusCode,
			resource,
		)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxBodyBytes))
	if err := decoder.Decode(destination); err != nil {
		return response.StatusCode, metadata.NewProviderError(
			metadata.ErrProviderFailure,
			fmt.Errorf("decode TVDB response: %w", err),
			response.StatusCode,
			resource,
		)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return response.StatusCode, metadata.NewProviderError(
			metadata.ErrProviderFailure,
			errors.New("decode TVDB response: trailing content"),
			response.StatusCode,
			resource,
		)
	}
	return response.StatusCode, nil
}

func parsePositiveID(value string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("%w: invalid TVDB identifier", metadata.ErrProviderFailure)
	}
	return parsed, nil
}

func isHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}
