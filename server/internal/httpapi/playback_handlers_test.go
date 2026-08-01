package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/settings"
)

type fakePlaybackService struct {
	sourcesInput playback.SourcesInput
	markersInput playback.MarkerInput
	prepareInput playback.PrepareInput
	resolveInput playback.ResolveInput
	sources      playback.SourceList
	markers      playback.MarkerList
	preparation  playback.Preparation
	session      playback.Session
	activity     playback.Activity
	sourcesErr   error
	markersErr   error
	prepareErr   error
	resolveErr   error
	proxyErr     error
	activityErr  error
}

func (fake *fakePlaybackService) Sources(_ context.Context, _ auth.Principal, input playback.SourcesInput) (playback.SourceList, error) {
	fake.sourcesInput = input
	return fake.sources, fake.sourcesErr
}
func (fake *fakePlaybackService) Markers(_ context.Context, _ auth.Principal, input playback.MarkerInput) (playback.MarkerList, error) {
	fake.markersInput = input
	return fake.markers, fake.markersErr
}

func (fake *fakePlaybackService) Prepare(_ context.Context, _ auth.Principal, input playback.PrepareInput) (playback.Preparation, error) {
	fake.prepareInput = input
	return fake.preparation, fake.prepareErr
}

func (fake *fakePlaybackService) Resolve(_ context.Context, _ auth.Principal, input playback.ResolveInput) (playback.Session, error) {
	fake.resolveInput = input
	return fake.session, fake.resolveErr
}

func (*fakePlaybackService) Stop(context.Context, auth.Principal, string) error {
	return nil
}

func (fake *fakePlaybackService) Activity(context.Context, auth.Principal) (playback.Activity, error) {
	return fake.activity, fake.activityErr
}

func (*fakePlaybackService) StopActivitySession(context.Context, auth.Principal, string) error {
	return nil
}

func (*fakePlaybackService) PurgeActivity(context.Context, auth.Principal) (playback.PurgeResult, error) {
	return playback.PurgeResult{}, nil
}

func (fake *fakePlaybackService) ProxyAsset(http.ResponseWriter, *http.Request, string, string, string, string, string) error {
	return fake.proxyErr
}

func TestPlaybackSourcesReturnsOpaqueReferences(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	service := &fakePlaybackService{sources: playback.SourceList{Sources: []playback.SourceOption{{
		ID: "stream-1", SourceRef: "opaque-source-reference", Name: "Source", Protocol: "http", ExpiresAt: expiresAt,
	}}}}
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{SessionID: "session-id", UserID: "user-id"}}
	api.playback = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/sources", stringsReader(`{
		"mediaType":"movie",
		"resourceId":"tt1234567",
		"capabilities":{"streamingProtocols":["http"],"containers":["mp4"]}
	}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sourceRef":"opaque-source-reference"`) {
		t.Fatalf("unexpected sources response: status=%d body=%s", response.Code, response.Body.String())
	}
	if service.sourcesInput.ResourceID != "tt1234567" || len(service.sourcesInput.Capabilities.StreamingProtocols) != 1 {
		t.Fatalf("unexpected sources input: %+v", service.sourcesInput)
	}
}

func TestPreparePlaybackUsesOpaqueReference(t *testing.T) {
	service := &fakePlaybackService{preparation: playback.Preparation{
		SourceRef: "opaque-source-reference", Mode: "remux", Protocol: "http", Container: "mp4", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}}
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{SessionID: "session-id", UserID: "user-id"}}
	api.playback = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/prepare", stringsReader(`{"sourceRef":"opaque-source-reference"}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || service.prepareInput.SourceRef != "opaque-source-reference" || !strings.Contains(response.Body.String(), `"mode":"remux"`) {
		t.Fatalf("unexpected preparation response: status=%d input=%+v body=%s", response.Code, service.prepareInput, response.Body.String())
	}
}

func TestResolvePlaybackCreatesSessionFromOpaqueReference(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	service := &fakePlaybackService{session: playback.Session{
		ID: "11111111-1111-4111-8111-111111111111", SelectedSourceID: "stream-1",
		Sources:   []playback.Source{{ID: "stream-1", Mode: "direct", Protocol: "hls", Compatible: true}},
		Subtitles: []playback.Subtitle{}, ProviderErrors: []playback.ProviderFailure{}, ExpiresAt: expiresAt,
	}}
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{SessionID: "session-id", UserID: "user-id"}}
	api.playback = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/resolve", stringsReader(`{
		"sourceRef":"opaque-source-reference",
		"titleId":"11111111-1111-4111-8111-111111111111"
	}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.resolveInput.SourceRef != "opaque-source-reference" || service.resolveInput.TitleID == "" {
		t.Fatalf("unexpected playback input: %+v", service.resolveInput)
	}
}

func TestPlaybackActivityIncludesArtworkAndCanonicalProviderIDs(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	service := &fakePlaybackService{activity: playback.Activity{
		Summary: playback.ActivitySummary{
			ActiveSessions: 1, ActiveJobs: 0, ProcessingSlots: 0, ProcessingLimit: 2,
			StorageBytes: 0, StorageLimitBytes: 1 << 30,
		},
		Diagnostics: playback.MediaDiagnostics{VideoEncoder: "h264"},
		Sessions: []playback.ActivitySession{{
			ID: "11111111-1111-4111-8111-111111111111", TitleID: "episode-1",
			ArtworkURL: "https://images.example.test/episode-still.jpg",
			ExternalIDs: playback.ActivityExternalIDs{
				IMDb: "tt0149460", TMDB: "300131", TVDB: "11704240",
			},
			ExternalIDMediaTypes: playback.ActivityExternalIDMediaTypes{IMDb: "series", TMDB: "series", TVDB: "episode"},
			Title:                "Combat de tétines", MediaType: "episode", Mode: "direct",
			Username: "admin", ProfileID: "22222222-2222-4222-8222-222222222222",
			Profile: "Alice", Device: "Living room", Platform: "Web",
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		}},
		Jobs: []playback.MediaActivityJob{},
	}}
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{Role: "admin"}}
	api.playback = service
	request := httptest.NewRequest(http.MethodGet, "/api/v1/playback/activity", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"artworkUrl":"https://images.example.test/episode-still.jpg"`) ||
		!strings.Contains(response.Body.String(), `"externalIds":{"imdb":"tt0149460","tmdb":"300131","tvdb":"11704240"}`) ||
		!strings.Contains(response.Body.String(), `"externalIdMediaTypes":{"imdb":"series","tmdb":"series","tvdb":"episode"}`) {
		t.Fatalf("unexpected activity response: status=%d body=%s", response.Code, response.Body.String())
	}
	validateContractResponse(t, loadOpenAPIContract(t), "/playback/activity", nil, request, response)
}

func TestPlaybackAssetReturnsStableMediaErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{name: "source", err: playback.ErrMediaSourceFailed, status: http.StatusBadGateway, code: "playback_source_failed"},
		{name: "capacity", err: playback.ErrMediaCapacityReached, status: http.StatusServiceUnavailable, code: "playback_capacity_reached", retryAfter: "10"},
		{name: "storage", err: playback.ErrMediaStorageLimit, status: http.StatusInsufficientStorage, code: "playback_storage_limit"},
		{name: "processing", err: playback.ErrMediaProcessingFailed, status: http.StatusBadGateway, code: "playback_processing_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := testAPI(&fakeInstanceService{})
			api.playback = &fakePlaybackService{proxyErr: test.err}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/session-id/assets/asset-id?token=token", nil)
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("unexpected error response: status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Retry-After") != test.retryAfter {
				t.Fatalf("unexpected Retry-After header %q", response.Header().Get("Retry-After"))
			}
		})
	}
}

func TestPlaybackMarkersApplyEffectiveSkipSettings(t *testing.T) {
	profileID := "profile-id"
	expiresAt := time.Now().UTC().Add(time.Hour)
	service := &fakePlaybackService{markers: playback.MarkerList{Markers: []playback.Marker{{
		Type: playback.MarkerTypeIntro, StartSeconds: 10, EndSeconds: 70, Confidence: 1, SubmissionCount: 2,
	}}}}
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{
		SessionID: "session-id", UserID: "user-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
	}}
	api.playback = service
	api.settings = &fakeSettingsService{effective: settings.Effective{Values: settings.EffectiveValues{
		SkipIntroEnabled: true, SkipRecapEnabled: false, SkipOutroEnabled: true,
	}}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/playback/markers?imdbId=tt0903747&season=1&episode=1", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"intro"`) {
		t.Fatalf("unexpected marker response: status=%d body=%s", response.Code, response.Body.String())
	}
	if service.markersInput.IMDBID != "tt0903747" || service.markersInput.Season != 1 || service.markersInput.Episode != 1 ||
		!service.markersInput.IncludeIntro || service.markersInput.IncludeRecap || !service.markersInput.IncludeOutro {
		t.Fatalf("unexpected marker input: %+v", service.markersInput)
	}
	validateContractResponse(t, loadOpenAPIContract(t), "/playback/markers", nil, request, response)
}

func stringsReader(value string) *strings.Reader {
	return strings.NewReader(value)
}
