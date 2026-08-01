package tvdb

import (
	"bytes"
	"context"
	"encoding/json"
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
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	Image    string     `json:"image"`
	Number   int        `json:"number"`
	SeriesID int64      `json:"seriesId"`
	Type     seasonType `json:"type"`
}

type episodesEnvelope struct {
	Status string `json:"status"`
	Data   struct {
		Episodes []episodeRecord `json:"episodes"`
	} `json:"data"`
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
		name := strings.TrimSpace(value.AlternateName)
		if name == "" {
			name = strings.TrimSpace(value.Name)
		}
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
	episodes, err := c.episodes(ctx, parsedID, season.SeasonNumber)
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

func (c *Client) SeriesSeasons(ctx context.Context, seriesTVDBID string) ([]metadata.ProviderSeasonSummary, error) {
	parsedID, err := parsePositiveID(seriesTVDBID)
	if err != nil {
		return nil, err
	}
	series, err := c.seriesExtended(ctx, parsedID)
	if err != nil {
		return nil, err
	}
	seasons := officialSeasons(series)
	result := make([]metadata.ProviderSeasonSummary, 0, len(seasons))
	for _, season := range seasons {
		episodes, err := c.episodes(ctx, parsedID, season.Number)
		if err != nil {
			return nil, err
		}
		airDate := ""
		for _, episode := range episodes {
			if episode.SeasonNumber != season.Number || episode.Aired == "" {
				continue
			}
			if airDate == "" || episode.Aired < airDate {
				airDate = episode.Aired
			}
		}
		name := strings.TrimSpace(season.Name)
		if name == "" {
			if season.Number == 0 {
				name = "Specials"
			} else {
				name = "Season " + strconv.Itoa(season.Number)
			}
		}
		result = append(result, metadata.ProviderSeasonSummary{
			ExternalID:   strconv.FormatInt(season.ID, 10),
			Name:         name,
			SeasonNumber: season.Number,
			EpisodeCount: len(episodes),
			AirDate:      airDate,
			PosterURL:    httpsURL(season.Image),
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
	for _, season := range officialSeasons(series) {
		if season.ID == parsedSeasonID {
			selected = season
			break
		}
	}
	if selected.ID == 0 {
		return metadata.ProviderSeason{}, metadata.ErrProviderNotFound
	}
	records, err := c.episodes(ctx, parsedSeriesID, selected.Number)
	if err != nil {
		return metadata.ProviderSeason{}, err
	}
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
	name := strings.TrimSpace(selected.Name)
	if name == "" {
		if selected.Number == 0 {
			name = "Specials"
		} else {
			name = "Season " + strconv.Itoa(selected.Number)
		}
	}
	return metadata.ProviderSeason{
		ExternalID:   strconv.FormatInt(selected.ID, 10),
		Name:         name,
		SeasonNumber: selected.Number,
		AirDate:      airDate,
		PosterURL:    httpsURL(selected.Image),
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

func (c *Client) episodes(ctx context.Context, seriesID int64, seasonNumber int) ([]episodeRecord, error) {
	query := url.Values{
		"page":   {"0"},
		"season": {strconv.Itoa(seasonNumber)},
	}
	var response episodesEnvelope
	endpoint := "/series/" + strconv.FormatInt(seriesID, 10) + "/episodes/official"
	if err := c.get(ctx, endpoint, query, &response); err != nil {
		return nil, err
	}
	return response.Data.Episodes, nil
}

func officialSeasons(series seriesExtendedRecord) []seasonRecord {
	officialTypeID := int64(0)
	for _, value := range series.SeasonTypes {
		if strings.EqualFold(strings.TrimSpace(value.Type), "official") {
			officialTypeID = value.ID
			break
		}
	}
	seasons := make([]seasonRecord, 0, len(series.Seasons))
	for _, season := range series.Seasons {
		if season.ID < 1 || season.Number < 0 || season.SeriesID != series.ID {
			continue
		}
		if officialTypeID > 0 {
			if season.Type.ID != officialTypeID {
				continue
			}
		} else if !strings.EqualFold(strings.TrimSpace(season.Type.Type), "official") {
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
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", metadata.ErrProviderFailure, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return response.StatusCode, metadata.ErrProviderUnauthorized
	case http.StatusNotFound:
		return response.StatusCode, metadata.ErrProviderNotFound
	case http.StatusTooManyRequests:
		return response.StatusCode, metadata.ErrProviderRateLimited
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return response.StatusCode, fmt.Errorf("%w: TVDB returned HTTP %d", metadata.ErrProviderFailure, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxBodyBytes))
	if err := decoder.Decode(destination); err != nil {
		return response.StatusCode, fmt.Errorf("%w: decode TVDB response: %v", metadata.ErrProviderFailure, err)
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
