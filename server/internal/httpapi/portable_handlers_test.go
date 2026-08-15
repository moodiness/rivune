package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/portable"
)

type fakePortableService struct {
	exports  int
	imports  int
	document portable.Document
	report   portable.ImportReport
	err      error
}

func (service *fakePortableService) Export(_ context.Context, _ auth.Principal, _ string) (portable.Document, error) {
	service.exports++
	return service.document, service.err
}
func (service *fakePortableService) Import(_ context.Context, _ auth.Principal, _ string, _ portable.Document) (portable.ImportReport, error) {
	service.imports++
	return service.report, service.err
}

func portableHandlerAPI(service *fakePortableService, principal auth.Principal) *API {
	return &API{portable: service, auth: &fakeAuthService{principal: principal}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestProfileArchiveRequiresGlobalAdministratorBeforeReadingOrCallingService(t *testing.T) {
	service := &fakePortableService{}
	api := portableHandlerAPI(service, auth.Principal{UserID: "user", Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/11111111-1111-4111-8111-111111111111/archive/import", strings.NewReader(strings.Repeat("x", 1024)))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || service.imports != 0 {
		t.Fatalf("denied import = %d calls=%d body=%s", response.Code, service.imports, response.Body.String())
	}
}

func TestProfileArchiveExportIsNoStoreAttachmentAndCarriesExplicitAddonSecret(t *testing.T) {
	service := &fakePortableService{document: portable.Document{Version: 1, ExportedAt: time.Now().UTC(), Addons: []portable.Addon{{TransportURL: "https://addon.example/manifest.json?token=secret"}}}}
	principal := auth.Principal{UserID: "admin", Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}
	api := portableHandlerAPI(service, principal)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles/11111111-1111-4111-8111-111111111111/archive", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Disposition") != `attachment; filename="rivune-profile-archive.json"` || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "token=secret") {
		t.Fatalf("archive export = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestProfileArchiveImportStrictBodyAndBudget(t *testing.T) {
	principal := auth.Principal{UserID: "admin", Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}
	for _, test := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"unknown field", `{"version":1,"unknown":true}`, http.StatusBadRequest, "invalid_profile_archive"},
		{"missing nested required field", `{"version":1,"exportedAt":"2026-08-14T00:00:00Z","settings":{},"addons":[{"key":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","transportUrl":"https://addon.example/manifest.json","manifest":{},"position":0}],"collections":[],"titles":[],"library":[],"progress":[],"favorites":[],"userData":[],"trackingPreferences":[]}`, http.StatusBadRequest, "invalid_profile_archive"},
		{"oversize", strings.Repeat(" ", portable.MaximumDocumentBytes+1), http.StatusRequestEntityTooLarge, "profile_archive_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakePortableService{}
			api := portableHandlerAPI(service, principal)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/11111111-1111-4111-8111-111111111111/archive/import", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer access")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) || service.imports != 0 {
				t.Fatalf("response = %d calls=%d body=%s", response.Code, service.imports, response.Body.String())
			}
		})
	}
}
