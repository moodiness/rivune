package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/config"
	"github.com/moodiness/rivune/server/internal/instance"
)

type fakeInstanceService struct {
	info        instance.Info
	infoErr     error
	setupResult instance.SetupResult
	setupErr    error
	setupToken  string
	setupInput  instance.SetupInput
}

func (f *fakeInstanceService) Info(context.Context) (instance.Info, error) {
	return f.info, f.infoErr
}

func (f *fakeInstanceService) Setup(_ context.Context, token string, input instance.SetupInput) (instance.SetupResult, error) {
	f.setupToken = token
	f.setupInput = input
	return f.setupResult, f.setupErr
}

func TestDiscoveryDescribesUnconfiguredServer(t *testing.T) {
	service := &fakeInstanceService{info: instance.Info{Name: "Rivune", SetupRequired: true}}
	api := testAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/.well-known/rivune", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	var body struct {
		Name            string `json:"name"`
		ProtocolVersion int    `json:"protocolVersion"`
		APIBaseURL      string `json:"apiBaseUrl"`
		SetupRequired   bool   `json:"setupRequired"`
	}
	decodeResponse(t, response, &body)
	if body.Name != "Rivune" || body.ProtocolVersion != 10 || body.APIBaseURL != "https://media.example/api/v1" || !body.SetupRequired {
		t.Fatalf("unexpected discovery response: %+v", body)
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

func testAPI(service instanceService) *API {
	return &API{
		config:    config.Config{PublicURL: "https://media.example"},
		instances: service,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		version:   "test",
	}
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
