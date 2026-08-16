package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/config"
	"github.com/moodiness/rivune/server/internal/demo"
	"github.com/moodiness/rivune/server/internal/instance"
	"github.com/moodiness/rivune/server/internal/requestwork"
	"github.com/moodiness/rivune/server/internal/runtimesettings"
	"github.com/moodiness/rivune/server/internal/settings"
	"github.com/moodiness/rivune/server/internal/tracking"
)

func TestNativeRequestIDIsValidatedReturnedAndLogged(t *testing.T) {
	tests := []struct {
		name     string
		supplied string
		accept   bool
	}{
		{name: "generated"},
		{name: "accepted", supplied: "gateway:request-42", accept: true},
		{name: "whitespace", supplied: "bad request"},
		{name: "control", supplied: "bad\nrequest"},
		{name: "oversized", supplied: strings.Repeat("a", 129)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			api := testAPI(&fakeInstanceService{})
			api.logger = slog.New(slog.NewTextHandler(&logs, nil))
			var contextRequestID string
			handler := api.middleware(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				contextRequestID = requestwork.RequestID(request.Context())
				response.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/native", nil)
			request.Header.Set(requestwork.RequestIDHeader, test.supplied)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			returned := response.Header().Get(requestwork.RequestIDHeader)
			if returned == "" || returned != contextRequestID || !requestwork.ValidRequestID(returned) {
				t.Fatalf("request IDs response=%q context=%q", returned, contextRequestID)
			}
			if (returned == test.supplied) != test.accept {
				t.Fatalf("supplied request ID %q reflected as %q, accept=%t", test.supplied, returned, test.accept)
			}
			if !strings.Contains(logs.String(), "request_id="+returned) || !strings.Contains(logs.String(), "request completed") {
				t.Fatalf("completion log lacks correlated ID: %s", logs.String())
			}
		})
	}
}

func TestPanicResponseAndBothLogsRetainRequestID(t *testing.T) {
	var logs bytes.Buffer
	api := testAPI(&fakeInstanceService{})
	api.logger = slog.New(slog.NewTextHandler(&logs, nil))
	handler := api.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("failure") }))
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set(requestwork.RequestIDHeader, "panic-correlation-9")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError || response.Header().Get(requestwork.RequestIDHeader) != "panic-correlation-9" {
		t.Fatalf("panic response status=%d request ID=%q", response.Code, response.Header().Get(requestwork.RequestIDHeader))
	}
	if strings.Count(logs.String(), "request_id=panic-correlation-9") != 2 ||
		!strings.Contains(logs.String(), "panic serving request") || !strings.Contains(logs.String(), "request completed") {
		t.Fatalf("panic correlation logs = %s", logs.String())
	}
}

func TestMiddlewareRethrowsHTTPAbortHandler(t *testing.T) {
	var logs bytes.Buffer
	api := testAPI(&fakeInstanceService{})
	api.logger = slog.New(slog.NewTextHandler(&logs, nil))
	handler := api.middleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("subtitle prefix"))
		panic(http.ErrAbortHandler)
	}))

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/subtitle.vtt", nil))
	}()

	if recovered != http.ErrAbortHandler {
		t.Fatalf("middleware recovered panic = %v, want http.ErrAbortHandler", recovered)
	}
	if strings.Contains(logs.String(), "panic serving request") {
		t.Fatalf("connection abort was logged as an application panic: %s", logs.String())
	}
}

type fakeInstanceService struct {
	infoCalls   int
	info        instance.Info
	infoErr     error
	setupResult instance.SetupResult
	setupErr    error
	setupToken  string
	setupInput  instance.SetupInput
}

func (f *fakeInstanceService) Info(context.Context) (instance.Info, error) {
	f.infoCalls++
	return f.info, f.infoErr
}

func (f *fakeInstanceService) Setup(_ context.Context, token string, input instance.SetupInput) (instance.SetupResult, error) {
	f.setupToken = token
	f.setupInput = input
	return f.setupResult, f.setupErr
}

func (f *fakeInstanceService) AcquireSetupPending(context.Context) (func(), error) {
	if f.infoErr != nil {
		return nil, f.infoErr
	}
	if !f.info.SetupRequired {
		return nil, instance.ErrAlreadyConfigured
	}
	return func() {}, nil
}

func (f *fakeInstanceService) AdmitDemoSession(
	_ context.Context,
	_ [sha256.Size]byte,
	_ string,
	_ time.Time,
	_ time.Time,
	_ int,
	_ int,
	prepare func() error,
) (string, func(), error) {
	if f.infoErr != nil {
		return "", nil, f.infoErr
	}
	if !f.info.SetupRequired {
		return "", nil, instance.ErrAlreadyConfigured
	}
	if err := prepare(); err != nil {
		return "", nil, err
	}
	return "00000000-0000-4000-8000-000000000001", func() {}, nil
}

func (f *fakeInstanceService) ReleaseDemoSession(context.Context, string) (func(), error) {
	if f.infoErr != nil {
		return nil, f.infoErr
	}
	if !f.info.SetupRequired {
		return nil, instance.ErrAlreadyConfigured
	}
	return func() {}, nil
}

type databasePingerFunc func(context.Context) error

func (ping databasePingerFunc) Ping(ctx context.Context) error {
	return ping(ctx)
}

func TestLivenessRemainsLiveWithoutDatabaseAndLogsCorrelation(t *testing.T) {
	var logs bytes.Buffer
	pingCalls := 0
	api := testAPI(&fakeInstanceService{})
	api.logger = slog.New(slog.NewTextHandler(&logs, nil))
	api.pool = databasePingerFunc(func(context.Context) error {
		pingCalls++
		return errors.New("database unavailable")
	})
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if pingCalls != 0 {
		t.Fatalf("liveness ping calls = %d, want 0", pingCalls)
	}
	var body map[string]string
	decodeResponse(t, response, &body)
	if body["status"] != "ok" || body["version"] != "test" || body["database"] != "" {
		t.Fatalf("unexpected liveness response: %+v", body)
	}
	if !strings.Contains(logs.String(), "request completed") || !strings.Contains(logs.String(), "request_id=") {
		t.Fatalf("successful liveness probe lacks correlation log: %s", logs.String())
	}
}

func TestHealthAndReadinessCheckDatabaseWithTimeoutAndCompletionLog(t *testing.T) {
	for _, path := range []string{"/health", "/ready"} {
		for _, test := range []struct {
			name       string
			pingErr    error
			wantStatus int
			wantState  string
		}{
			{name: "ready", wantStatus: http.StatusOK, wantState: "ok"},
			{name: "database unavailable", pingErr: errors.New("database offline"), wantStatus: http.StatusServiceUnavailable, wantState: "unavailable"},
		} {
			t.Run(path+"/"+test.name, func(t *testing.T) {
				var logs bytes.Buffer
				pingCalls := 0
				api := testAPI(&fakeInstanceService{})
				api.logger = slog.New(slog.NewTextHandler(&logs, nil))
				api.pool = databasePingerFunc(func(ctx context.Context) error {
					pingCalls++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 2*time.Second {
						t.Fatalf("readiness ping deadline = %v, present=%t", deadline, ok)
					}
					return test.pingErr
				})
				response := httptest.NewRecorder()

				api.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

				if response.Code != test.wantStatus {
					t.Fatalf("readiness status = %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
				}
				if pingCalls != 1 {
					t.Fatalf("readiness ping calls = %d, want 1", pingCalls)
				}
				var body map[string]string
				decodeResponse(t, response, &body)
				if body["status"] != test.wantState || body["database"] != test.wantState || body["version"] != "test" {
					t.Fatalf("unexpected readiness response: %+v", body)
				}
				if !strings.Contains(logs.String(), "request completed") || !strings.Contains(logs.String(), "request_id=") {
					t.Fatalf("readiness completion log = %s", logs.String())
				}
				if test.pingErr != nil && (!strings.Contains(logs.String(), "database readiness check failed") || !strings.Contains(logs.String(), "status=503")) {
					t.Fatalf("failed readiness logs = %s", logs.String())
				}
			})
		}
	}
}

func TestHealthProbePanicRetainsErrorAndCompletionLogs(t *testing.T) {
	var logs bytes.Buffer
	api := testAPI(&fakeInstanceService{})
	api.logger = slog.New(slog.NewTextHandler(&logs, nil))
	handler := api.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("probe failure")
	}))
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Pattern = "GET /health"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want 500", response.Code)
	}
	if !strings.Contains(logs.String(), "panic serving request") || !strings.Contains(logs.String(), "request completed") {
		t.Fatalf("panic logs = %s", logs.String())
	}
}

func TestDiscoveryDescribesUnconfiguredServer(t *testing.T) {
	service := &fakeInstanceService{info: instance.Info{Name: "Rivune", SetupRequired: true}}
	api := testAPI(service)
	runtimeSource, err := runtimesettings.New(runtimesettings.Values{
		Revision:                     1,
		Timezone:                     "Europe/Paris",
		HardwareAcceleration:         runtimesettings.DefaultHardwareAcceleration,
		PreferredTranscodeVideoCodec: runtimesettings.DefaultPreferredTranscodeVideoCodec,
		TranscodeQualityPreset:       runtimesettings.DefaultTranscodeQualityPreset,
		TranscodeConcurrency:         runtimesettings.DefaultTranscodeConcurrency,
		TranscodeMaxBitrateKbps:      runtimesettings.DefaultTranscodeMaxBitrateKbps,
		MediaMaxStorageMB:            runtimesettings.DefaultMediaMaxStorageMB,
		ArtworkMaxStorageMB:          runtimesettings.DefaultArtworkMaxStorageMB,
		AllowTranscoding:             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	api.runtimeSettings = &runtimeSettingsCoordinator{source: runtimeSource}
	request := httptest.NewRequest(http.MethodGet, "/.well-known/rivune", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	var body struct {
		Name              string   `json:"name"`
		ProtocolVersion   int      `json:"protocolVersion"`
		APIBaseURL        string   `json:"apiBaseUrl"`
		SetupRequired     bool     `json:"setupRequired"`
		Timezone          string   `json:"timezone"`
		InterfaceLanguage string   `json:"interfaceLanguage"`
		Capabilities      []string `json:"capabilities"`
	}
	decodeResponse(t, response, &body)
	if body.Name != "Rivune" || body.ProtocolVersion != 20 || body.APIBaseURL != "https://media.example/api/v1" ||
		body.Timezone != "Europe/Paris" || body.InterfaceLanguage != "en" || !body.SetupRequired ||
		!slices.Equal(body.Capabilities, nativeCapabilities[:]) {
		t.Fatalf("unexpected discovery response: %+v", body)
	}
}
func TestDiscoveryUsesInstanceInterfaceLanguage(t *testing.T) {
	arabic := "ar"
	api := testAPI(&fakeInstanceService{info: instance.Info{Name: "Rivune"}})
	api.settings = &fakeSettingsService{instance: settings.Layer{
		SchemaVersion: 1,
		Values:        settings.Values{InterfaceLanguage: &arabic},
	}}
	request := httptest.NewRequest(http.MethodGet, "/.well-known/rivune", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		InterfaceLanguage string `json:"interfaceLanguage"`
	}
	decodeResponse(t, response, &body)
	if body.InterfaceLanguage != "ar" {
		t.Fatalf("interface language = %q, want ar", body.InterfaceLanguage)
	}
}

func TestDiscoveryUsesBuiltInInterfaceLanguageWhenInstanceSettingIsClear(t *testing.T) {
	api := testAPI(&fakeInstanceService{info: instance.Info{Name: "Rivune"}})
	request := httptest.NewRequest(http.MethodGet, "/.well-known/rivune", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		InterfaceLanguage string `json:"interfaceLanguage"`
	}
	decodeResponse(t, response, &body)
	if body.InterfaceLanguage != "en" {
		t.Fatalf("interface language = %q, want en", body.InterfaceLanguage)
	}
}

func TestSetupCreatesInitialResources(t *testing.T) {
	service := &fakeInstanceService{setupResult: instance.SetupResult{
		InstanceID: "a2925e24-8619-49fd-bd97-8c677ad78b61",
		UserID:     "65ec9a0d-0f02-49e7-9862-c2682ec90e6c",
		ProfileID:  "847316a3-1845-477a-884f-16b1ec6e316f",
	}}
	api := testAPI(service)
	requestBody := []byte(`{"instanceName":"Rivune Home","admin":{"username":"admin","password":"correct-horse-battery-staple"},"profileName":"Admin"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer bootstrap-secret")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.setupToken != "bootstrap-secret" {
		t.Fatalf("expected bearer token to reach setup service")
	}
	if service.setupInput.Username != "admin" || service.setupInput.ProfileName != "Admin" {
		t.Fatalf("unexpected setup input: %+v", service.setupInput)
	}
	var body map[string]map[string]string
	decodeResponse(t, response, &body)
	if body["instance"]["id"] != service.setupResult.InstanceID || body["admin"]["id"] != service.setupResult.UserID || body["profile"]["id"] != service.setupResult.ProfileID {
		t.Fatalf("unexpected setup response: %+v", body)
	}
}

func TestSetupMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid token", err: instance.ErrInvalidSetupToken, status: http.StatusUnauthorized, code: "invalid_setup_token"},
		{name: "already configured", err: instance.ErrAlreadyConfigured, status: http.StatusConflict, code: "already_configured"},
		{name: "missing server token", err: instance.ErrSetupUnavailable, status: http.StatusServiceUnavailable, code: "setup_unavailable"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeInstanceService{setupErr: test.err}
			api := testAPI(service)
			requestBody := []byte(`{"instanceName":"Rivune","admin":{"username":"admin","password":"correct-horse-battery-staple"},"profileName":"Admin"}`)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewReader(requestBody))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("expected status %d, got %d", test.status, response.Code)
			}
			var body errorEnvelope
			decodeResponse(t, response, &body)
			if body.Error.Code != test.code {
				t.Fatalf("expected error code %q, got %q", test.code, body.Error.Code)
			}
		})
	}
}

func TestSetupRejectsUnknownFields(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(`{"instanceName":"Rivune","unknown":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", response.Code)
	}
}

func TestSuccessfulSetupPurgesInProcessDemoSessions(t *testing.T) {
	service := &fakeInstanceService{
		info:        instance.Info{Name: "Rivune", SetupRequired: true},
		setupResult: instance.SetupResult{InstanceID: "instance", UserID: "user", ProfileID: "profile"},
	}
	api := testAPI(service)
	handler := api.Handler()
	cookie := startDemoSession(t, handler)

	setup := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(`{"instanceName":"Rivune","admin":{"username":"admin","password":"correct-horse-battery-staple"},"profileName":"Admin"}`))
	setup.Header.Set("Content-Type", "application/json")
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, setup)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("setup status = %d: %s", setupResponse.Code, setupResponse.Body.String())
	}

	resume := httptest.NewRequest(http.MethodGet, "/api/v1/demo/session", nil)
	resume.AddCookie(cookie)
	resumeResponse := httptest.NewRecorder()
	handler.ServeHTTP(resumeResponse, resume)
	if resumeResponse.Code != http.StatusGone {
		t.Fatalf("purged demo status = %d, want %d: %s", resumeResponse.Code, http.StatusGone, resumeResponse.Body.String())
	}
}

func TestFailedSetupKeepsDemoSession(t *testing.T) {
	service := &fakeInstanceService{
		info:     instance.Info{Name: "Rivune", SetupRequired: true},
		setupErr: instance.ErrInvalidSetupToken,
	}
	api := testAPI(service)
	handler := api.Handler()
	cookie := startDemoSession(t, handler)

	setup := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(`{"instanceName":"Rivune","admin":{"username":"admin","password":"correct-horse-battery-staple"},"profileName":"Admin"}`))
	setup.Header.Set("Content-Type", "application/json")
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, setup)
	if setupResponse.Code != http.StatusUnauthorized {
		t.Fatalf("failed setup status = %d: %s", setupResponse.Code, setupResponse.Body.String())
	}

	resume := httptest.NewRequest(http.MethodGet, "/api/v1/demo/session", nil)
	resume.AddCookie(cookie)
	resumeResponse := httptest.NewRecorder()
	handler.ServeHTTP(resumeResponse, resume)
	if resumeResponse.Code != http.StatusOK {
		t.Fatalf("demo after failed setup = %d: %s", resumeResponse.Code, resumeResponse.Body.String())
	}
}

func startDemoSession(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/demo/sessions", nil))
	if response.Code != http.StatusCreated {
		t.Fatalf("start demo = %d: %s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == demo.CookieName {
			return cookie
		}
	}
	t.Fatal("demo cookie missing")
	return nil
}

func testAPI(service instanceService) *API {
	api := &API{
		config:                config.Config{PublicURL: "https://media.example"},
		instances:             service,
		settings:              &fakeSettingsService{instance: settings.Layer{SchemaVersion: 1}},
		logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		version:               "test",
		credentialAdmission:   newCredentialAdmission(),
		usernameAdmission:     newCredentialUsernameAdmission(),
		deviceCodeAdmission:   newDeviceCodeAdmission(),
		calendarFeedAdmission: newCalendarFeedAdmission(),
	}
	api.demo = demo.New(service, demo.Options{})
	return api
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func TestTrackingOutboxCapacityHasRetryableHTTPContract(t *testing.T) {
	response := httptest.NewRecorder()
	if !writeTrackingError(&API{}, response, tracking.ErrOutboxCapacity, "update tracking preferences") {
		t.Fatal("tracking capacity error was not handled")
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("tracking capacity status = %d, want 503", response.Code)
	}
	if response.Header().Get("Retry-After") != "5" {
		t.Fatalf("tracking capacity Retry-After = %q, want 5", response.Header().Get("Retry-After"))
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "tracking_sync_capacity" {
		t.Fatalf("tracking capacity code = %q, want tracking_sync_capacity", body.Error.Code)
	}
}
