package addon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/netguard"
)

const (
	maximumManifestBytes = 2 << 20
	maximumResourceBytes = 16 << 20
)

type providerUnavailableError struct {
	operation  string
	cause      error
	temporary  bool
	statusCode int
}

func (err *providerUnavailableError) Error() string {
	if err.statusCode != 0 {
		return fmt.Sprintf("%s: %s: HTTP %d", ErrProviderUnavailable, err.operation, err.statusCode)
	}
	return fmt.Sprintf("%s: %s", ErrProviderUnavailable, err.operation)
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

func unavailableHTTPStatus(operation string, statusCode int, temporary bool) error {
	return &providerUnavailableError{operation: operation, temporary: temporary, statusCode: statusCode}
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

type aggregateResourceBudget struct {
	mu        sync.Mutex
	limit     int64
	used      int64
	reserved  int64
	itemLimit int64
	items     int64
	exceeded  bool
	cancel    context.CancelFunc
	done      chan struct{}
	changed   chan struct{}
	limitErr  func() error
}

func newAggregateResourceBudget(limit int64, cancel context.CancelFunc) *aggregateResourceBudget {
	return &aggregateResourceBudget{
		limit:    limit,
		cancel:   cancel,
		done:     make(chan struct{}),
		changed:  make(chan struct{}),
		limitErr: aggregateResourceLimitError,
	}
}

func (budget *aggregateResourceBudget) signalLocked() {
	close(budget.changed)
	budget.changed = make(chan struct{})
}

func (budget *aggregateResourceBudget) reserve(ctx context.Context, maximum int) (int, bool, error) {
	for {
		budget.mu.Lock()
		if budget.exceeded {
			budget.mu.Unlock()
			return 0, false, budget.limitError()
		}
		available := budget.limit - budget.used - budget.reserved
		if available > 0 {
			reserved := min(int64(maximum), available)
			budget.reserved += reserved
			budget.mu.Unlock()
			return int(reserved), false, nil
		}
		if budget.reserved == 0 {
			budget.mu.Unlock()
			return 1, true, nil
		}
		changed := budget.changed
		budget.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return 0, false, ctx.Err()
		}
	}
}

func (budget *aggregateResourceBudget) finishReservation(reserved int, read int) {
	budget.mu.Lock()
	budget.reserved -= int64(reserved)
	budget.used += int64(read)
	budget.signalLocked()
	budget.mu.Unlock()
}

func (budget *aggregateResourceBudget) limitError() error {
	if budget.limitErr != nil {
		return budget.limitErr()
	}
	return fmt.Errorf("%w: provider payload budget exceeded", ErrInvalidResponse)
}

func (budget *aggregateResourceBudget) exceed() error {
	budget.mu.Lock()
	first := !budget.exceeded
	if first {
		budget.exceeded = true
		close(budget.done)
		budget.signalLocked()
	}
	budget.mu.Unlock()
	if first {
		budget.cancel()
	}
	return budget.limitError()
}

func (budget *aggregateResourceBudget) consumeMaterialized(size int) error {
	budget.mu.Lock()
	if budget.exceeded || int64(size) > budget.limit-budget.used-budget.reserved {
		budget.mu.Unlock()
		return budget.exceed()
	}
	budget.used += int64(size)
	budget.signalLocked()
	budget.mu.Unlock()
	return nil
}

func (budget *aggregateResourceBudget) consumeItems(count int) error {
	if count < 0 {
		return budget.exceed()
	}
	budget.mu.Lock()
	if budget.exceeded || budget.itemLimit > 0 && int64(count) > budget.itemLimit-budget.items {
		budget.mu.Unlock()
		return budget.exceed()
	}
	budget.items += int64(count)
	budget.mu.Unlock()
	return nil
}

func (budget *aggregateResourceBudget) wasExceeded() bool {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.exceeded
}

type payloadBudgetContextKey struct{}
type payloadBudgetSourceContextKey struct{}

type PayloadBudget = aggregateResourceBudget

type payloadBudgetSource struct {
	mu     sync.Mutex
	budget *aggregateResourceBudget
	bytes  int
	items  int
}

func WithPayloadBudget(ctx context.Context, byteLimit, itemLimit int64) (context.Context, *PayloadBudget) {
	budgetCtx, cancel := context.WithCancel(ctx)
	budget := &aggregateResourceBudget{
		limit: byteLimit, itemLimit: itemLimit, cancel: cancel,
		done: make(chan struct{}), changed: make(chan struct{}),
	}
	return context.WithValue(budgetCtx, payloadBudgetContextKey{}, budget), budget
}

func WithPayloadBudgetSource(ctx context.Context) context.Context {
	budget, _ := ctx.Value(payloadBudgetContextKey{}).(*aggregateResourceBudget)
	if budget == nil {
		return ctx
	}
	return context.WithValue(ctx, payloadBudgetSourceContextKey{}, &payloadBudgetSource{budget: budget})
}

func BudgetedPayloadReader(ctx context.Context, source io.Reader) io.Reader {
	tracker, _ := ctx.Value(payloadBudgetSourceContextKey{}).(*payloadBudgetSource)
	if tracker == nil {
		return source
	}
	return &aggregateBudgetReader{ctx: ctx, source: source, budget: tracker.budget, tracker: tracker}
}

func EnsurePayloadBytes(ctx context.Context, total int) error {
	tracker, _ := ctx.Value(payloadBudgetSourceContextKey{}).(*payloadBudgetSource)
	if tracker == nil {
		return nil
	}
	tracker.mu.Lock()
	missing := total - tracker.bytes
	tracker.mu.Unlock()
	if missing <= 0 {
		return nil
	}
	if err := tracker.budget.consumeMaterialized(missing); err != nil {
		return err
	}
	tracker.addBytes(missing)
	return nil
}

func ConsumePayloadItems(ctx context.Context, count int) error {
	tracker, _ := ctx.Value(payloadBudgetSourceContextKey{}).(*payloadBudgetSource)
	if tracker == nil {
		return nil
	}
	if err := tracker.budget.consumeItems(count); err != nil {
		return err
	}
	tracker.mu.Lock()
	tracker.items += count
	tracker.mu.Unlock()
	return nil
}

func EnsurePayloadItems(ctx context.Context, total int) error {
	tracker, _ := ctx.Value(payloadBudgetSourceContextKey{}).(*payloadBudgetSource)
	if tracker == nil {
		return nil
	}
	tracker.mu.Lock()
	missing := total - tracker.items
	tracker.mu.Unlock()
	if missing <= 0 {
		return nil
	}
	return ConsumePayloadItems(ctx, missing)
}

func (budget *aggregateResourceBudget) Exceeded() bool {
	return budget.wasExceeded()
}

func (budget *aggregateResourceBudget) Cancel() {
	budget.cancel()
}

func (source *payloadBudgetSource) addBytes(count int) {
	if source == nil || count == 0 {
		return
	}
	source.mu.Lock()
	source.bytes += count
	source.mu.Unlock()
}

type aggregateBudgetReader struct {
	ctx     context.Context
	source  io.Reader
	budget  *aggregateResourceBudget
	tracker *payloadBudgetSource
}

func (reader *aggregateBudgetReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	reserved, sentinel, err := reader.budget.reserve(reader.ctx, len(destination))
	if err != nil {
		return 0, err
	}
	if sentinel {
		var probe [1]byte
		read, readErr := reader.source.Read(probe[:])
		if read > 0 {
			return 0, reader.budget.exceed()
		}
		return 0, readErr
	}
	read, readErr := reader.source.Read(destination[:reserved])
	reader.budget.finishReservation(reserved, read)
	reader.tracker.addBytes(read)
	return read, readErr
}

type aggregateBudgetTransport interface {
	resourceWithBudget(context.Context, string, ResourcePath, *aggregateResourceBudget) (json.RawMessage, CachePolicy, error)
}

type HTTPTransport struct {
	publicClient         *http.Client
	privateLiteralClient *http.Client
}

func NewHTTPTransport(client *http.Client) *HTTPTransport {
	if client != nil {
		return &HTTPTransport{publicClient: client, privateLiteralClient: client}
	}
	return &HTTPTransport{
		publicClient:         guardedHTTPClient(netguard.DialContextPublic, true),
		privateLiteralClient: guardedHTTPClient(netguard.DialContext, false),
	}
}

func guardedHTTPClient(dialContext func(context.Context, string, string) (net.Conn, error), allowRedirects bool) *http.Client {
	httpTransport := http.DefaultTransport.(*http.Transport).Clone()
	httpTransport.Proxy = nil
	httpTransport.DialContext = dialContext
	httpTransport.MaxResponseHeaderBytes = 64 << 10
	return &http.Client{
		Transport: httpTransport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if !allowRedirects && len(via) > 0 {
				return errorsText("private-network addon redirects are not supported")
			}
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
	return transport.resource(ctx, transportURL, path, nil)
}

func (transport *HTTPTransport) resourceWithBudget(ctx context.Context, transportURL string, path ResourcePath, budget *aggregateResourceBudget) (json.RawMessage, CachePolicy, error) {
	return transport.resource(ctx, transportURL, path, budget)
}

func (transport *HTTPTransport) resource(ctx context.Context, transportURL string, path ResourcePath, budget *aggregateResourceBudget) (json.RawMessage, CachePolicy, error) {
	if err := validateResourcePath(path); err != nil {
		return nil, CachePolicy{}, err
	}
	resourceURL, err := buildResourceURL(transportURL, path)
	if err != nil {
		return nil, CachePolicy{}, err
	}
	payload, cache, err := transport.getWithBudget(ctx, resourceURL, maximumResourceBytes, budget)
	if err != nil {
		return nil, CachePolicy{}, err
	}
	if path.Resource != "catalog" && path.Resource != "addon_catalog" && !json.Valid(payload) {
		return nil, CachePolicy{}, fmt.Errorf("%w: response contains invalid JSON", ErrInvalidResponse)
	}
	if path.Resource != "catalog" && path.Resource != "addon_catalog" && firstJSONToken(payload) != '{' {
		return nil, CachePolicy{}, fmt.Errorf("%w: response must be a JSON object", ErrInvalidResponse)
	}
	if err := validateResourceResponseContext(ctx, path.Resource, payload); err != nil {
		return nil, CachePolicy{}, err
	}
	cache = mergeBodyCache(payload, cache)
	return json.RawMessage(payload), cache, nil
}

func (transport *HTTPTransport) get(ctx context.Context, target string, maximumBytes int64) ([]byte, CachePolicy, error) {
	return transport.getWithBudget(ctx, target, maximumBytes, nil)
}

func (transport *HTTPTransport) getWithBudget(ctx context.Context, target string, maximumBytes int64, budget *aggregateResourceBudget) ([]byte, CachePolicy, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, CachePolicy{}, unavailable("construct request", err, false)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Rivune/1 StremioAddonClient")
	client := transport.publicClient
	if isPrivateNetworkTransportURL(target) {
		client = transport.privateLiteralClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, CachePolicy{}, unavailable("request failed", err, true)
	}
	defer response.Body.Close()
	var source io.Reader = BudgetedPayloadReader(ctx, response.Body)
	if budget != nil {
		source = &aggregateBudgetReader{ctx: ctx, source: source, budget: budget}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, drainErr := io.Copy(io.Discard, io.LimitReader(source, 32<<10))
		if errors.Is(drainErr, ErrInvalidResponse) {
			return nil, CachePolicy{}, drainErr
		}
		temporary := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError && response.StatusCode <= 599
		return nil, CachePolicy{}, unavailableHTTPStatus("request failed", response.StatusCode, temporary)
	}
	limited := io.LimitReader(source, maximumBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		if errors.Is(err, ErrInvalidResponse) {
			return nil, CachePolicy{}, err
		}
		return nil, CachePolicy{}, unavailable("read response", err, true)
	}
	if int64(len(payload)) > maximumBytes {
		return nil, CachePolicy{}, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, maximumBytes)
	}
	return payload, parseCacheControl(response.Header.Get("Cache-Control")), nil
}

func isPrivateNetworkTransportURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	return err == nil && netguard.IsAllowedAddress(address) && netguard.IsPrivateNetworkAddress(address)
}

func validateResourceResponse(resource string, payload []byte) error {
	return validateResourceResponseContext(context.Background(), resource, payload)
}

func validateResourceResponseContext(ctx context.Context, resource string, payload []byte) error {
	if resource == "catalog" || resource == "addon_catalog" {
		if err := validateExposablePayloadComplexity(ctx, payload); err != nil {
			return err
		}
	}
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
	switch resource {
	case "stream":
		_, err := ParseProviderStreamResponse(payload)
		return err
	case "subtitles":
		_, err := ParseProviderSubtitleResponse(payload)
		return err
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
