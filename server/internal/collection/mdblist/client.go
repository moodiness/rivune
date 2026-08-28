package mdblist

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

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/requestwork"
)

const (
	defaultBaseURL   = "https://api.mdblist.com"
	maximumBodyBytes = 4 * 1024 * 1024
	pageSize         = 100
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type listResponse struct {
	Movies     []json.RawMessage `json:"movies"`
	Shows      []json.RawMessage `json:"shows"`
	Pagination struct {
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	} `json:"pagination"`
}

type mediaItem struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	IMDBID      string `json:"imdb_id"`
	TVDBID      int64  `json:"tvdb_id"`
	MediaType   string `json:"mediatype"`
	ReleaseYear int    `json:"release_year"`
	Year        int    `json:"year"`
	Released    string `json:"released"`
	Poster      string `json:"poster"`
	Description string `json:"description"`
	IDs         struct {
		MDBList string `json:"mdblist"`
		IMDB    string `json:"imdb"`
		TMDB    int64  `json:"tmdb"`
		TVDB    int64  `json:"tvdb"`
	} `json:"ids"`
}

func New(apiKey string, httpClient *http.Client) *Client {
	return newWithBaseURL(apiKey, defaultBaseURL, httpClient)
}

func newWithBaseURL(apiKey, baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: strings.TrimSpace(apiKey), httpClient: requestwork.BoundedHTTPClient(httpClient)}
}

func (client *Client) ResolveCollectionSource(ctx context.Context, source collection.MDBListSource, page int) (collection.SourcePage, error) {
	requestURL, err := url.Parse(client.baseURL + "/lists/" + strconv.FormatInt(source.ListID, 10) + "/items")
	if err != nil {
		return collection.SourcePage{}, fmt.Errorf("construct MDBList list URL: %w", err)
	}
	mediaType := "movie"
	if source.MediaType == collection.MediaTypeSeries {
		mediaType = "show"
	}
	query := requestURL.Query()
	query.Set("apikey", client.apiKey)
	query.Set("append_to_response", "poster,description")
	query.Set("limit", strconv.Itoa(pageSize))
	query.Set("offset", strconv.Itoa((page-1)*pageSize))
	query.Set("mediatype", mediaType)
	query.Set("sort", source.Sort)
	query.Set("order", source.Order)
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return collection.SourcePage{}, fmt.Errorf("construct MDBList list request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	requestwork.PropagateRequestID(request)
	requestwork.BeginOutbound(ctx, requestwork.Now())
	response, err := client.httpClient.Do(request)
	if err != nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
	} else if response.Body == nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
		response.Body = http.NoBody
	} else {
		response.Body = requestwork.ObserveBody(ctx, response.Body)
	}
	if err != nil {
		if ctx.Err() != nil {
			return collection.SourcePage{}, ctx.Err()
		}
		return collection.SourcePage{}, fmt.Errorf("%w: MDBList request failed", collection.ErrProviderUnavailable)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return collection.SourcePage{}, fmt.Errorf("%w: MDBList rejected the configured API key", collection.ErrProviderUnavailable)
	case http.StatusNotFound:
		return collection.SourcePage{}, collection.ErrNotFound
	case http.StatusTooManyRequests:
		return collection.SourcePage{}, fmt.Errorf("%w: MDBList rate limit reached", collection.ErrProviderUnavailable)
	default:
		return collection.SourcePage{}, fmt.Errorf("%w: MDBList returned HTTP %d", collection.ErrProviderUnavailable, response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(addon.BudgetedPayloadReader(ctx, response.Body), maximumBodyBytes+1))
	if err != nil {
		return collection.SourcePage{}, fmt.Errorf("read MDBList list: %w", err)
	}
	if len(body) > maximumBodyBytes {
		return collection.SourcePage{}, fmt.Errorf("%w: MDBList response exceeds 4 MiB", collection.ErrProviderUnavailable)
	}
	var payload listResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return collection.SourcePage{}, fmt.Errorf("%w: decode MDBList list: %v", collection.ErrProviderUnavailable, err)
	}
	rawItems := payload.Movies
	if source.MediaType == collection.MediaTypeSeries {
		rawItems = payload.Shows
	}
	if err := addon.ConsumePayloadItems(ctx, len(rawItems)); err != nil {
		return collection.SourcePage{}, err
	}
	items := make([]collection.Item, 0, len(rawItems))
	for _, raw := range rawItems {
		item, ok := normalizeItem(raw, source.MediaType)
		if ok {
			items = append(items, item)
		}
	}
	hasMore := responseHasMore(response.Header, payload, len(rawItems))
	return collection.SourcePage{Items: items, Page: page, HasMore: hasMore}, nil
}

func normalizeItem(raw json.RawMessage, mediaType string) (collection.Item, bool) {
	var value mediaItem
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value.Title) == "" {
		return collection.Item{}, false
	}
	externalIDs := make(map[string]string, 4)
	if value.IDs.MDBList != "" {
		externalIDs["mdblist"] = value.IDs.MDBList
	}
	imdbID := strings.TrimSpace(value.IDs.IMDB)
	if imdbID == "" {
		imdbID = strings.TrimSpace(value.IMDBID)
	}
	if imdbID != "" {
		externalIDs["imdb"] = imdbID
	}
	tmdbID := value.IDs.TMDB
	if tmdbID < 1 {
		tmdbID = value.ID
	}
	if tmdbID > 0 {
		externalIDs["tmdb"] = strconv.FormatInt(tmdbID, 10)
	}
	tvdbID := value.IDs.TVDB
	if tvdbID < 1 {
		tvdbID = value.TVDBID
	}
	if tvdbID > 0 {
		externalIDs["tvdb"] = strconv.FormatInt(tvdbID, 10)
	}
	id := ""
	if tmdbID > 0 {
		id = "tmdb:" + strconv.FormatInt(tmdbID, 10)
	} else if imdbID != "" {
		id = imdbID
	} else if value.IDs.MDBList != "" {
		id = "mdblist:" + value.IDs.MDBList
	} else {
		return collection.Item{}, false
	}
	year := value.ReleaseYear
	if year < 1 {
		year = value.Year
	}
	return collection.Item{
		ID: id, MediaType: mediaType, Title: strings.TrimSpace(value.Title),
		PosterURL: posterURL(value.Poster), Description: strings.TrimSpace(value.Description),
		ReleaseInfo: releaseInfo(year, value.Released), Released: strings.TrimSpace(value.Released),
		ExternalIDs: externalIDs, Raw: raw,
	}, true
}

func responseHasMore(header http.Header, payload listResponse, itemCount int) bool {
	if raw := strings.TrimSpace(header.Get("X-Has-More")); raw != "" {
		value, err := strconv.ParseBool(raw)
		return err == nil && value
	}
	return payload.Pagination.HasMore || payload.Pagination.NextCursor != "" || itemCount >= pageSize
}

func posterURL(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/") {
		return "https://image.tmdb.org/t/p/w500" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return value
}

func releaseInfo(year int, released string) string {
	if year > 0 {
		return strconv.Itoa(year)
	}
	if released = strings.TrimSpace(released); len(released) >= 4 {
		return released[:4]
	}
	return ""
}

var _ collection.MDBListProvider = (*Client)(nil)
