package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/settings"
)

func TestMaintenanceModeEnforcementBoundaries(t *testing.T) {
	message := "Upgrading the library"
	tests := []struct {
		name      string
		role      string
		path      string
		enabled   bool
		wantCode  int
		wantError string
	}{
		{name: "member application request is blocked", role: "member", path: "/api/v1/collections", enabled: true, wantCode: http.StatusServiceUnavailable, wantError: "maintenance_mode"},
		{name: "member session recovery remains available", role: "member", path: "/api/v1/auth/me", enabled: true, wantCode: http.StatusNoContent},
		{name: "admin application request remains available", role: "admin", path: "/api/v1/collections", enabled: true, wantCode: http.StatusNoContent},
		{name: "member request recovers after disable", role: "member", path: "/api/v1/collections", enabled: false, wantCode: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := testAPI(&fakeInstanceService{})
			api.auth = &fakeAuthService{principal: auth.Principal{Role: test.role}}
			api.settings = &fakeSettingsService{maintenance: settings.Maintenance{Enabled: test.enabled, Message: &message}}
			handler := api.requireAuthentication(func(w http.ResponseWriter, _ *http.Request, _ auth.Principal) {
				w.WriteHeader(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
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
