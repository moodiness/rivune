package addon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/requestwork"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestExposableResourceAllowlist(t *testing.T) {
	for _, resource := range []string{"catalog", "addon_catalog", "meta"} {
		if !IsExposableResource(resource) {
			t.Fatalf("%q must remain exposable", resource)
		}
	}
	for _, resource := range []string{"stream", "subtitles", "custom-resource", "Meta", ""} {
		if IsExposableResource(resource) {
			t.Fatalf("%q must not be exposable", resource)
		}
	}
}

func TestResourceResultSerializationRemovesProviderTransportMaterial(t *testing.T) {
	result := ResourceResult{
		Resource: "catalog",
		Payload:  json.RawMessage(`{"metas":[{"id":"tt123","poster":"/api/v1/artwork/canonical","externalUrl":"https://provider.example/private?token=secret","behaviorHints":{"proxyHeaders":{"request":{"Authorization":"Bearer secret"}}},"extensions":{"cookie":"session=secret","apiKey":"provider-key","signature":"signed-secret","score":9007199254740993}}]}`),
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal catalog resource: %v", err)
	}
	for _, exposed := range []string{"provider.example", "externalUrl", "proxyHeaders", "Authorization", "cookie", "apiKey", "signature", "Bearer secret", "session=secret", "provider-key", "signed-secret"} {
		if strings.Contains(string(encoded), exposed) {
			t.Fatalf("resource result exposed %q: %s", exposed, encoded)
		}
	}
	for _, preserved := range []string{"/api/v1/artwork/canonical", "9007199254740993", `"id":"tt123"`} {
		if !strings.Contains(string(encoded), preserved) {
			t.Fatalf("resource result removed safe value %q: %s", preserved, encoded)
		}
	}
}

func TestResourceResultSerializationOnlyPreservesLocalizedVideoThumbnails(t *testing.T) {
	result := ResourceResult{
		Resource: "meta",
		Payload:  json.RawMessage(`{"meta":{"id":"series-id","videos":[{"id":"external-video","thumbnail":"https://provider.example/episode.webp?token=secret","thumbnailUrl":"https://provider.example/episode-alt.webp?token=secret"},{"id":"localized-video","thumbnail":"/api/v1/artwork/thumbnail","thumbnailUrl":"/api/v1/artwork/thumbnail-url"}]}}`),
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal meta resource: %v", err)
	}
	for _, exposed := range []string{"provider.example", "episode.webp", "episode-alt.webp", "token=secret"} {
		if strings.Contains(string(encoded), exposed) {
			t.Fatalf("resource result exposed unlocalized thumbnail value %q: %s", exposed, encoded)
		}
	}

	var serialized struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(encoded, &serialized); err != nil {
		t.Fatalf("decode serialized resource result: %v", err)
	}
	var envelope struct {
		Meta struct {
			Videos []map[string]any `json:"videos"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(serialized.Payload, &envelope); err != nil || len(envelope.Meta.Videos) != 2 {
		t.Fatalf("decode sanitized video metadata: %v payload=%s", err, serialized.Payload)
	}
	if _, exists := envelope.Meta.Videos[0]["thumbnail"]; exists {
		t.Fatalf("unlocalized thumbnail survived sanitization: %#v", envelope.Meta.Videos[0])
	}
	if _, exists := envelope.Meta.Videos[0]["thumbnailUrl"]; exists {
		t.Fatalf("unlocalized thumbnailUrl survived sanitization: %#v", envelope.Meta.Videos[0])
	}
	if got := envelope.Meta.Videos[1]["thumbnail"]; got != "/api/v1/artwork/thumbnail" {
		t.Fatalf("localized thumbnail = %#v", got)
	}
	if got := envelope.Meta.Videos[1]["thumbnailUrl"]; got != "/api/v1/artwork/thumbnail-url" {
		t.Fatalf("localized thumbnailUrl = %#v", got)
	}
}

func TestResourceResultSerializationCannotExposePlaybackPayload(t *testing.T) {
	for _, resource := range []string{"stream", "subtitles"} {
		result := ResourceResult{
			Resource: resource,
			Payload:  json.RawMessage(`{"streams":[{"url":"https://provider.example/private?token=secret","behaviorHints":{"proxyHeaders":{"request":{"Authorization":"Bearer secret"}}}}]}`),
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal %s resource: %v", resource, err)
		}
		if !strings.Contains(string(encoded), `"payload":null`) {
			t.Fatalf("%s resource payload was not redacted: %s", resource, encoded)
		}
		for _, exposed := range []string{"provider.example", "token", "proxyHeaders", "Authorization", "Bearer secret"} {
			if strings.Contains(string(encoded), exposed) {
				t.Fatalf("%s resource exposed %q: %s", resource, exposed, encoded)
			}
		}
	}
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
		{name: "catalog required extra satisfied", path: ResourcePath{Resource: "catalog", Type: "anime-special", ID: "custom/catalog", Extra: []ExtraValue{{Name: "search", Value: "fixture"}, {Name: "genre", Value: "Drama"}, {Name: "genre", Value: "Sci-Fi"}}}, want: true},
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
	var propagatedRequestID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURI = r.RequestURI
		propagatedRequestID = r.Header.Get(requestwork.RequestIDHeader)
		w.Header().Set("Cache-Control", "max-age=300, stale-while-revalidate=60")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"streams":[{"infoHash":"ABCDEF","fileIdx":7,"behaviorHints":{"bingeGroup":"custom-group","proxyHeaders":{"request":{"Authorization":"secret"}},"futureHint":true}}],"futureResponseField":{"kept":true},"staleError":900}`))
	}))
	defer server.Close()

	transport := NewHTTPTransport(server.Client())
	requestContext, requestID := requestwork.WithRequestID(context.Background(), "transport-correlation-4")
	payload, cache, err := transport.Resource(requestContext, server.URL+"/configured/token/manifest.json?key=value", ResourcePath{
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
	if propagatedRequestID != requestID {
		t.Fatalf("propagated request ID = %q, want %q", propagatedRequestID, requestID)
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
			if errors.Is(err, ErrProviderUnavailable) && !strings.Contains(err.Error(), "HTTP "+strconv.Itoa(test.status)) {
				t.Fatalf("provider error did not preserve HTTP status %d: %v", test.status, err)
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

	_, _, err := transport.Resource(context.Background(), "https://addon.example/manifest.json?token=network-secret", ResourcePath{
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
	if strings.Contains(err.Error(), "network-secret") || strings.Contains(err.Error(), "addon.example") {
		t.Fatalf("network error exposed request URL: %v", err)
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

func TestManifestRejectsExcessiveCardinalityAndComplexity(t *testing.T) {
	base := Manifest{
		ID: "org.example.bounded", Version: "1.0.0", Name: "Bounded",
		Types: []string{"movie"}, Resources: []ManifestResource{{Name: "catalog", Short: true}},
	}
	tooManyCatalogs := base
	tooManyCatalogs.Catalogs = make([]ManifestCatalog, maxManifestCatalogs+1)
	if err := tooManyCatalogs.Validate(); !errors.Is(err, ErrInvalidManifest) || !strings.Contains(err.Error(), "catalogs exceeds limit") {
		t.Fatalf("catalog cardinality error = %v", err)
	}

	tooComplex := base
	extras := make([]ExtraProp, 17)
	for index := range extras {
		extras[index] = ExtraProp{
			Name:    "extra",
			Options: make([]string, maxManifestListEntries),
		}
	}
	tooComplex.Catalogs = []ManifestCatalog{{Type: "movie", ID: "catalog", Extra: extras}}
	if err := tooComplex.Validate(); !errors.Is(err, ErrInvalidManifest) || !strings.Contains(err.Error(), "manifest complexity exceeds limit") {
		t.Fatalf("manifest complexity error = %v", err)
	}
}

func TestInstalledAddonManifestMarshalsAsObjectWithoutTransport(t *testing.T) {
	const secret = "https://provider.example/private/path/manifest.json?token=must-not-leak"
	encoded, err := json.Marshal(InstalledAddon{
		Manifest:     json.RawMessage(`{"id":"org.example"}`),
		ProfileIDs:   []string{"11111111-1111-4111-8111-111111111111"},
		CategoryIDs:  []string{"22222222-2222-4222-8222-222222222222"},
		transportURL: secret,
	})
	if err != nil {
		t.Fatalf("marshal installed addon: %v", err)
	}
	if !strings.Contains(string(encoded), `"manifest":{"id":"org.example"}`) {
		t.Fatalf("manifest was not embedded as JSON: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"enabled":false`) {
		t.Fatalf("installed addon availability was omitted: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"profileIds":["11111111-1111-4111-8111-111111111111"]`) ||
		!strings.Contains(string(encoded), `"categoryIds":["22222222-2222-4222-8222-222222222222"]`) {
		t.Fatalf("installed addon did not preserve separate assignment arrays: %s", encoded)
	}
	if strings.Contains(string(encoded), "transportUrl") || strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "must-not-leak") {
		t.Fatalf("installed addon exposed internal transport: %s", encoded)
	}
}

func TestManagedAddonTransportDisclosureIsExplicit(t *testing.T) {
	const secret = "https://provider.example/private/path/manifest.json?token=management-secret"
	installed := InstalledAddon{
		ID:           "11111111-1111-4111-8111-111111111111",
		Manifest:     json.RawMessage(`{"id":"org.example"}`),
		transportURL: secret,
	}
	revealed := managedAddon(installed, true)
	if revealed.TransportURL != secret {
		t.Fatal("managed addon did not preserve the stored transport URL")
	}
	redacted, err := json.Marshal(managedAddon(installed, false))
	if err != nil {
		t.Fatalf("marshal redacted managed addon: %v", err)
	}
	if strings.Contains(string(redacted), "transportUrl") || strings.Contains(string(redacted), "management-secret") {
		t.Fatal("redacted managed addon exposed its transport URL")
	}
}
func TestUpdateAddonInputAvailabilitySerialization(t *testing.T) {
	disabled := false
	update, err := json.Marshal(UpdateAddonInput{Enabled: &disabled, ProfileIDs: []string{}})
	if err != nil {
		t.Fatalf("marshal addon availability update: %v", err)
	}
	if !strings.Contains(string(update), `"enabled":false`) {
		t.Fatalf("addon availability update omitted false: %s", update)
	}
	omitted, err := json.Marshal(UpdateAddonInput{ProfileIDs: []string{}})
	if err != nil {
		t.Fatalf("marshal addon update without availability: %v", err)
	}
	if strings.Contains(string(omitted), `"enabled"`) {
		t.Fatalf("addon update serialized omitted availability: %s", omitted)
	}
}

func TestNormalizeInstallAssignmentsKeepsDimensionsSeparate(t *testing.T) {
	const activeProfileID = "11111111-1111-4111-8111-111111111111"
	defaulted, err := normalizeInstallAssignments(nil, nil, activeProfileID)
	if err != nil || !reflect.DeepEqual(defaulted.profileIDs, []string{activeProfileID}) || len(defaulted.categoryIDs) != 0 {
		t.Fatalf("default assignments = %+v, error %v", defaulted, err)
	}
	categoryOnly, err := normalizeInstallAssignments(
		[]string{}, []string{"22222222-2222-4222-8222-222222222222"}, activeProfileID,
	)
	if err != nil || len(categoryOnly.profileIDs) != 0 || !reflect.DeepEqual(categoryOnly.categoryIDs, []string{"22222222-2222-4222-8222-222222222222"}) {
		t.Fatalf("category-only assignments = %+v, error %v", categoryOnly, err)
	}
	if _, err := normalizeInstallAssignments([]string{}, []string{}, activeProfileID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty assignment union error = %v, want %v", err, ErrInvalidInput)
	}
	if _, err := normalizeInstallAssignments(
		[]string{activeProfileID, activeProfileID}, nil, activeProfileID,
	); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate profile assignment error = %v, want %v", err, ErrInvalidInput)
	}
	if _, err := normalizeInstallAssignments(nil, []string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
	}, activeProfileID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("mixed-case duplicate category assignment error = %v, want %v", err, ErrInvalidInput)
	}
}
