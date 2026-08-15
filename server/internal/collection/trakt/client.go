package trakt

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
	defaultBaseURL   = "https://api.trakt.tv"
	maximumBodyBytes = 4 * 1024 * 1024
)

type Client struct {
	baseURL    string
	clientID   string
	httpClient *http.Client
}

type listItem struct {
	Rank     int        `json:"rank"`
	ListedAt string     `json:"listed_at"`
	Type     string     `json:"type"`
	Movie    *mediaItem `json:"movie"`
	Show     *mediaItem `json:"show"`
}

type mediaItem struct {
	Title      string  `json:"title"`
	Year       int     `json:"year"`
	Overview   string  `json:"overview"`
	Released   string  `json:"released"`
	FirstAired string  `json:"first_aired"`
	Rating     float64 `json:"rating"`
	Votes      int     `json:"votes"`
	IDs        struct {
		Trakt int64  `json:"trakt"`
		Slug  string `json:"slug"`
		IMDB  string `json:"imdb"`
		TMDB  int64  `json:"tmdb"`
		TVDB  int64  `json:"tvdb"`
	} `json:"ids"`
}

func New(clientID string, httpClient *http.Client) *Client {
	return newWithBaseURL(clientID, defaultBaseURL, httpClient)
}

func newWithBaseURL(clientID, baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), clientID: strings.TrimSpace(clientID), httpClient: httpClient}
}

func (client *Client) ResolveCollectionSource(ctx context.Context, source collection.TraktSource, page int) (collection.SourcePage, error) {
	mediaPath := "movies"
	if source.MediaType == collection.MediaTypeSeries {
		mediaPath = "shows"
	}
	requestURL, err := url.Parse(client.baseURL + "/lists/" + strconv.FormatInt(source.ListID, 10) + "/items/" + mediaPath)
	if err != nil {
		return collection.SourcePage{}, fmt.Errorf("construct Trakt list URL: %w", err)
	}
	query := requestURL.Query()
	query.Set("extended", "full")
	query.Set("page", strconv.Itoa(page))
	query.Set("limit", "100")
	query.Set("sort_by", source.SortBy)
	query.Set("sort_how", source.SortHow)
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return collection.SourcePage{}, fmt.Errorf("construct Trakt list request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("trakt-api-key", client.clientID)
	request.Header.Set("trakt-api-version", "2")
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
		return collection.SourcePage{}, fmt.Errorf("request Trakt list: %w", err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return collection.SourcePage{}, fmt.Errorf("%w: Trakt rejected the configured client ID", collection.ErrProviderUnavailable)
	case http.StatusNotFound:
		return collection.SourcePage{}, collection.ErrNotFound
	case http.StatusTooManyRequests:
		return collection.SourcePage{}, fmt.Errorf("%w: Trakt rate limit reached", collection.ErrProviderUnavailable)
	default:
		return collection.SourcePage{}, fmt.Errorf("%w: Trakt returned HTTP %d", collection.ErrProviderUnavailable, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(addon.BudgetedPayloadReader(ctx, response.Body), maximumBodyBytes+1))
	if err != nil {
		return collection.SourcePage{}, fmt.Errorf("read Trakt list: %w", err)
	}
	if len(body) > maximumBodyBytes {
		return collection.SourcePage{}, fmt.Errorf("%w: Trakt response exceeds 4 MiB", collection.ErrProviderUnavailable)
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(body, &rawItems); err != nil {
		return collection.SourcePage{}, fmt.Errorf("%w: decode Trakt list: %v", collection.ErrProviderUnavailable, err)
	}
	if err := addon.ConsumePayloadItems(ctx, len(rawItems)); err != nil {
		return collection.SourcePage{}, err
	}
	items := make([]collection.Item, 0, len(rawItems))
	for _, raw := range rawItems {
		var value listItem
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		media := value.Movie
		mediaType := collection.MediaTypeMovie
		if source.MediaType == collection.MediaTypeSeries {
			media = value.Show
			mediaType = collection.MediaTypeSeries
		}
		if media == nil || media.Title == "" {
			continue
		}
		externalIDs := make(map[string]string)
		if media.IDs.Trakt > 0 {
			externalIDs["trakt"] = strconv.FormatInt(media.IDs.Trakt, 10)
		}
		if media.IDs.TMDB > 0 {
			externalIDs["tmdb"] = strconv.FormatInt(media.IDs.TMDB, 10)
		}
		if media.IDs.TVDB > 0 {
			externalIDs["tvdb"] = strconv.FormatInt(media.IDs.TVDB, 10)
		}
		if media.IDs.IMDB != "" {
			externalIDs["imdb"] = media.IDs.IMDB
		}
		id := "trakt:" + strconv.FormatInt(media.IDs.Trakt, 10)
		if media.IDs.TMDB > 0 {
			id = "tmdb:" + strconv.FormatInt(media.IDs.TMDB, 10)
		} else if media.IDs.IMDB != "" {
			id = media.IDs.IMDB
		}
		rating := media.Rating
		votes := media.Votes
		released := media.Released
		if released == "" {
			released = media.FirstAired
		}
		items = append(items, collection.Item{
			ID: id, MediaType: mediaType, Title: media.Title, Description: media.Overview,
			ReleaseInfo: releaseInfo(media.Year, released), Released: released,
			VoteAverage: &rating, VoteCount: &votes, ExternalIDs: externalIDs,
			Raw: raw,
		})
	}
	pageCount, _ := strconv.Atoi(response.Header.Get("X-Pagination-Page-Count"))
	return collection.SourcePage{Items: items, Page: page, HasMore: pageCount > page || pageCount == 0 && len(rawItems) >= 100}, nil
}

func releaseInfo(year int, released string) string {
	if year > 0 {
		return strconv.Itoa(year)
	}
	if len(released) >= 4 {
		return released[:4]
	}
	return ""
}

var _ collection.TraktProvider = (*Client)(nil)
