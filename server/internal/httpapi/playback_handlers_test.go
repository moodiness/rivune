package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/requestwork"
	"github.com/moodiness/rivune/server/internal/settings"
)

type fakePlaybackService struct {
	sourcesInput     playback.SourcesInput
	markersInput     playback.MarkerInput
	prepareInput     playback.PrepareInput
	resolveInput     playback.ResolveInput
	sources          playback.SourceList
	markers          playback.MarkerList
	preparation      playback.Preparation
	session          playback.Session
	activity         playback.Activity
	sourcesErr       error
	markersErr       error
	prepareErr       error
	resolveErr       error
	proxyErr         error
	proxy            func(http.ResponseWriter, *http.Request) error
	proxyCalls       int
	activityErr      error
	stopActivityErr  error
	purgeActivityErr error
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

func (fake *fakePlaybackService) StopActivitySession(context.Context, auth.Principal, string) error {
	return fake.stopActivityErr
}

func (fake *fakePlaybackService) PurgeActivity(context.Context, auth.Principal) (playback.PurgeResult, error) {
	return playback.PurgeResult{}, fake.purgeActivityErr
}

func (fake *fakePlaybackService) ProxyAsset(w http.ResponseWriter, r *http.Request, _ string, _ string, _ string, _ string, _ string) error {
	fake.proxyCalls++
	if fake.proxy != nil {
		return fake.proxy(w, r)
	}
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
		"addonId":"11111111-1111-4111-8111-111111111111",
		"resourceId":"tt1234567",
		"capabilities":{"streamingProtocols":["http"],"containers":["mp4"],"processingModes":["remux"],"mediaProfiles":[{"container":"mp4","videoCodec":"h264","audioCodec":"aac"}],"externalPlayers":["system"]}
	}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sourceRef":"opaque-source-reference"`) {
		t.Fatalf("unexpected sources response: status=%d body=%s", response.Code, response.Body.String())
	}
	if service.sourcesInput.AddonID != "11111111-1111-4111-8111-111111111111" ||
		service.sourcesInput.ResourceID != "tt1234567" ||
		len(service.sourcesInput.Capabilities.StreamingProtocols) != 1 ||
		len(service.sourcesInput.Capabilities.ProcessingModes) != 1 ||
		len(service.sourcesInput.Capabilities.MediaProfiles) != 1 ||
		len(service.sourcesInput.Capabilities.ExternalPlayers) != 1 {
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

func TestPreparePlaybackReportsUnsupportedSourceWithoutStartingSession(t *testing.T) {
	service := &fakePlaybackService{prepareErr: playback.ErrUnsupportedSource}
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{SessionID: "session-id", UserID: "user-id"}}
	api.playback = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/prepare", stringsReader(`{"sourceRef":"opaque-source-reference"}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), `"code":"playback_source_unsupported"`) ||
		!strings.Contains(response.Body.String(), "external player") {
		t.Fatalf("unexpected unsupported source response: status=%d body=%s", response.Code, response.Body.String())
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
		Diagnostics: playback.MediaDiagnostics{FFmpegVersion: "7.1", FFprobeVersion: "7.1", HardwareAcceleration: "software", VideoEncoder: "h264", TranscodeThreads: 4, MaximumReadRate: 1.5},
		Sessions: []playback.ActivitySession{{
			ID: "11111111-1111-4111-8111-111111111111", TitleID: "episode-1",
			ArtworkURL: "https://images.example.test/episode-still.jpg",
			ExternalIDs: playback.ActivityExternalIDs{
				IMDb: "tt9000001", TMDB: "900001", TVDB: "9000006",
			},
			ExternalIDMediaTypes: playback.ActivityExternalIDMediaTypes{IMDb: "series", TMDB: "series", TVDB: "episode"},
			Title:                "Fixture Episode", MediaType: "episode", Mode: "direct",
			Username: "admin", ProfileID: "22222222-2222-4222-8222-222222222222",
			Profile: "Alice", Device: "Living room", Platform: "Web",
			PositionSeconds: 605, DurationSeconds: 1320,
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
		!strings.Contains(response.Body.String(), `"externalIds":{"imdb":"tt9000001","tmdb":"900001","tvdb":"9000006"}`) ||
		!strings.Contains(response.Body.String(), `"externalIdMediaTypes":{"imdb":"series","tmdb":"series","tvdb":"episode"}`) ||
		!strings.Contains(response.Body.String(), `"positionSeconds":605,"durationSeconds":1320`) {
		t.Fatalf("unexpected activity response: status=%d body=%s", response.Code, response.Body.String())
	}
	validateContractResponse(t, loadOpenAPIContract(t), "/playback/activity", nil, request, response)
}

func TestPlaybackAssetReturnsStableMediaErrors(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{name: "source", method: http.MethodGet, err: playback.ErrMediaSourceFailed, status: http.StatusBadGateway, code: "playback_source_failed"},
		{name: "capability", method: http.MethodGet, err: playback.ErrClientCapabilityMissing, status: http.StatusUnprocessableEntity, code: "playback_client_capability_missing"},
		{name: "capacity GET", method: http.MethodGet, err: playback.ErrMediaCapacityReached, status: http.StatusServiceUnavailable, code: "playback_capacity_reached", retryAfter: "10"},
		{name: "capacity HEAD", method: http.MethodHead, err: playback.ErrMediaCapacityReached, status: http.StatusServiceUnavailable, code: "playback_capacity_reached", retryAfter: "10"},
		{name: "storage GET", method: http.MethodGet, err: playback.ErrMediaStorageLimit, status: http.StatusInsufficientStorage, code: "playback_storage_limit"},
		{name: "storage HEAD", method: http.MethodHead, err: playback.ErrMediaStorageLimit, status: http.StatusInsufficientStorage, code: "playback_storage_limit"},
		{name: "processing", method: http.MethodGet, err: playback.ErrMediaProcessingFailed, status: http.StatusBadGateway, code: "playback_processing_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := testAPI(&fakeInstanceService{})
			api.playback = &fakePlaybackService{proxyErr: test.err}
			request := httptest.NewRequest(test.method, "/api/v1/playback/sessions/11111111-1111-4111-8111-111111111111/assets/asset-id?token=token", nil)
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("unexpected error status=%d body=%s", response.Code, response.Body.String())
			}
			if test.method == http.MethodHead {
				if response.Body.Len() != 0 {
					t.Fatalf("HEAD error returned a body: %s", response.Body.String())
				}
			} else if !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("unexpected error body: %s", response.Body.String())
			}
			if response.Header().Get("Retry-After") != test.retryAfter {
				t.Fatalf("unexpected Retry-After header %q", response.Header().Get("Retry-After"))
			}
			if test.status == http.StatusServiceUnavailable || test.status == http.StatusInsufficientStorage {
				validateContractResponse(t, loadOpenAPIContract(t), "/playback/sessions/{sessionId}/assets/{assetId}", map[string]string{"sessionId": "11111111-1111-4111-8111-111111111111", "assetId": "asset-id"}, request, response)
			}
		})
	}
}

func TestPlaybackAssetDoesNotAppendJSONAfterStreamCommit(t *testing.T) {
	service := &fakePlaybackService{proxy: func(response http.ResponseWriter, _ *http.Request) error {
		response.Header().Set("Content-Type", "video/mp4")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("media-bytes"))
		return errors.New("copy failed")
	}}
	api := testAPI(&fakeInstanceService{})
	var logs bytes.Buffer
	api.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	api.playback = service
	request := httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/session-secret/assets/asset-secret?token=query-secret", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "media-bytes" {
		t.Fatalf("committed stream response = %d %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "internal_error") || !strings.Contains(logs.String(), "failed after response committed") {
		t.Fatalf("committed stream error handling body=%q logs=%s", response.Body.String(), logs.String())
	}
	for _, secret := range []string{"session-secret", "asset-secret", "query-secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("committed stream log exposed %q: %s", secret, logs.String())
		}
	}
}

func TestNativeTracingUsesNormalizedRouteAndFixedWorkCounters(t *testing.T) {
	var logs bytes.Buffer
	service := &fakePlaybackService{proxy: func(response http.ResponseWriter, request *http.Request) error {
		requestwork.BeginDB(request.Context(), 10)
		requestwork.EndDB(request.Context(), 40)
		requestwork.BeginOutbound(request.Context(), 20)
		requestwork.EndOutbound(request.Context(), 70, 321)
		response.WriteHeader(http.StatusPartialContent)
		_, _ = response.Write([]byte("asset"))
		return nil
	}}
	api := testAPI(&fakeInstanceService{})
	api.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	api.playback = service
	for _, identifier := range []string{"first-secret-id", "second-secret-id"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/"+identifier+"/assets/media?token=query-secret", nil)
		api.Handler().ServeHTTP(httptest.NewRecorder(), request)
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("completion events = %d, want 2: %s", len(lines), logs.String())
	}
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode completion event: %v", err)
		}
		if event["route"] != "GET /api/v1/playback/sessions/{sessionId}/assets/{assetId}" ||
			event["status"] != float64(http.StatusPartialContent) || event["bytes"] != float64(5) ||
			event["db_call_count"] != float64(1) || event["db_duration"] != float64(30) ||
			event["outbound_call_count"] != float64(1) || event["outbound_duration"] != float64(50) ||
			event["upstream_bytes"] != float64(321) {
			t.Fatalf("completion event = %#v", event)
		}
		for _, forbidden := range []string{"first-secret-id", "second-secret-id", "query-secret", "path", "query"} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("completion event exposed %q: %s", forbidden, line)
			}
		}
	}
}

func TestPlaybackActivityManagementMapsAuthorizationAndMissingSession(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		service *fakePlaybackService
		status  int
		code    string
	}{
		{name: "activity admin required", method: http.MethodGet, path: "/api/v1/playback/activity", service: &fakePlaybackService{activityErr: playback.ErrForbidden}, status: http.StatusForbidden, code: "admin_required"},
		{name: "stop admin required", method: http.MethodDelete, path: "/api/v1/playback/activity/sessions/session-id", service: &fakePlaybackService{stopActivityErr: playback.ErrForbidden}, status: http.StatusForbidden, code: "admin_required"},
		{name: "purge admin required", method: http.MethodPost, path: "/api/v1/playback/activity/purge", service: &fakePlaybackService{purgeActivityErr: playback.ErrForbidden}, status: http.StatusForbidden, code: "admin_required"},
		{name: "stop missing", method: http.MethodDelete, path: "/api/v1/playback/activity/sessions/session-id", service: &fakePlaybackService{stopActivityErr: playback.ErrSessionNotFound}, status: http.StatusNotFound, code: "playback_session_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := testAPI(&fakeInstanceService{})
			api.auth = &fakeAuthService{principal: auth.Principal{Role: "member"}}
			api.playback = test.service
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Authorization", "Bearer access")
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("management response status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPlaybackAssetRedactsUpstreamURLFromLogsAndResponse(t *testing.T) {
	secretURL := "https://provider.example/private/segment.ts?target=key.bin&token=upstream-secret"
	networkCause := errors.New("connection reset")
	service := &fakePlaybackService{proxyErr: fmt.Errorf(
		"fetch playback asset: %w",
		&url.Error{Op: "Get", URL: secretURL, Err: networkCause},
	)}
	api := testAPI(&fakeInstanceService{})
	var logs bytes.Buffer
	api.logger = slog.New(slog.NewTextHandler(&logs, nil))
	api.playback = service
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/playback/sessions/session-id/assets/asset-id?token=client-secret",
		nil,
	)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("upstream failure status = %d, body=%s", response.Code, response.Body.String())
	}
	combined := logs.String() + response.Body.String()
	for _, secret := range []string{secretURL, "/private/segment.ts", "upstream-secret", "client-secret"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("playback log or response exposed %q: %s", secret, combined)
		}
	}
	if !strings.Contains(logs.String(), `msg="proxy playback asset"`) ||
		!strings.Contains(logs.String(), "fetch playback asset") ||
		!strings.Contains(logs.String(), "connection reset") {
		t.Fatalf("sanitized playback log lost operation or cause: %s", logs.String())
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
	request := httptest.NewRequest(http.MethodGet, "/api/v1/playback/markers?imdbId=tt9002001&season=1&episode=1", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"intro"`) {
		t.Fatalf("unexpected marker response: status=%d body=%s", response.Code, response.Body.String())
	}
	if service.markersInput.IMDBID != "tt9002001" || service.markersInput.Season != 1 || service.markersInput.Episode != 1 ||
		!service.markersInput.IncludeIntro || service.markersInput.IncludeRecap || !service.markersInput.IncludeOutro {
		t.Fatalf("unexpected marker input: %+v", service.markersInput)
	}
	validateContractResponse(t, loadOpenAPIContract(t), "/playback/markers", nil, request, response)
}

func TestPlaybackPrepareAndResolveReturnSpecificTranscodingErrors(t *testing.T) {
	profileID := "profile-id"
	expiresAt := time.Now().UTC().Add(time.Hour)
	tests := []struct {
		name string
		path string
		err  error
		code string
	}{
		{name: "prepare disabled", path: "/api/v1/playback/prepare", err: playback.ErrTranscodingDisabled, code: "playback_transcoding_disabled"},
		{name: "prepare capability", path: "/api/v1/playback/prepare", err: playback.ErrClientCapabilityMissing, code: "playback_client_capability_missing"},
		{name: "resolve disabled", path: "/api/v1/playback/resolve", err: playback.ErrTranscodingDisabled, code: "playback_transcoding_disabled"},
		{name: "resolve capability", path: "/api/v1/playback/resolve", err: playback.ErrClientCapabilityMissing, code: "playback_client_capability_missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakePlaybackService{}
			if strings.Contains(test.path, "/prepare") {
				service.prepareErr = test.err
			} else {
				service.resolveErr = test.err
			}
			api := testAPI(&fakeInstanceService{})
			api.auth = &fakeAuthService{principal: auth.Principal{
				SessionID: "session-id", UserID: "user-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
			}}
			api.playback = service
			api.settings = &fakeSettingsService{effective: settings.Effective{Values: settings.EffectiveValues{
				AllowTranscoding: false, MaximumResolution: "720p",
			}}}
			request := httptest.NewRequest(http.MethodPost, test.path, stringsReader(`{"sourceRef":"opaque-source-reference"}`))
			request.Header.Set("Authorization", "Bearer access-token")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("unexpected transcoding error: status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(test.path, "/prepare") {
				if service.prepareInput.AllowTranscoding || service.prepareInput.MaximumHeight != 720 {
					t.Fatalf("prepare did not receive fresh effective policy: %+v", service.prepareInput)
				}
			} else if service.resolveInput.AllowTranscoding || service.resolveInput.MaximumHeight != 720 {
				t.Fatalf("resolve did not receive fresh effective policy: %+v", service.resolveInput)
			}
		})
	}
}

func stringsReader(value string) *strings.Reader {
	return strings.NewReader(value)
}
