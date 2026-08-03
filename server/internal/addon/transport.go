package addon

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
)

const (
	maximumManifestBytes = 2 << 20
	maximumResourceBytes = 16 << 20
)

type providerUnavailableError struct {
	operation string
	cause     error
	temporary bool
}

func (err *providerUnavailableError) Error() string {
	return fmt.Sprintf("%s: %s: %v", ErrProviderUnavailable, err.operation, err.cause)
}

func (err *providerUnavailableError) Unwrap() error {
	return err.cause
}

func (err *providerUnavailableError) Is(target error) bool {
	return target == ErrProviderUnavailable
}

func (err *providerUnavailableError) Temporary() bool {
	return err.temporary
}

func unavailable(operation string, cause error, temporary bool) error {
	return &providerUnavailableError{operation: operation, cause: cause, temporary: temporary}
}

func isTemporaryProviderError(err error) bool {
	var temporary interface {
		Temporary() bool
	}
	return errors.As(err, &temporary) && temporary.Temporary()
}

type Transport interface {
	Manifest(context.Context, string) (Manifest, json.RawMessage, error)
	Resource(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error)
}

type HTTPTransport struct {
	client *http.Client
}

func NewHTTPTransport(client *http.Client) *HTTPTransport {
	if client == nil {
		client = &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errorsText("too many redirects")
				}
				if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && request.URL.Scheme != "https" {
					return errorsText("HTTPS redirect downgrade refused")
				}
				if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
					return errorsText("unsupported redirect scheme")
				}
				return nil
			},
		}
	}
	return &HTTPTransport{client: client}
}

func (transport *HTTPTransport) Manifest(ctx context.Context, transportURL string) (Manifest, json.RawMessage, error) {
	normalized, err := NormalizeTransportURL(transportURL)
	if err != nil {
		return Manifest{}, nil, err
	}
	payload, _, err := transport.get(ctx, normalized, maximumManifestBytes)
	if err != nil {
		return Manifest{}, nil, err
	}
	return ParseManifest(payload)
}

func (transport *HTTPTransport) Resource(ctx context.Context, transportURL string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
	if err := validateResourcePath(path); err != nil {
		return nil, CachePolicy{}, err
	}
	resourceURL, err := buildResourceURL(transportURL, path)
	if err != nil {
		return nil, CachePolicy{}, err
	}
	payload, cache, err := transport.get(ctx, resourceURL, maximumResourceBytes)
	if err != nil {
		return nil, CachePolicy{}, err
	}
	if !json.Valid(payload) {
		return nil, CachePolicy{}, fmt.Errorf("%w: response contains invalid JSON", ErrInvalidResponse)
	}
	if firstJSONToken(payload) != '{' {
		return nil, CachePolicy{}, fmt.Errorf("%w: response must be a JSON object", ErrInvalidResponse)
	}
	if err := validateResourceResponse(path.Resource, payload); err != nil {
		return nil, CachePolicy{}, err
	}
	cache = mergeBodyCache(payload, cache)
	return json.RawMessage(payload), cache, nil
}

func (transport *HTTPTransport) get(ctx context.Context, target string, maximumBytes int64) ([]byte, CachePolicy, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, CachePolicy{}, unavailable("construct request", err, false)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Rivune/1 StremioAddonClient")
	response, err := transport.client.Do(request)
	if err != nil {
		return nil, CachePolicy{}, unavailable("request failed", err, true)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		temporary := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError && response.StatusCode <= 599
		return nil, CachePolicy{}, unavailable("request failed", errorsText(fmt.Sprintf("HTTP %d", response.StatusCode)), temporary)
	}
	limited := io.LimitReader(response.Body, maximumBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, CachePolicy{}, unavailable("read response", err, true)
	}
	if int64(len(payload)) > maximumBytes {
		return nil, CachePolicy{}, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, maximumBytes)
	}
	return payload, parseCacheControl(response.Header.Get("Cache-Control")), nil
}

func validateResourceResponse(resource string, payload []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return fmt.Errorf("%w: decode %s response: %v", ErrInvalidResponse, resource, err)
	}
	field := ""
	token := byte('[')
	kind := "array"
	switch resource {
	case "catalog", "addon_catalog":
		field = "metas"
	case "stream":
		field = "streams"
	case "subtitles":
		field = "subtitles"
	case "meta":
		field = "meta"
		token = '{'
		kind = "object"
	default:
		return nil
	}
	value, ok := object[field]
	if !ok || firstJSONToken(value) != token {
		return fmt.Errorf("%w: %s response requires %q %s", ErrInvalidResponse, resource, field, kind)
	}
	return nil
}

func buildResourceURL(transportURL string, path ResourcePath) (string, error) {
	normalized, err := NormalizeTransportURL(transportURL)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(normalized)
	if err != nil || !strings.HasSuffix(base.Path, "/manifest.json") {
		return "", ErrInvalidTransportURL
	}
	prefix := strings.TrimSuffix(base.EscapedPath(), "/manifest.json")
	segments := []string{escapeURIComponent(path.Resource), escapeURIComponent(path.Type), escapeURIComponent(path.ID)}
	if len(path.Extra) > 0 {
		extra := make([]string, 0, len(path.Extra))
		for _, value := range path.Extra {
			extra = append(extra, escapeURIComponent(value.Name)+"="+escapeURIComponent(value.Value))
		}
		segments = append(segments, strings.Join(extra, "&"))
	}
	escapedPath := prefix + "/" + strings.Join(segments, "/") + ".json"
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", ErrInvalidTransportURL
	}
	base.Path = decodedPath
	base.RawPath = escapedPath
	return base.String(), nil
}

func escapeURIComponent(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("-_.!~*'()", rune(character)) {
			builder.WriteByte(character)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hexadecimal[character>>4])
		builder.WriteByte(hexadecimal[character&15])
	}
	return builder.String()
}

func validateResourcePath(path ResourcePath) error {
	if invalidToken(path.Resource, 256) || invalidToken(path.Type, 256) || invalidValue(path.ID, 8192) || path.ID == "" || len(path.Extra) > 128 {
		return ErrInvalidInput
	}
	for _, value := range path.Extra {
		if invalidToken(value.Name, 256) || invalidValue(value.Value, 8192) {
			return ErrInvalidInput
		}
	}
	return nil
}

func parseCacheControl(header string) CachePolicy {
	policy := CachePolicy{}
	for _, rawDirective := range strings.Split(header, ",") {
		name, rawValue, found := strings.Cut(strings.TrimSpace(rawDirective), "=")
		if !found {
			continue
		}
		value, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(rawValue), `"`), 10, 64)
		if err != nil || value < 0 {
			continue
		}
		switch strings.ToLower(name) {
		case "max-age":
			policy.MaxAgeSeconds = &value
		case "stale-while-revalidate":
			policy.StaleWhileRevalidateSeconds = &value
		case "stale-if-error":
			policy.StaleIfErrorSeconds = &value
		}
	}
	return policy
}

func mergeBodyCache(payload []byte, policy CachePolicy) CachePolicy {
	var body struct {
		CacheMaxAge     *int64 `json:"cacheMaxAge"`
		StaleRevalidate *int64 `json:"staleRevalidate"`
		StaleError      *int64 `json:"staleError"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return policy
	}
	if policy.MaxAgeSeconds == nil && nonnegative(body.CacheMaxAge) {
		policy.MaxAgeSeconds = body.CacheMaxAge
	}
	if policy.StaleWhileRevalidateSeconds == nil && nonnegative(body.StaleRevalidate) {
		policy.StaleWhileRevalidateSeconds = body.StaleRevalidate
	}
	if policy.StaleIfErrorSeconds == nil && nonnegative(body.StaleError) {
		policy.StaleIfErrorSeconds = body.StaleError
	}
	return policy
}

func nonnegative(value *int64) bool {
	return value != nil && *value >= 0
}

func firstJSONToken(payload []byte) byte {
	for _, character := range payload {
		switch character {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return character
		}
	}
	return 0
}

type errorsText string

func (err errorsText) Error() string { return string(err) }
