package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	artworkdomain "github.com/moodiness/rivune/server/internal/artwork"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	runtimeGoldenServerID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	runtimeGoldenProfileID = "11111111-1111-4111-8111-111111111111"
	runtimeGoldenItemID    = "22222222-2222-4222-8222-222222222222"
	runtimeGoldenToken     = "rivune_jf_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

type runtimeGoldenAuthentication struct {
	session AuthenticatedSession
}

func (*runtimeGoldenAuthentication) Login(context.Context, CompatLoginInput) (LoginResult, error) {
	return LoginResult{}, errors.New("login is not used by runtime golden tests")
}

func (authentication *runtimeGoldenAuthentication) Authenticate(_ context.Context, token string) (AuthenticatedSession, error) {
	if token != runtimeGoldenToken {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return authentication.session, nil
}

func (authentication *runtimeGoldenAuthentication) Revalidate(_ context.Context, expected AuthenticatedSession) (AuthenticatedSession, error) {
	return expected, nil
}

func (*runtimeGoldenAuthentication) Logout(context.Context, AuthenticatedSession) error {
	return errors.New("logout is not used by runtime golden tests")
}

type runtimeGoldenPlaybackDelivery struct {
	*fakeCompatPlaybackDelivery
}

func (delivery *runtimeGoldenPlaybackDelivery) Open(ctx context.Context, principal auth.Principal, input playback.ResolveInput) (playback.Delivery, error) {
	result, err := delivery.fakeCompatPlaybackDelivery.Open(ctx, principal, input)
	if err != nil || !input.AllowTranscoding {
		return result, err
	}
	result.Session = playback.Session{SelectedSourceID: "native-transcode", Sources: []playback.Source{{
		ID: "native-transcode", Mode: "transcode", Protocol: "hls", Container: "hls",
	}}}
	return result, nil
}

type runtimeGoldenCatalog struct {
	item watchstate.CatalogTitle
	page watchstate.CatalogPage
}

func (catalog *runtimeGoldenCatalog) GetCatalogTitle(_ context.Context, _ auth.Principal, id string) (watchstate.CatalogTitle, error) {
	if id != catalog.item.ID {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	return catalog.item, nil
}

func (catalog *runtimeGoldenCatalog) ListCatalogItems(_ context.Context, _ auth.Principal, _ watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return catalog.page, nil
}

type runtimeGoldenArtwork struct {
	key      string
	metadata artworkdomain.ImageMetadata
}

func (artwork *runtimeGoldenArtwork) LookupKey(_ context.Context, materialized string) (string, bool) {
	return artwork.key, materialized == localizedArtworkPrefix+artwork.key
}

func (artwork *runtimeGoldenArtwork) DescribeKey(_ context.Context, key string) (artworkdomain.ImageMetadata, bool) {
	return artwork.metadata, key == artwork.key
}

func (artwork *runtimeGoldenArtwork) ServeKey(response http.ResponseWriter, request *http.Request, key string) {
	if key != artwork.key {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	response.Header().Set("Content-Length", "8")
	response.Header().Set("Content-Type", "image/png")
	response.Header().Set("ETag", `"`+key+`"`)
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = response.Write([]byte("pngbytes"))
	}
}

type runtimeGoldenResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

func TestRuntimeHTTPGoldenContracts(t *testing.T) {
	handler := newRuntimeGoldenHandler(t)
	imageTag := strings.Repeat("a", 64)

	tests := []struct {
		name      string
		method    string
		target    string
		body      string
		token     bool
		headers   []string
		normalize func(*testing.T, []byte) []byte
	}{
		{
			name: "items_page", method: http.MethodGet,
			target: "/Items?SearchTerm=Golden&StartIndex=1&Limit=1&IncludeItemTypes=Movie&Fields=Overview,Genres,ProviderIds&EnableUserData=true",
			token:  true, headers: runtimeGoldenJSONHeaders,
		},
		{
			name: "search_hints", method: http.MethodGet,
			target: "/Search/Hints?SearchTerm=Golden&IncludeItemTypes=Movie&StartIndex=1&Limit=1",
			token:  true, headers: runtimeGoldenJSONHeaders,
		},
		{
			name: "playback_info_direct", method: http.MethodPost,
			target: "/Items/" + runtimeGoldenItemID + "/PlaybackInfo",
			body:   `{"EnableDirectPlay":true,"EnableDirectStream":true,"EnableTranscoding":false,"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`,
			token:  true, headers: runtimeGoldenJSONHeaders, normalize: normalizeRuntimeGoldenPlayback,
		},
		{
			name: "playback_info_transcode_ts", method: http.MethodPost,
			target: "/Items/" + runtimeGoldenItemID + "/PlaybackInfo",
			body:   `{"StartTimeTicks":30000000,"EnableDirectPlay":false,"EnableDirectStream":false,"EnableTranscoding":true,"DeviceProfile":{"TranscodingProfiles":[{"Container":"ts","VideoCodec":"h264","AudioCodec":"aac","Protocol":"hls","Context":"Streaming","Type":"Video"}]}}`,
			token:  true, headers: runtimeGoldenJSONHeaders, normalize: normalizeRuntimeGoldenPlayback,
		},
		{
			name: "playback_info_transcode_fmp4", method: http.MethodPost,
			target: "/Items/" + runtimeGoldenItemID + "/PlaybackInfo",
			body:   `{"StartTimeTicks":30000000,"EnableDirectPlay":false,"EnableDirectStream":false,"EnableTranscoding":true,"DeviceProfile":{"TranscodingProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Protocol":"hls","Context":"Streaming","Type":"Video"}]}}`,
			token:  true, headers: runtimeGoldenJSONHeaders, normalize: normalizeRuntimeGoldenPlayback,
		},
		{
			name: "image_infos", method: http.MethodGet,
			target: "/Items/" + runtimeGoldenItemID + "/Images",
			token:  true, headers: runtimeGoldenJSONHeaders,
		},
		{
			name: "image_head", method: http.MethodHead,
			target: "/Items/" + runtimeGoldenItemID + "/Images/Primary?tag=" + imageTag,
			token:  true, headers: []string{"Cache-Control", "Content-Length", "Content-Type", "ETag"},
		},
		{
			name: "malformed_error", method: http.MethodGet,
			target: "/Items?SearchTerm=Golden&Limit=not-a-number",
			token:  true, headers: runtimeGoldenJSONHeaders,
		},
		{
			name: "unauthorized_error", method: http.MethodGet,
			target:  "/Items?SearchTerm=Golden",
			headers: append(append([]string(nil), runtimeGoldenJSONHeaders...), "WWW-Authenticate"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if test.token {
				request.Header.Set("X-Emby-Token", runtimeGoldenToken)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertRuntimeHTTPGolden(t, test.name, response, test.headers, test.normalize)
		})
	}
}

var runtimeGoldenJSONHeaders = []string{"Cache-Control", "Content-Type", "X-Content-Type-Options"}

func newRuntimeGoldenHandler(t *testing.T) *Handler {
	t.Helper()
	serverID, err := ParseServerID(runtimeGoldenServerID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	profileID := runtimeGoldenProfileID
	session := AuthenticatedSession{
		ID: "33333333-3333-4333-8333-333333333333", ProfileID: profileID, ProfileName: "Golden Viewer",
		Client:    ClientIdentity{Client: "Golden Client", Device: "Fixture", DeviceID: "golden-device", Version: "1.0"},
		ExpiresAt: now.Add(time.Hour),
		Principal: auth.Principal{
			SessionID: "44444444-4444-4444-8444-444444444444", UserID: "55555555-5555-4555-8555-555555555555",
			DeviceID: "golden-native-device", ActiveProfileID: &profileID,
		},
	}
	runtimeMinutes := 95
	imageTag := strings.Repeat("a", 64)
	item := watchstate.CatalogTitle{
		ID: runtimeGoldenItemID, MediaType: "movie", Title: "Golden Film", Released: "2024-02-03",
		Overview: "Small deterministic fixture.", RuntimeMinutes: &runtimeMinutes, Genres: []string{"Drama"},
		ProviderIDs: map[string]string{"imdb": "tt1234567", "provider-secret": "must-not-escape"},
		PosterURL:   localizedArtworkPrefix + imageTag, ResourceID: "fixture-resource",
	}
	catalog := &runtimeGoldenCatalog{item: item, page: watchstate.CatalogPage{
		Items: []watchstate.CatalogTitle{item}, Offset: 1, Limit: 1, Total: 3,
	}}
	baseDelivery := &fakeCompatPlaybackDelivery{
		now: now, handle: opaquePlaybackHandleNamed(t, "runtime-golden"),
		sources: playback.SourceList{Sources: []playback.SourceOption{{
			SourceRef: "fixture-source", Protocol: "http", Container: "mp4", ExpiresAt: now.Add(time.Hour),
		}}},
		resolvedSession: playback.Session{
			SelectedSourceID: "native-direct",
			Sources:          []playback.Source{{ID: "native-direct", Mode: "direct", Protocol: "http", Container: "mp4"}},
		},
	}
	delivery := &runtimeGoldenPlaybackDelivery{fakeCompatPlaybackDelivery: baseDelivery}
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Rivune", RuntimeVersion: "10.11.11-golden"},
		Authentication: &runtimeGoldenAuthentication{session: session}, Catalog: catalog,
		Artwork:  &runtimeGoldenArtwork{key: imageTag, metadata: artworkdomain.ImageMetadata{Width: 600, Height: 900, Size: 12345}},
		Playback: delivery,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func normalizeRuntimeGoldenPlayback(t *testing.T, body []byte) []byte {
	t.Helper()
	var value struct {
		PlaySessionID string `json:"PlaySessionId"`
	}
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode playback golden response: %v", err)
	}
	if !isRivunePlaySessionID(value.PlaySessionID) {
		t.Fatalf("playback response has invalid play session id: %q", value.PlaySessionID)
	}
	needle := []byte(value.PlaySessionID)
	if bytes.Count(body, needle) < 2 {
		t.Fatalf("playback response did not bind its transport URLs to the play session")
	}
	return bytes.ReplaceAll(body, needle, []byte("PLAY_SESSION_ID"))
}

func assertRuntimeHTTPGolden(t *testing.T, name string, response *httptest.ResponseRecorder, headers []string, normalize func(*testing.T, []byte) []byte) {
	t.Helper()
	body := response.Body.Bytes()
	if normalize != nil {
		body = normalize(t, body)
	}
	selectedHeaders := make(map[string]string, len(headers))
	for _, header := range headers {
		if value := response.Header().Get(header); value != "" {
			selectedHeaders[header] = value
		}
	}
	actual := runtimeGoldenResponse{Status: response.Code, Headers: selectedHeaders}
	if len(bytes.TrimSpace(body)) != 0 {
		if !json.Valid(body) {
			t.Fatalf("response body is not JSON: %q", body)
		}
		actual.Body = json.RawMessage(body)
	}
	encoded, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		t.Fatalf("encode golden response: %v", err)
	}
	expected, err := os.ReadFile(filepath.Join("testdata", "runtime_http", name+".golden.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(expected), bytes.TrimSpace(encoded)) {
		t.Fatalf("runtime HTTP contract changed (-want +got):\nwant:\n%s\n\ngot:\n%s", expected, encoded)
	}
}
