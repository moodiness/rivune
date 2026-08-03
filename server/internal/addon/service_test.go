package addon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type catalogSearchTransport struct {
	mu    sync.Mutex
	paths []ResourcePath
}

func (transport *catalogSearchTransport) Manifest(context.Context, string) (Manifest, json.RawMessage, error) {
	return Manifest{}, nil, errors.New("unexpected manifest request")
}

func (transport *catalogSearchTransport) Resource(_ context.Context, _ string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
	transport.mu.Lock()
	transport.paths = append(transport.paths, path)
	transport.mu.Unlock()
	if path.ID == "broken" {
		return nil, CachePolicy{}, ErrProviderUnavailable
	}
	return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
}

type resourceTransportFunc func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error)

type functionTransport struct {
	resource resourceTransportFunc
}

func (transport functionTransport) Manifest(context.Context, string) (Manifest, json.RawMessage, error) {
	return Manifest{}, nil, errors.New("unexpected manifest request")
}

func (transport functionTransport) Resource(ctx context.Context, transportURL string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
	return transport.resource(ctx, transportURL, path)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func executeTestRequest(id string) plannedRequest {
	return plannedRequest{
		addon: InstalledAddon{
			ID:           "addon-" + id,
			TransportURL: "https://" + id + ".example/manifest.json",
			parsedManifest: Manifest{
				ID: "manifest-" + id,
			},
		},
		path: ResourcePath{Resource: "catalog", Type: "movie", ID: id},
	}
}

func TestPlanCatalogSearchAdaptsEachTVCatalog(t *testing.T) {
	manifest := Manifest{
		ID:        "org.example.tv",
		Types:     []string{"movie", "tv"},
		Resources: []ManifestResource{{Name: "catalog", Short: true}},
		Catalogs: []ManifestCatalog{
			{
				Type: "tv",
				ID:   "modern",
				Extra: []ExtraProp{
					{Name: "search", IsRequired: true},
					{Name: "skip"},
					{Name: "limit"},
					{Name: "country", IsRequired: true, Default: "US"},
				},
			},
			{
				Type:           "tv",
				ID:             "legacy",
				ExtraRequired:  []string{"search"},
				ExtraSupported: []string{"search", "limit"},
			},
			{Type: "tv", ID: "broken", ExtraSupported: []string{"search", "skip"}},
			{Type: "tv", ID: "not-searchable", Extra: []ExtraProp{{Name: "skip"}}},
			{
				Type: "tv",
				ID:   "missing-required",
				Extra: []ExtraProp{
					{Name: "search"},
					{Name: "token", IsRequired: true},
				},
			},
		},
	}
	partialTVManifest := Manifest{
		ID:        "org.example.partial-tv",
		Types:     []string{"movie"},
		Resources: []ManifestResource{{Name: "catalog", Short: true}},
		Catalogs:  []ManifestCatalog{{Type: "tv", ID: "partial", ExtraSupported: []string{"search", "skip"}}},
	}
	addons := []InstalledAddon{
		{ID: "addon-tv", TransportURL: "https://tv.example/manifest.json", parsedManifest: manifest},
		{ID: "addon-partial", TransportURL: "https://partial.example/manifest.json", parsedManifest: partialTVManifest},
	}

	firstPage := planCatalogSearch(addons, "tv", CatalogSearchInput{Search: "news", Limit: 24})
	if got, want := requestIDs(firstPage), []string{"modern", "legacy", "broken"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first-page catalog order = %#v, want %#v", got, want)
	}
	if got, want := firstPage[0].path.Extra, []ExtraValue{
		{Name: "search", Value: "news"},
		{Name: "skip", Value: "0"},
		{Name: "limit", Value: "24"},
		{Name: "country", Value: "US"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("modern extras = %#v, want %#v", got, want)
	}
	if got, want := firstPage[1].path.Extra, []ExtraValue{
		{Name: "search", Value: "news"},
		{Name: "limit", Value: "24"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy extras = %#v, want %#v", got, want)
	}

	nextPage := planCatalogSearch(addons, "tv", CatalogSearchInput{Search: "news", Skip: 24, Limit: 24})
	if got, want := requestIDs(nextPage), []string{"modern", "broken"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("next-page catalog order = %#v, want %#v", got, want)
	}
	if got := nextPage[0].path.Extra[1]; got != (ExtraValue{Name: "skip", Value: "24"}) {
		t.Fatalf("next-page skip = %#v", got)
	}
}

func TestCatalogSearchDeduplicatesRequestsAndKeepsPartialResults(t *testing.T) {
	manifest := Manifest{
		ID:        "org.example.tv",
		Types:     []string{"tv"},
		Resources: []ManifestResource{{Name: "catalog", Short: true}},
		Catalogs: []ManifestCatalog{
			{Type: "tv", ID: "working", ExtraSupported: []string{"search", "skip", "limit"}},
			{Type: "tv", ID: "broken", ExtraSupported: []string{"search", "skip", "limit"}},
		},
	}
	addons := []InstalledAddon{
		{ID: "first", TransportURL: "https://same.example/manifest.json", parsedManifest: manifest},
		{ID: "duplicate", TransportURL: "https://same.example/manifest.json", parsedManifest: manifest},
	}
	requests := planCatalogSearch(addons, "tv", CatalogSearchInput{Search: "sports", Skip: 0, Limit: 24})
	if len(requests) != 2 {
		t.Fatalf("planned %d requests, want 2 unique requests", len(requests))
	}

	transport := &catalogSearchTransport{}
	service := Service{transport: transport, logger: discardLogger()}
	batch := service.execute(context.Background(), requests)
	if len(batch.Results) != 1 || batch.Results[0].ID != "working" {
		t.Fatalf("unexpected successful results: %#v", batch.Results)
	}
	if len(batch.Errors) != 1 || batch.Errors[0].AddonID != "first" || batch.Errors[0].Code != "addon_unavailable" {
		t.Fatalf("unexpected isolated failures: %#v", batch.Errors)
	}
	if got, want := batch.Results[0].Extra, requests[0].path.Extra; !reflect.DeepEqual(got, want) {
		t.Fatalf("result extras = %#v, want sent extras %#v", got, want)
	}
	transport.mu.Lock()
	callCount := len(transport.paths)
	transport.mu.Unlock()
	if callCount != 2 {
		t.Fatalf("transport received %d calls, want 2", callCount)
	}
}
func TestPlanCatalogSearchRequiresDeclaredTypeAndCatalogResource(t *testing.T) {
	searchable := ManifestCatalog{Type: "movie", ID: "search", ExtraSupported: []string{"search"}}
	wrongResourceTypes := []string{"series"}
	addons := []InstalledAddon{
		{
			ID: "missing-type",
			parsedManifest: Manifest{
				Types:     []string{"series"},
				Resources: []ManifestResource{{Name: "catalog", Short: true}},
				Catalogs:  []ManifestCatalog{searchable},
			},
		},
		{
			ID: "missing-resource",
			parsedManifest: Manifest{
				Types:     []string{"movie"},
				Resources: []ManifestResource{{Name: "meta", Short: true}},
				Catalogs:  []ManifestCatalog{searchable},
			},
		},
		{
			ID: "resource-excludes-type",
			parsedManifest: Manifest{
				Types:     []string{"movie"},
				Resources: []ManifestResource{{Name: "catalog", Types: &wrongResourceTypes}},
				Catalogs:  []ManifestCatalog{searchable},
			},
		},
	}

	if requests := planCatalogSearch(addons, "movie", CatalogSearchInput{Search: "query", Limit: 24}); len(requests) != 0 {
		t.Fatalf("planned requests for undeclared capabilities: %#v", requests)
	}
}

func TestExecuteRetriesOnlyTemporaryFailures(t *testing.T) {
	tests := []struct {
		name       string
		firstErr   error
		wantCalls  int
		wantResult bool
	}{
		{
			name:       "temporary network failure",
			firstErr:   unavailable("request failed", errors.New("connection reset"), true),
			wantCalls:  2,
			wantResult: true,
		},
		{
			name:      "permanent HTTP failure",
			firstErr:  unavailable("request failed", errorsText("HTTP 404"), false),
			wantCalls: 1,
		},
		{
			name:      "invalid response",
			firstErr:  fmt.Errorf("%w: catalog response requires \"metas\" array", ErrInvalidResponse),
			wantCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			transport := functionTransport{resource: func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
				calls++
				if calls == 1 {
					return nil, CachePolicy{}, test.firstErr
				}
				return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
			}}
			service := Service{
				transport:      transport,
				logger:         discardLogger(),
				requestTimeout: time.Second,
				retryDelay:     time.Millisecond,
			}

			batch := service.execute(context.Background(), []plannedRequest{executeTestRequest("search")})
			if calls != test.wantCalls {
				t.Fatalf("Resource calls = %d, want %d", calls, test.wantCalls)
			}
			if got := len(batch.Results) == 1; got != test.wantResult {
				t.Fatalf("successful result = %v, want %v; batch = %#v", got, test.wantResult, batch)
			}
		})
	}
}

func TestExecuteUsesIndependentRequestTimeouts(t *testing.T) {
	var mu sync.Mutex
	deadlines := 0
	transport := functionTransport{resource: func(ctx context.Context, _ string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
		if _, ok := ctx.Deadline(); ok {
			mu.Lock()
			deadlines++
			mu.Unlock()
		}
		if path.ID == "slow" {
			<-ctx.Done()
			return nil, CachePolicy{}, ctx.Err()
		}
		return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
	}}
	service := Service{
		transport:      transport,
		logger:         discardLogger(),
		requestTimeout: 20 * time.Millisecond,
		retryDelay:     time.Millisecond,
	}

	batch := service.execute(context.Background(), []plannedRequest{
		executeTestRequest("slow"),
		executeTestRequest("fast"),
	})
	if len(batch.Results) != 1 || batch.Results[0].ID != "fast" {
		t.Fatalf("fast result did not survive independent timeout: %#v", batch.Results)
	}
	if len(batch.Errors) != 1 || batch.Errors[0].Code != "addon_request_timeout" {
		t.Fatalf("unexpected timeout failure: %#v", batch.Errors)
	}
	mu.Lock()
	defer mu.Unlock()
	if deadlines != 2 {
		t.Fatalf("requests with a deadline = %d, want 2", deadlines)
	}
}

func TestExecuteLogsFailureIdentityAndCauseWithoutPayload(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	secretPayload := json.RawMessage(`{"secretPayload":"must-not-be-logged"}`)
	cause := unavailable("request failed", errors.New("dial tcp: connection refused"), true)
	transport := functionTransport{resource: func(_ context.Context, _ string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
		if path.ID == "healthy" {
			return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
		}
		return secretPayload, CachePolicy{}, cause
	}}
	service := Service{transport: transport, logger: logger, requestTimeout: time.Second, retryDelay: time.Millisecond}
	request := executeTestRequest("featured")

	batch := service.execute(context.Background(), []plannedRequest{request, executeTestRequest("healthy")})
	if len(batch.Results) != 1 || batch.Results[0].ID != "healthy" || len(batch.Errors) != 1 {
		t.Fatalf("unexpected batch: %#v", batch)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
		t.Fatalf("decode log: %v; log = %q", err, logs.String())
	}
	want := map[string]string{
		"addonId":      request.addon.ID,
		"manifestId":   request.addon.parsedManifest.ID,
		"transportUrl": request.addon.TransportURL,
		"resource":     request.path.Resource,
		"type":         request.path.Type,
		"resourceId":   request.path.ID,
	}
	for key, value := range want {
		if record[key] != value {
			t.Fatalf("log %s = %#v, want %q", key, record[key], value)
		}
	}
	if loggedCause, _ := record["error"].(string); !strings.Contains(loggedCause, "dial tcp: connection refused") {
		t.Fatalf("log cause = %#v", record["error"])
	}
	if strings.Contains(logs.String(), "must-not-be-logged") {
		t.Fatalf("failure log leaked response payload: %s", logs.String())
	}
}

func requestIDs(requests []plannedRequest) []string {
	ids := make([]string, len(requests))
	for index, request := range requests {
		ids[index] = request.path.ID
	}
	return ids
}
