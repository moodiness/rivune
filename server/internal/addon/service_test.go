package addon

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
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

func TestPlanCatalogSearchAdaptsEachTVCatalog(t *testing.T) {
	manifest := Manifest{
		ID:    "org.example.tv",
		Types: []string{"movie", "tv"},
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
		ID:       "org.example.partial-tv",
		Types:    []string{"movie"},
		Catalogs: []ManifestCatalog{{Type: "tv", ID: "partial", ExtraSupported: []string{"search", "skip"}}},
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
		ID:    "org.example.tv",
		Types: []string{"tv"},
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
	service := Service{transport: transport}
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

func requestIDs(requests []plannedRequest) []string {
	ids := make([]string, len(requests))
	for index, request := range requests {
		ids[index] = request.path.ID
	}
	return ids
}
