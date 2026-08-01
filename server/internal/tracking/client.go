package tracking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maximumProviderResponseBytes = 1 << 20

type providerConfig struct {
	clientID     string
	clientSecret string
	baseURL      string
	authURL      string
}

type providerClient struct {
	http  *http.Client
	trakt providerConfig
	simkl providerConfig
}

type deviceCode struct {
	Provider        string
	ProviderCode    string
	UserCode        string
	VerificationURL string
	ExpiresIn       int
	Interval        int
}

type providerToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
}

type mediaItem struct {
	MediaType string
	Title     string
	Year      int
	Season    int
	Episode   int
	IDs       map[string]any
	ShowTitle string
	ShowYear  int
	ShowIDs   map[string]any
}

type upstreamError struct {
	status     int
	retryAfter time.Duration
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("tracking provider returned HTTP %d", e.status)
}

func newProviderClient(traktID, traktSecret, simklID string, httpClient *http.Client) *providerClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &providerClient{
		http:  httpClient,
		trakt: providerConfig{clientID: traktID, clientSecret: traktSecret, baseURL: "https://api.trakt.tv", authURL: "https://auth.trakt.tv"},
		simkl: providerConfig{clientID: simklID, baseURL: "https://api.simkl.com"},
	}
}

func (c *providerClient) configured(provider string) bool {
	switch provider {
	case "trakt":
		return c.trakt.clientID != "" && c.trakt.clientSecret != ""
	case "simkl":
		return c.simkl.clientID != ""
	default:
		return false
	}
}

func validatedVerificationURL(provider, raw string) (string, error) {
	if len(raw) < 8 || len(raw) > 2048 {
		return "", fmt.Errorf("%w: invalid verification URL", ErrProviderUnavailable)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: invalid verification URL", ErrProviderUnavailable)
	}
	host := strings.ToLower(parsed.Hostname())
	root := "trakt.tv"
	if provider == "simkl" {
		root = "simkl.com"
	}
	if host != root && !strings.HasSuffix(host, "."+root) {
		return "", fmt.Errorf("%w: unexpected verification host", ErrProviderUnavailable)
	}
	return parsed.String(), nil
}

func (c *providerClient) beginDeviceAuthorization(ctx context.Context, provider string) (deviceCode, error) {
	if !c.configured(provider) {
		return deviceCode{}, ErrNotConfigured
	}
	switch provider {
	case "trakt":
		var response struct {
			DeviceCode      string `json:"device_code"`
			UserCode        string `json:"user_code"`
			VerificationURL string `json:"verification_url"`
			ExpiresIn       int    `json:"expires_in"`
			Interval        int    `json:"interval"`
		}
		if err := c.request(ctx, provider, http.MethodPost, c.trakt.authURL+"/oauth/device/code", "", map[string]string{"client_id": c.trakt.clientID}, &response); err != nil {
			return deviceCode{}, err
		}
		verificationURL, err := validatedVerificationURL(provider, response.VerificationURL)
		if err != nil {
			return deviceCode{}, err
		}
		return deviceCode{Provider: provider, ProviderCode: response.DeviceCode, UserCode: response.UserCode, VerificationURL: verificationURL, ExpiresIn: response.ExpiresIn, Interval: response.Interval}, nil
	case "simkl":
		endpoint := c.simkl.baseURL + "/oauth/pin?client_id=" + url.QueryEscape(c.simkl.clientID)
		var response struct {
			UserCode        string `json:"user_code"`
			VerificationURI string `json:"verification_uri"`
			VerificationURL string `json:"verification_url"`
			ExpiresIn       int    `json:"expires_in"`
			Interval        int    `json:"interval"`
		}
		if err := c.request(ctx, provider, http.MethodGet, endpoint, "", nil, &response); err != nil {
			return deviceCode{}, err
		}
		verificationURL := response.VerificationURI
		if verificationURL == "" {
			verificationURL = response.VerificationURL
		}
		verificationURL, err := validatedVerificationURL(provider, verificationURL)
		if err != nil {
			return deviceCode{}, err
		}
		return deviceCode{Provider: provider, ProviderCode: response.UserCode, UserCode: response.UserCode, VerificationURL: verificationURL, ExpiresIn: response.ExpiresIn, Interval: response.Interval}, nil
	default:
		return deviceCode{}, ErrInvalidInput
	}
}

func (c *providerClient) pollDeviceAuthorization(ctx context.Context, provider, code string) (providerToken, error) {
	switch provider {
	case "trakt":
		var response struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
			CreatedAt    int64  `json:"created_at"`
		}
		err := c.request(ctx, provider, http.MethodPost, c.trakt.authURL+"/oauth/device/token", "", map[string]string{"code": code, "client_id": c.trakt.clientID, "client_secret": c.trakt.clientSecret}, &response)
		if upstream, ok := err.(*upstreamError); ok {
			switch upstream.status {
			case 400:
				return providerToken{}, ErrAuthorizationWait
			case 404, 409, 410:
				return providerToken{}, ErrAuthorizationGone
			case 418:
				return providerToken{}, ErrAuthorizationDenied
			case 429:
				return providerToken{}, ErrAuthorizationSlow
			}
		}
		if err != nil {
			return providerToken{}, err
		}
		if response.AccessToken == "" || response.RefreshToken == "" || len(response.AccessToken) > 8192 || len(response.RefreshToken) > 8192 || response.ExpiresIn < 1 {
			return providerToken{}, fmt.Errorf("%w: invalid Trakt token response", ErrProviderUnavailable)
		}
		created := time.Unix(response.CreatedAt, 0).UTC()
		if response.CreatedAt == 0 {
			created = time.Now().UTC()
		}
		expires := created.Add(time.Duration(response.ExpiresIn) * time.Second)
		return providerToken{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, ExpiresAt: &expires}, nil
	case "simkl":
		endpoint := c.simkl.baseURL + "/oauth/pin/" + url.PathEscape(code) + "?client_id=" + url.QueryEscape(c.simkl.clientID)
		var response struct {
			Result      string `json:"result"`
			Message     string `json:"message"`
			AccessToken string `json:"access_token"`
		}
		if err := c.request(ctx, provider, http.MethodGet, endpoint, "", nil, &response); err != nil {
			return providerToken{}, err
		}
		if response.Result != "OK" || response.AccessToken == "" {
			return providerToken{}, ErrAuthorizationWait
		}
		if len(response.AccessToken) > 8192 {
			return providerToken{}, fmt.Errorf("%w: invalid Simkl token response", ErrProviderUnavailable)
		}
		return providerToken{AccessToken: response.AccessToken}, nil
	default:
		return providerToken{}, ErrInvalidInput
	}
}

func (c *providerClient) refreshTrakt(ctx context.Context, refreshToken string) (providerToken, error) {
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		CreatedAt    int64  `json:"created_at"`
	}
	err := c.request(ctx, "trakt", http.MethodPost, c.trakt.authURL+"/oauth/token", "", map[string]string{
		"refresh_token": refreshToken, "client_id": c.trakt.clientID, "client_secret": c.trakt.clientSecret,
		"redirect_uri": "urn:ietf:wg:oauth:2.0:oob", "grant_type": "refresh_token",
	}, &response)
	if err != nil {
		return providerToken{}, err
	}
	if response.AccessToken == "" || response.RefreshToken == "" || len(response.AccessToken) > 8192 || len(response.RefreshToken) > 8192 || response.ExpiresIn < 1 {
		return providerToken{}, fmt.Errorf("%w: invalid Trakt refresh response", ErrProviderUnavailable)
	}
	created := time.Unix(response.CreatedAt, 0).UTC()
	if response.CreatedAt == 0 {
		created = time.Now().UTC()
	}
	expires := created.Add(time.Duration(response.ExpiresIn) * time.Second)
	return providerToken{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, ExpiresAt: &expires}, nil
}

func (c *providerClient) revoke(ctx context.Context, provider, accessToken string) error {
	if provider != "trakt" {
		return nil
	}
	return c.request(ctx, provider, http.MethodPost, c.trakt.authURL+"/oauth/revoke", "", map[string]string{"token": accessToken, "client_id": c.trakt.clientID, "client_secret": c.trakt.clientSecret}, nil)
}

func (c *providerClient) send(ctx context.Context, provider, accessToken, eventType string, event Event, item mediaItem) error {
	if len(item.IDs) == 0 && len(item.ShowIDs) == 0 {
		return fmt.Errorf("%w: no IMDb, TMDB, or TVDB mapping", ErrInvalidInput)
	}
	switch eventType {
	case "progress":
		if event.Cleared {
			return c.clearPlayback(ctx, provider, accessToken, item)
		}
		progress := 0.0
		if event.DurationSeconds > 0 {
			progress = float64(event.PositionSeconds) * 100 / float64(event.DurationSeconds)
		}
		action := "pause"
		if event.PositionSeconds == 0 {
			action = "start"
		}
		if event.Completed {
			action = "stop"
			progress = 100
		}
		err := c.authorizedRequest(ctx, provider, http.MethodPost, "/scrobble/"+action, accessToken, scrobbleBody(item, progress))
		if upstream, ok := err.(*upstreamError); ok && provider == "trakt" && action == "stop" && upstream.status == http.StatusConflict {
			return nil
		}
		return err
	case "watched":
		if event.Completed {
			err := c.authorizedRequest(ctx, provider, http.MethodPost, "/scrobble/stop", accessToken, scrobbleBody(item, 100))
			if upstream, ok := err.(*upstreamError); ok && provider == "trakt" && upstream.status == http.StatusConflict {
				return nil
			}
			return err
		}
		return c.authorizedRequest(ctx, provider, http.MethodPost, "/sync/history/remove", accessToken, syncBody(item, event, false, provider))
	case "library":
		if provider == "trakt" {
			path := "/sync/collection"
			if !event.InLibrary {
				path += "/remove"
			}
			return c.authorizedRequest(ctx, provider, http.MethodPost, path, accessToken, syncBody(item, event, true, provider))
		}
		path := "/sync/add-to-list"
		if !event.InLibrary {
			path = "/sync/history/remove"
		}
		return c.authorizedRequest(ctx, provider, http.MethodPost, path, accessToken, syncBody(item, event, true, provider))
	default:
		return fmt.Errorf("%w: unsupported tracking event", ErrInvalidInput)
	}
}

type remoteMedia struct {
	IDs map[string]any `json:"ids"`
}

type remoteEpisode struct {
	Season int            `json:"season"`
	Number int            `json:"number"`
	IDs    map[string]any `json:"ids"`
}

func (c *providerClient) clearPlayback(ctx context.Context, provider, accessToken string, item mediaItem) error {
	var playbacks []struct {
		ID      int64          `json:"id"`
		Movie   *remoteMedia   `json:"movie"`
		Show    *remoteMedia   `json:"show"`
		Anime   *remoteMedia   `json:"anime"`
		Episode *remoteEpisode `json:"episode"`
	}
	if err := c.authorizedRequestOutput(ctx, provider, http.MethodGet, "/sync/playback", accessToken, nil, &playbacks); err != nil {
		return err
	}
	for _, playback := range playbacks {
		matches := item.MediaType == "movie" && playback.Movie != nil && remoteIDsMatch(item.IDs, playback.Movie.IDs)
		if item.MediaType == "episode" && playback.Episode != nil && playback.Episode.Season == item.Season && playback.Episode.Number == item.Episode {
			container := playback.Show
			if container == nil {
				container = playback.Anime
			}
			matches = remoteIDsMatch(item.IDs, playback.Episode.IDs) || container != nil && remoteIDsMatch(item.ShowIDs, container.IDs)
		}
		if matches {
			if err := c.authorizedRequest(ctx, provider, http.MethodDelete, "/sync/playback/"+strconv.FormatInt(playback.ID, 10), accessToken, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func remoteIDsMatch(local, remote map[string]any) bool {
	for key, localValue := range local {
		if remoteValue, ok := remote[key]; ok && fmt.Sprint(localValue) == fmt.Sprint(remoteValue) {
			return true
		}
	}
	return false
}

func scrobbleBody(item mediaItem, progress float64) map[string]any {
	body := map[string]any{"progress": progress}
	if item.MediaType == "movie" {
		body["movie"] = itemObject(item.Title, item.Year, item.IDs)
		return body
	}
	body["show"] = itemObject(item.ShowTitle, item.ShowYear, item.ShowIDs)
	episode := itemObject(item.Title, item.Year, item.IDs)
	episode["season"] = item.Season
	episode["number"] = item.Episode
	body["episode"] = episode
	return body
}

func syncBody(item mediaItem, event Event, library bool, provider string) map[string]any {
	occurredAt := event.OccurredAt.UTC().Format(time.RFC3339)
	if item.MediaType == "movie" {
		object := itemObject(item.Title, item.Year, item.IDs)
		if library {
			object["collected_at"] = occurredAt
		} else {
			object["watched_at"] = occurredAt
		}
		if provider == "simkl" && library && event.InLibrary {
			object["to"] = "plantowatch"
		}
		return map[string]any{"movies": []any{object}}
	}
	if library {
		object := itemObject(item.Title, item.Year, item.IDs)
		if provider == "simkl" && event.InLibrary {
			object["to"] = "plantowatch"
		} else {
			object["collected_at"] = occurredAt
		}
		return map[string]any{"shows": []any{object}}
	}
	show := itemObject(item.ShowTitle, item.ShowYear, item.ShowIDs)
	show["seasons"] = []any{map[string]any{"number": item.Season, "episodes": []any{map[string]any{"number": item.Episode, "watched_at": occurredAt}}}}
	return map[string]any{"shows": []any{show}}
}

func itemObject(title string, year int, ids map[string]any) map[string]any {
	object := map[string]any{"ids": ids}
	if title != "" {
		object["title"] = title
	}
	if year > 0 {
		object["year"] = year
	}
	return object
}

func (c *providerClient) authorizedRequest(ctx context.Context, provider, method, path, accessToken string, body any) error {
	return c.authorizedRequestOutput(ctx, provider, method, path, accessToken, body, nil)
}

func (c *providerClient) authorizedRequestOutput(ctx context.Context, provider, method, path, accessToken string, body, output any) error {
	config := c.trakt
	if provider == "simkl" {
		config = c.simkl
	}
	endpoint := config.baseURL + path
	if provider == "simkl" {
		query := url.Values{"client_id": {config.clientID}, "app-name": {"rivune"}, "app-version": {"1"}}
		endpoint += "?" + query.Encode()
	}
	return c.request(ctx, provider, method, endpoint, accessToken, body, output)
}

func (c *providerClient) request(ctx context.Context, provider, method, endpoint, accessToken string, body, output any) error {
	var encoded io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode tracking provider request: %w", err)
		}
		encoded = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, encoded)
	if err != nil {
		return fmt.Errorf("create tracking provider request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Rivune/1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if provider == "trakt" {
		req.Header.Set("trakt-api-version", "2")
		req.Header.Set("trakt-api-key", c.trakt.clientID)
	} else if provider == "simkl" {
		req.Header.Set("simkl-api-key", c.simkl.clientID)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: request failed", ErrProviderUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryAfter := time.Duration(0)
		if seconds, parseErr := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); parseErr == nil {
			retryAfter = time.Duration(seconds) * time.Second
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumProviderResponseBytes))
		return &upstreamError{status: response.StatusCode, retryAfter: retryAfter}
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumProviderResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read provider response", ErrProviderUnavailable)
	}
	if len(payload) > maximumProviderResponseBytes {
		return fmt.Errorf("%w: provider response too large", ErrProviderUnavailable)
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(payload, output); err != nil {
		return fmt.Errorf("decode tracking provider response: %w", err)
	}
	return nil
}
