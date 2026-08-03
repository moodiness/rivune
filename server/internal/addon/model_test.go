package addon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestManifestSupportsOpaqueTypesIDsAndCatalogExtras(t *testing.T) {
	raw := []byte(`{
		"id":"org.example.complete",
		"version":"1.2.3-beta.1+build.7",
		"name":"Complete",
		"types":["movie","anime-special","all"],
		"idPrefixes":["tt","global:"],
		"resources":[
			"catalog",
			"addon_catalog",
			"meta",
			{"name":"stream","types":["anime-special"],"idPrefixes":["kitsu:","addon/custom:"]},
			{"name":"subtitles","types":["anime-special"],"idPrefixes":[]},
			{"name":"custom-resource","types":["anime-special"]}
		],
		"catalogs":[
			{"type":"anime-special","id":"custom/catalog","extra":[
				{"name":"search","isRequired":true},
				{"name":"genre","options":["Drama","Sci-Fi"],"optionsLimit":2}
			]},
			{"type":"movie","id":"legacy","extraRequired":["search"],"extraSupported":["search","skip"]},
			{"type":"movie","id":"defaults","extra":[{"name":"genre","isRequired":true,"default":"None"},{"name":"skip"}]}
		],
		"addonCatalogs":[{"type":"all","id":"community"}],
		"behaviorHints":{"p2p":true},
		"futureManifestField":{"kept":true}
	}`)
	manifest, preserved, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if !strings.Contains(string(preserved), `"futureManifestField":{"kept":true}`) {
		t.Fatalf("unknown manifest field was not preserved: %s", preserved)
	}

	tests := []struct {
		name string
		path ResourcePath
		want bool
	}{
		{name: "short resource global type and prefix", path: ResourcePath{Resource: "meta", Type: "movie", ID: "tt123"}, want: true},
		{name: "short resource rejects custom unmatched prefix", path: ResourcePath{Resource: "meta", Type: "movie", ID: "kitsu:1"}, want: false},
		{name: "full resource accepts custom slash ID prefix", path: ResourcePath{Resource: "stream", Type: "anime-special", ID: "addon/custom:item/episode:2"}, want: true},
		{name: "full resource rejects type", path: ResourcePath{Resource: "stream", Type: "movie", ID: "kitsu:1"}, want: false},
		{name: "empty prefixes accept every ID", path: ResourcePath{Resource: "subtitles", Type: "anime-special", ID: "anything/at-all"}, want: true},
		{name: "full omitted prefixes accept every ID", path: ResourcePath{Resource: "custom-resource", Type: "anime-special", ID: "opaque:value"}, want: true},
		{name: "catalog required extra satisfied", path: ResourcePath{Resource: "catalog", Type: "anime-special", ID: "custom/catalog", Extra: []ExtraValue{{Name: "search", Value: "Frieren"}, {Name: "genre", Value: "Drama"}, {Name: "genre", Value: "Sci-Fi"}}}, want: true},
		{name: "catalog missing required extra", path: ResourcePath{Resource: "catalog", Type: "anime-special", ID: "custom/catalog"}, want: false},
		{name: "catalog rejects undeclared extra", path: ResourcePath{Resource: "catalog", Type: "anime-special", ID: "custom/catalog", Extra: []ExtraValue{{Name: "search", Value: "x"}, {Name: "unknown", Value: "x"}}}, want: false},
		{name: "legacy catalog extras", path: ResourcePath{Resource: "catalog", Type: "movie", ID: "legacy", Extra: []ExtraValue{{Name: "search", Value: "x"}, {Name: "skip", Value: "100"}}}, want: true},
		{name: "addon catalog", path: ResourcePath{Resource: "addon_catalog", Type: "all", ID: "community"}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := manifest.Supports(test.path); got != test.want {
				t.Fatalf("Supports() = %v, want %v", got, test.want)
			}
		})
	}
}
func TestManifestCatalogSupportRequiresDeclaredTypeAndResourceCapability(t *testing.T) {
	movieTypes := []string{"movie"}
	seriesTypes := []string{"series"}
	catalog := ManifestCatalog{Type: "movie", ID: "search"}
	path := ResourcePath{Resource: "catalog", Type: "movie", ID: "search"}
	tests := []struct {
		name     string
		manifest Manifest
		want     bool
	}{
		{
			name: "short catalog resource",
			manifest: Manifest{
				Types:     movieTypes,
				Resources: []ManifestResource{{Name: "catalog", Short: true}},
				Catalogs:  []ManifestCatalog{catalog},
			},
			want: true,
		},
		{
			name: "catalog type absent from manifest",
			manifest: Manifest{
				Types:     seriesTypes,
				Resources: []ManifestResource{{Name: "catalog", Short: true}},
				Catalogs:  []ManifestCatalog{catalog},
			},
		},
		{
			name: "catalog resource absent",
			manifest: Manifest{
				Types:     movieTypes,
				Resources: []ManifestResource{{Name: "meta", Short: true}},
				Catalogs:  []ManifestCatalog{catalog},
			},
		},
		{
			name: "full catalog resource excludes type",
			manifest: Manifest{
				Types:     movieTypes,
				Resources: []ManifestResource{{Name: "catalog", Types: &seriesTypes}},
				Catalogs:  []ManifestCatalog{catalog},
			},
		},
		{
			name: "matching catalog capability may follow another type",
			manifest: Manifest{
				Types: movieTypes,
				Resources: []ManifestResource{
					{Name: "catalog", Types: &seriesTypes},
					{Name: "catalog", Types: &movieTypes},
				},
				Catalogs: []ManifestCatalog{catalog},
			},
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.manifest.Supports(path); got != test.want {
				t.Fatalf("Supports() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestManifestAppliesRequiredCatalogDefaults(t *testing.T) {
	manifest, _, err := ParseManifest([]byte(`{
		"id":"org.example.defaults",
		"version":"1.0.0",
		"name":"Defaults",
		"types":["movie"],
		"resources":["catalog"],
		"catalogs":[{"type":"movie","id":"popular","extra":[
			{"name":"genre","isRequired":true,"default":"None"},
			{"name":"skip"}
		]}]
	}`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	path := manifest.ApplyCatalogDefaults(ResourcePath{Resource: "catalog", Type: "movie", ID: "popular"})
	if !manifest.Supports(path) {
		t.Fatal("catalog path with its declared default should be supported")
	}
	if len(path.Extra) != 1 || path.Extra[0] != (ExtraValue{Name: "genre", Value: "None"}) {
		t.Fatalf("unexpected catalog defaults: %#v", path.Extra)
	}

	explicit := manifest.ApplyCatalogDefaults(ResourcePath{
		Resource: "catalog", Type: "movie", ID: "popular",
		Extra: []ExtraValue{{Name: "genre", Value: "Drama"}},
	})
	if len(explicit.Extra) != 1 || explicit.Extra[0].Value != "Drama" {
		t.Fatalf("explicit extra was overwritten: %#v", explicit.Extra)
	}
}

func TestCatalogSearchSupportNormalizesModernAndLegacyExtras(t *testing.T) {
	tests := []struct {
		name    string
		catalog ManifestCatalog
		want    bool
	}{
		{name: "modern", catalog: ManifestCatalog{Extra: []ExtraProp{{Name: "search"}}}, want: true},
		{name: "legacy", catalog: ManifestCatalog{ExtraSupported: []string{"search"}}, want: true},
		{name: "not searchable", catalog: ManifestCatalog{Extra: []ExtraProp{{Name: "skip"}}}},
		{
			name: "modern form takes precedence",
			catalog: ManifestCatalog{
				Extra:          []ExtraProp{},
				ExtraSupported: []string{"search"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.catalog.SupportsSearch(); got != test.want {
				t.Fatalf("SupportsSearch() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNormalizeTransportURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "https://addon.example", want: "https://addon.example/manifest.json"},
		{input: "https://addon.example/config-token/manifest.json", want: "https://addon.example/config-token/manifest.json"},
		{input: "stremio://addon.example/user/config", want: "https://addon.example/user/config/manifest.json"},
		{input: "http://192.168.1.20:7000/", want: "http://192.168.1.20:7000/manifest.json"},
	}
	for _, test := range tests {
		got, err := NormalizeTransportURL(test.input)
		if err != nil {
			t.Fatalf("NormalizeTransportURL(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("NormalizeTransportURL(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	for _, input := range []string{"file:///tmp/manifest.json", "https://user:secret@addon.example/manifest.json", "https://addon.example/manifest.json#fragment", "https://addon.example/stremio/v1"} {
		if _, err := NormalizeTransportURL(input); !errors.Is(err, ErrInvalidTransportURL) {
			t.Fatalf("expected invalid transport for %q, got %v", input, err)
		}
	}
}

func TestHTTPTransportEncodesOpaqueResourceAndPreservesPayload(t *testing.T) {
	var requestedURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURI = r.RequestURI
		w.Header().Set("Cache-Control", "max-age=300, stale-while-revalidate=60")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"streams":[{"infoHash":"ABCDEF","fileIdx":7,"behaviorHints":{"bingeGroup":"custom-group","proxyHeaders":{"request":{"Authorization":"secret"}},"futureHint":true}}],"futureResponseField":{"kept":true},"staleError":900}`))
	}))
	defer server.Close()

	transport := NewHTTPTransport(server.Client())
	payload, cache, err := transport.Resource(context.Background(), server.URL+"/configured/token/manifest.json?key=value", ResourcePath{
		Resource: "stream",
		Type:     "anime-special",
		ID:       "kitsu:anime/42 ?",
		Extra: []ExtraValue{
			{Name: "genre", Value: "Sci Fi"},
			{Name: "genre", Value: "Drama/Action"},
		},
	})
	if err != nil {
		t.Fatalf("fetch resource: %v", err)
	}
	wantURI := "/configured/token/stream/anime-special/kitsu%3Aanime%2F42%20%3F/genre=Sci%20Fi&genre=Drama%2FAction.json?key=value"
	if requestedURI != wantURI {
		t.Fatalf("request URI = %q, want %q", requestedURI, wantURI)
	}
	if !strings.Contains(string(payload), `"futureHint":true`) || !strings.Contains(string(payload), `"futureResponseField":{"kept":true}`) {
		t.Fatalf("response fields were not preserved: %s", payload)
	}
	if cache.MaxAgeSeconds == nil || *cache.MaxAgeSeconds != 300 || cache.StaleWhileRevalidateSeconds == nil || *cache.StaleWhileRevalidateSeconds != 60 || cache.StaleIfErrorSeconds == nil || *cache.StaleIfErrorSeconds != 900 {
		t.Fatalf("unexpected cache policy: %+v", cache)
	}
}

func TestHTTPTransportRejectsInvalidResponsesAndClassifiesProviderErrors(t *testing.T) {
	tests := []struct {
		name           string
		resource       string
		status         int
		body           string
		want           error
		checkTemporary bool
		wantTemporary  bool
	}{
		{name: "non object JSON", resource: "meta", status: http.StatusOK, body: `[]`, want: ErrInvalidResponse},
		{name: "invalid JSON", resource: "meta", status: http.StatusOK, body: `{`, want: ErrInvalidResponse},
		{name: "missing meta", resource: "meta", status: http.StatusOK, body: `{}`, want: ErrInvalidResponse},
		{name: "null meta", resource: "meta", status: http.StatusOK, body: `{"meta":null}`, want: ErrInvalidResponse},
		{name: "catalog metas must be array", resource: "catalog", status: http.StatusOK, body: `{"metas":{}}`, want: ErrInvalidResponse},
		{name: "stream requires streams", resource: "stream", status: http.StatusOK, body: `{}`, want: ErrInvalidResponse},
		{name: "subtitles requires subtitles", resource: "subtitles", status: http.StatusOK, body: `{}`, want: ErrInvalidResponse},
		{name: "rate limit is temporary", resource: "meta", status: http.StatusTooManyRequests, body: `{}`, want: ErrProviderUnavailable, checkTemporary: true, wantTemporary: true},
		{name: "server failure is temporary", resource: "meta", status: http.StatusBadGateway, body: `{}`, want: ErrProviderUnavailable, checkTemporary: true, wantTemporary: true},
		{name: "client failure is permanent", resource: "meta", status: http.StatusNotFound, body: `{}`, want: ErrProviderUnavailable, checkTemporary: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			transport := NewHTTPTransport(server.Client())
			_, _, err := transport.Resource(context.Background(), server.URL+"/manifest.json", ResourcePath{Resource: test.resource, Type: "custom", ID: "id"})
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
			if test.checkTemporary {
				if got := isTemporaryProviderError(err); got != test.wantTemporary {
					t.Fatalf("temporary = %v, want %v", got, test.wantTemporary)
				}
			}
		})
	}
}

func TestHTTPTransportPreservesNetworkCause(t *testing.T) {
	networkCause := errors.New("dial tcp: connection refused")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, networkCause
	})}
	transport := NewHTTPTransport(client)

	_, _, err := transport.Resource(context.Background(), "https://addon.example/manifest.json", ResourcePath{
		Resource: "meta",
		Type:     "movie",
		ID:       "tt123",
	})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("expected provider unavailable, got %v", err)
	}
	if !errors.Is(err, networkCause) {
		t.Fatalf("network cause was not preserved: %v", err)
	}
	if !isTemporaryProviderError(err) {
		t.Fatalf("network error is not classified temporary: %v", err)
	}
}

func TestParseManifestRejectsInvalidRequiredFields(t *testing.T) {
	for _, raw := range []string{
		`{"id":"x","version":"not-semver","name":"x","types":["movie"],"resources":["meta"],"catalogs":[]}`,
		`{"id":"x","version":"1.0.0","name":"x","types":["movie"],"resources":[],"catalogs":[]}`,
		`{"id":"x","version":"1.0.0","name":"x","types":["movie"],"resources":["meta"],"catalogs":[{"type":"movie","id":"same"},{"type":"movie","id":"same"}]}`,
	} {
		if _, _, err := ParseManifest([]byte(raw)); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("expected invalid manifest for %s, got %v", raw, err)
		}
	}
}

func TestInstalledAddonManifestMarshalsAsObject(t *testing.T) {
	encoded, err := json.Marshal(InstalledAddon{Manifest: json.RawMessage(`{"id":"org.example"}`)})
	if err != nil {
		t.Fatalf("marshal installed addon: %v", err)
	}
	if !strings.Contains(string(encoded), `"manifest":{"id":"org.example"}`) {
		t.Fatalf("manifest was not embedded as JSON: %s", encoded)
	}
}
