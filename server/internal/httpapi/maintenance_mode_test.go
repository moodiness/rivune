package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/settings"
)

func TestMaintenanceModeEnforcementBoundaries(t *testing.T) {
	message := "Upgrading the library"
	tests := []struct {
		name                   string
		role                   string
		activeProfileCanManage bool
		method                 string
		path                   string
		enabled                bool
		wantCode               int
		wantError              string
	}{
		{name: "viewer profile application request is blocked", role: "admin", path: "/api/v1/collections", enabled: true, wantCode: http.StatusServiceUnavailable, wantError: "maintenance_mode"},
		{name: "account recovery remains available", role: "member", path: "/api/v1/auth/me", enabled: true, wantCode: http.StatusNoContent},
		{name: "profile listing remains available", role: "member", path: "/api/v1/profiles", enabled: true, wantCode: http.StatusNoContent},
		{name: "profile selection remains available for handler validation", role: "member", method: http.MethodPost, path: "/api/v1/profiles/profile-id/select", enabled: true, wantCode: http.StatusNoContent},
		{name: "logout remains available", role: "member", path: "/api/v1/auth/logout", enabled: true, wantCode: http.StatusNoContent},
		{name: "manager profile application request remains available", role: "member", activeProfileCanManage: true, path: "/api/v1/collections", enabled: true, wantCode: http.StatusNoContent},
		{name: "viewer profile stays blocked for administrator account", role: "admin", path: "/api/v1/collections", enabled: true, wantCode: http.StatusServiceUnavailable, wantError: "maintenance_mode"},
		{name: "operations remain available for administrator account with viewer profile", role: "admin", path: "/api/v1/operations", enabled: true, wantCode: http.StatusNoContent},
		{name: "viewer profile recovers after disable", role: "member", path: "/api/v1/collections", enabled: false, wantCode: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := testAPI(&fakeInstanceService{})
			api.auth = &fakeAuthService{principal: auth.Principal{Role: test.role, ActiveProfileCanManage: test.activeProfileCanManage}}
			api.settings = &fakeSettingsService{maintenance: settings.Maintenance{Enabled: test.enabled, Message: &message}}
			handler := api.requireAuthentication(func(w http.ResponseWriter, _ *http.Request, _ auth.Principal) {
				w.WriteHeader(http.StatusNoContent)
			})
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			request := httptest.NewRequest(method, test.path, nil)
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantCode {
				t.Fatalf("expected status %d, got %d: %s", test.wantCode, response.Code, response.Body.String())
			}
			if test.wantError != "" {
				var body errorEnvelope
				decodeResponse(t, response, &body)
				if body.Error.Code != test.wantError || body.Error.Message != defaultMaintenanceMessage {
					t.Fatalf("unexpected maintenance error %#v", body.Error)
				}
				if body.Error.PublicMessage == nil || *body.Error.PublicMessage != message {
					t.Fatalf("unexpected public maintenance message %#v", body.Error.PublicMessage)
				}
				if response.Header().Get("Retry-After") != "5" {
					t.Fatalf("expected Retry-After 5, got %q", response.Header().Get("Retry-After"))
				}
			}
		})
	}
}

func TestMaintenanceAllowsAuthenticationForProfileSelection(t *testing.T) {
	now := time.Now().UTC()
	tokens := auth.TokenPair{
		AccessToken:      "rivune_at_maintenance",
		AccessExpiresAt:  now.Add(15 * time.Minute),
		RefreshToken:     "rivune_rt_maintenance",
		RefreshExpiresAt: now.Add(30 * 24 * time.Hour),
		SessionID:        "maintenance-session",
		DeviceID:         "maintenance-device",
	}
	tests := []struct {
		name      string
		path      string
		body      string
		configure func(*fakeAuthService)
		serve     func(*API, http.ResponseWriter, *http.Request)
	}{
		{
			name:      "password login",
			path:      "/api/v1/auth/login",
			body:      `{"username":"member","password":"secret","device":{"name":"Browser","platform":"web"}}`,
			configure: func(service *fakeAuthService) { service.loginTokens = tokens },
			serve:     func(api *API, w http.ResponseWriter, r *http.Request) { api.login(w, r) },
		},
		{
			name:      "token refresh",
			path:      "/api/v1/auth/refresh",
			body:      `{"refreshToken":"rivune_rt_existing"}`,
			configure: func(service *fakeAuthService) { service.refreshTokens = tokens },
			serve:     func(api *API, w http.ResponseWriter, r *http.Request) { api.refresh(w, r) },
		},
		{
			name:      "device authorization exchange",
			path:      "/api/v1/auth/device-code/token",
			body:      `{"deviceCode":"rivune_dc_approved"}`,
			configure: func(service *fakeAuthService) { service.exchangeTokens = tokens },
			serve:     func(api *API, w http.ResponseWriter, r *http.Request) { api.exchangeDeviceAuthorization(w, r) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAuthService{}
			test.configure(service)
			message := "Upgrading the library"
			api := testAPI(&fakeInstanceService{})
			api.auth = service
			api.settings = &fakeSettingsService{maintenance: settings.Maintenance{Enabled: true, Message: &message}}
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			test.serve(api, response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("expected authentication to reach profile selection, got %d: %s", response.Code, response.Body.String())
			}
			if service.authenticateToken != "" || service.logoutPrincipal.SessionID != "" {
				t.Fatalf("issued session was unexpectedly inspected or revoked")
			}
		})
	}
}

func TestMaintenanceBlocksPlaybackAssets(t *testing.T) {
	message := "Upgrading the library"
	playbackService := &fakePlaybackService{}
	api := testAPI(&fakeInstanceService{})
	api.playback = playbackService
	api.settings = &fakeSettingsService{maintenance: settings.Maintenance{Enabled: true, Message: &message}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/session/assets/master", nil)
	response := httptest.NewRecorder()

	api.playbackAsset(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected maintenance response, got %d: %s", response.Code, response.Body.String())
	}
	if playbackService.proxyCalls != 0 {
		t.Fatalf("playback proxy was called %d times", playbackService.proxyCalls)
	}
}

func TestMaintenanceModeResponseMatchesOpenAPI(t *testing.T) {
	message := "Upgrading the library"
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{Role: "member"}}
	api.settings = &fakeSettingsService{maintenance: settings.Maintenance{Enabled: true, Message: &message}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/collections", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := serveContractRequest(t, api, request, http.StatusServiceUnavailable)

	validateContractResponse(t, loadOpenAPIContract(t), "/collections", nil, request, response)
}
