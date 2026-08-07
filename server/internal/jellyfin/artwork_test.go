package jellyfin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	artworkTokenOne       = "rivune_jf_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	artworkTokenTwo       = "rivune_jf_AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
	artworkProfileOne     = "10000000-0000-4000-8000-000000000001"
	artworkProfileTwo     = "20000000-0000-4000-8000-000000000002"
	artworkItemOne        = "30000000-0000-4000-8000-000000000003"
	artworkItemStale      = "40000000-0000-4000-8000-000000000004"
	artworkItemInvalidKey = "50000000-0000-4000-8000-000000000005"
)

type artworkAuthentication struct {
	sessions map[string]AuthenticatedSession
}

func (*artworkAuthentication) Login(context.Context, CompatLoginInput) (LoginResult, error) {
	return LoginResult{}, errors.New("login is not used by artwork handlers")
}

func (authentication *artworkAuthentication) Authenticate(_ context.Context, token string) (AuthenticatedSession, error) {
	session, ok := authentication.sessions[token]
	if !ok {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return session, nil
}

func (*artworkAuthentication) Logout(context.Context, AuthenticatedSession) error {
	return errors.New("logout is not used by artwork handlers")
}

type artworkCatalog struct {
	items map[string]map[string]watchstate.CatalogTitle
	calls int
}

func (catalog *artworkCatalog) GetCatalogTitle(_ context.Context, principal auth.Principal, itemID string) (watchstate.CatalogTitle, error) {
	catalog.calls++
	if principal.ActiveProfileID == nil {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	item, ok := catalog.items[*principal.ActiveProfileID][itemID]
	if !ok {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	return item, nil
}

func (*artworkCatalog) ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return watchstate.CatalogPage{}, errors.New("list is not used by artwork handlers")
}

type artworkDelivery struct {
	keys       map[string]string
	body       []byte
	lookup     []string
	servedKeys []string
}

func (delivery *artworkDelivery) LookupKey(_ context.Context, materialized string) (string, bool) {
	delivery.lookup = append(delivery.lookup, materialized)
	key, ok := delivery.keys[materialized]
	return key, ok
}

func (delivery *artworkDelivery) ServeKey(response http.ResponseWriter, request *http.Request, key string) {
	delivery.servedKeys = append(delivery.servedKeys, key)
	response.Header().Set("Content-Type", "image/png")
	response.Header().Set("Content-Length", "8")
	response.Header().Set("ETag", `"`+key+`"`)
	response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = response.Write(delivery.body)
	}
}

func TestArtworkGETHEADSelectRegisteredPrimaryAndBackdrop(t *testing.T) {
	poster := "https://provider.invalid/private/poster.png?token=secret"
	background := "/api/v1/artwork/" + strings.Repeat("b", 64)
	posterKey := strings.Repeat("a", 64)
	backgroundKey := strings.Repeat("b", 64)
	handler, catalog, delivery := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {
			artworkItemOne: {ID: artworkItemOne, MediaType: "episode", PosterURL: poster, BackgroundURL: background},
		},
	}, map[string]string{poster: posterKey, background: backgroundKey})

	for _, test := range []struct {
		name       string
		method     string
		imageType  string
		indexed    bool
		wantKey    string
		wantSource string
		wantBody   bool
	}{
		{name: "primary get", method: http.MethodGet, imageType: "Primary", wantKey: posterKey, wantSource: poster, wantBody: true},
		{name: "thumb get", method: http.MethodGet, imageType: "Thumb", wantKey: posterKey, wantSource: poster, wantBody: true},
		{name: "primary head", method: http.MethodHead, imageType: "Primary", indexed: true, wantKey: posterKey, wantSource: poster},
		{name: "backdrop get", method: http.MethodGet, imageType: "Backdrop", indexed: true, wantKey: backgroundKey, wantSource: background, wantBody: true},
		{name: "backdrop head", method: http.MethodHead, imageType: "Backdrop", wantKey: backgroundKey, wantSource: background},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := "/Items/" + artworkItemOne + "/Images/" + test.imageType
			if test.indexed {
				path += "/0"
			}
			request := httptest.NewRequest(test.method, path+"?api_key="+artworkTokenOne+"&maxWidth=600&maxHeight=900&quality=90", nil)
			request.SetPathValue("id", artworkItemOne)
			request.SetPathValue("type", test.imageType)
			if test.indexed {
				request.SetPathValue("index", "0")
			}
			response := httptest.NewRecorder()
			if test.indexed {
				handler.handleIndexedImage(response, request)
			} else {
				handler.handleImage(response, request)
			}
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" ||
				response.Header().Get("Content-Length") != "8" || response.Header().Get("ETag") != `"`+test.wantKey+`"` ||
				response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || response.Header().Get("Location") != "" {
				t.Fatalf("response status=%d headers=%v", response.Code, response.Header())
			}
			if gotBody := response.Body.String(); test.wantBody && gotBody != "pngbytes" || !test.wantBody && gotBody != "" {
				t.Fatalf("response body = %q, wantBody=%t", gotBody, test.wantBody)
			}
			if got := delivery.lookup[len(delivery.lookup)-1]; got != test.wantSource {
				t.Fatalf("looked up %q, want %q", got, test.wantSource)
			}
			if got := delivery.servedKeys[len(delivery.servedKeys)-1]; got != test.wantKey {
				t.Fatalf("served key %q, want %q", got, test.wantKey)
			}
		})
	}
	if catalog.calls != 5 {
		t.Fatalf("catalog calls = %d", catalog.calls)
	}
}

func TestArtworkServesObservedVidHubTagCapabilityWithoutToken(t *testing.T) {
	key := strings.Repeat("d", 64)
	handler, catalog, delivery := newArtworkHandler(t, nil, nil)
	request := httptest.NewRequest(
		http.MethodGet,
		"/Items/"+artworkItemOne+"/Images/Primary?tag="+key+"&maxWidth=500&quality=90",
		nil,
	)
	request.SetPathValue("id", artworkItemOne)
	request.SetPathValue("type", "Primary")
	response := httptest.NewRecorder()
	handler.handleImage(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"`+key+`"` || response.Body.String() != "pngbytes" {
		t.Fatalf("anonymous tag response=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	if catalog.calls != 0 || len(delivery.lookup) != 0 || len(delivery.servedKeys) != 1 || delivery.servedKeys[0] != key {
		t.Fatalf("anonymous tag catalog=%d lookup=%v served=%v", catalog.calls, delivery.lookup, delivery.servedKeys)
	}
	authenticated := httptest.NewRequest(
		http.MethodGet,
		"/Items/"+artworkItemOne+"/Images/Primary?tag="+key+"&api_key="+artworkTokenOne,
		nil,
	)
	authenticated.SetPathValue("id", artworkItemOne)
	authenticated.SetPathValue("type", "Primary")
	authenticatedResponse := httptest.NewRecorder()
	handler.handleImage(authenticatedResponse, authenticated)
	if authenticatedResponse.Code != http.StatusOK || catalog.calls != 0 || len(delivery.lookup) != 0 || len(delivery.servedKeys) != 2 || delivery.servedKeys[1] != key {
		t.Fatalf("authenticated tag response=%d catalog=%d lookup=%v served=%v", authenticatedResponse.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}

	for _, test := range []struct {
		name        string
		target      string
		headerName  string
		headerValue string
	}{
		{name: "invalid api key", target: "?tag=" + key + "&api_key=rivune_at_native"},
		{name: "malformed api key", target: "?tag=" + key + "&api_key=%ZZ"},
		{name: "unsupported authorization", target: "?tag=" + key, headerName: "Authorization", headerValue: "Bearer invalid"},
		{name: "empty authorization token", target: "?tag=" + key, headerName: "X-Emby-Authorization", headerValue: `MediaBrowser Token=""`},
		{name: "empty token header", target: "?tag=" + key, headerName: "X-Emby-Token", headerValue: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalidCredential := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images/Primary"+test.target, nil)
			invalidCredential.SetPathValue("id", artworkItemOne)
			invalidCredential.SetPathValue("type", "Primary")
			if test.headerName != "" {
				invalidCredential.Header.Set(test.headerName, test.headerValue)
			}
			invalidResponse := httptest.NewRecorder()
			handler.handleImage(invalidResponse, invalidCredential)
			if invalidResponse.Code != http.StatusUnauthorized || len(delivery.servedKeys) != 2 {
				t.Fatalf("invalid credential fallback status=%d served=%v", invalidResponse.Code, delivery.servedKeys)
			}
		})
	}
}

func TestArtworkRejectsInvalidSelectorsAndUnregisteredSources(t *testing.T) {
	poster := "https://provider.invalid/private/poster.png?token=secret"
	handler, catalog, delivery := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {
			artworkItemOne:        {ID: artworkItemOne, MediaType: "movie", PosterURL: poster},
			artworkItemStale:      {ID: artworkItemStale, MediaType: "season", PosterURL: "/api/v1/artwork/" + strings.Repeat("c", 64)},
			artworkItemInvalidKey: {ID: artworkItemInvalidKey, MediaType: "series", PosterURL: "/api/v1/artwork/not-a-key"},
		},
	}, nil)

	tests := []struct {
		name      string
		itemID    string
		imageType string
		index     string
		token     string
		header    string
		profile   string
	}{
		{name: "unregistered key", itemID: artworkItemOne, imageType: "Primary", token: artworkTokenOne},
		{name: "stale local key", itemID: artworkItemStale, imageType: "Primary", token: artworkTokenOne},
		{name: "invalid local key", itemID: artworkItemInvalidKey, imageType: "Primary", token: artworkTokenOne},
		{name: "unknown item", itemID: "60000000-0000-4000-8000-000000000006", imageType: "Primary", token: artworkTokenOne},
		{name: "cross profile item", itemID: artworkItemOne, imageType: "Primary", token: artworkTokenTwo},
		{name: "unsupported type", itemID: artworkItemOne, imageType: "Logo", token: artworkTokenOne},
		{name: "negative index", itemID: artworkItemOne, imageType: "Primary", index: "-1", token: artworkTokenOne},
		{name: "out of range index", itemID: artworkItemOne, imageType: "Backdrop", index: "1", token: artworkTokenOne},
		{name: "non decimal index", itemID: artworkItemOne, imageType: "Primary", index: "00", token: artworkTokenOne},
		{name: "native token", itemID: artworkItemOne, imageType: "Primary", token: "rivune_at_native"},
		{name: "different transports", itemID: artworkItemOne, imageType: "Primary", token: artworkTokenOne, header: artworkTokenTwo},
		{name: "invalid profile selector", itemID: artworkItemOne, imageType: "Primary", token: artworkTokenOne, profile: artworkProfileTwo},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			indexed := test.index != ""
			path := "/Items/" + test.itemID + "/Images/" + test.imageType
			if indexed {
				path += "/" + test.index
			}
			if test.token != "" {
				path += "?api_key=" + test.token
			}
			if test.profile != "" {
				path += "&UserId=" + test.profile
			}
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.SetPathValue("id", test.itemID)
			request.SetPathValue("type", test.imageType)
			request.SetPathValue("index", test.index)
			if test.header != "" {
				request.Header.Set("X-Emby-Token", test.header)
			}
			response := httptest.NewRecorder()
			if indexed {
				handler.handleIndexedImage(response, request)
			} else {
				handler.handleImage(response, request)
			}
			want := http.StatusNotFound
			if test.name == "native token" || test.name == "different transports" {
				want = http.StatusUnauthorized
			}
			challenge := response.Header().Get("WWW-Authenticate")
			if want == http.StatusUnauthorized && challenge != "MediaBrowser" || want != http.StatusUnauthorized && challenge != "" {
				t.Fatalf("response challenge = %q for status %d", challenge, want)
			}
			if response.Code != want || response.Header().Get("Location") != "" || strings.Contains(response.Body.String(), "provider.invalid") || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("response = %d %q headers=%v", response.Code, response.Body.String(), response.Header())
			}
		})
	}
	if len(delivery.servedKeys) != 0 {
		t.Fatalf("invalid requests served keys: %v", delivery.servedKeys)
	}
	if catalog.calls == 0 {
		t.Fatal("authorized item cases did not use CatalogReader")
	}
}

func TestArtworkRejectsDuplicateQueryTokenBeforeCatalog(t *testing.T) {
	handler, catalog, delivery := newArtworkHandler(t, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images/Primary?api_key="+artworkTokenOne+"&api_key="+artworkTokenOne, nil)
	request.SetPathValue("id", artworkItemOne)
	request.SetPathValue("type", "Primary")
	response := httptest.NewRecorder()
	handler.handleImage(response, request)
	if response.Code != http.StatusUnauthorized || catalog.calls != 0 || len(delivery.lookup) != 0 || len(delivery.servedKeys) != 0 {
		t.Fatalf("ambiguous auth response=%d catalog=%d lookup=%v served=%v", response.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}
}

func newArtworkHandler(t *testing.T, items map[string]map[string]watchstate.CatalogTitle, keys map[string]string) (*Handler, *artworkCatalog, *artworkDelivery) {
	t.Helper()
	profileOne := artworkProfileOne
	profileTwo := artworkProfileTwo
	authentication := &artworkAuthentication{sessions: map[string]AuthenticatedSession{
		artworkTokenOne: {
			ID: "session-one", ProfileID: profileOne, ProfileName: "Profile One", ExpiresAt: time.Now().Add(time.Hour),
			Principal: auth.Principal{SessionID: "native-one", UserID: "user-one", ActiveProfileID: &profileOne},
		},
		artworkTokenTwo: {
			ID: "session-two", ProfileID: profileTwo, ProfileName: "Profile Two", ExpiresAt: time.Now().Add(time.Hour),
			Principal: auth.Principal{SessionID: "native-two", UserID: "user-two", ActiveProfileID: &profileTwo},
		},
	}}
	catalog := &artworkCatalog{items: items}
	delivery := &artworkDelivery{keys: keys, body: []byte("pngbytes")}
	handler, err := New(Dependencies{Authentication: authentication, Catalog: catalog, Artwork: delivery})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}
	return handler, catalog, delivery
}
