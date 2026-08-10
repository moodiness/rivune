package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/settings"
)

type fakeIntegrationSettingsService struct {
	updates int
	reads   int
	audits  int
	patch   settings.IntegrationCredentialsPatch
	cursor  *int64
	limit   int
}

func (service *fakeIntegrationSettingsService) IntegrationStatus(context.Context, auth.Principal) (settings.IntegrationStatus, error) {
	service.reads++
	return settings.IntegrationStatus{}, nil
}

func (service *fakeIntegrationSettingsService) UpdateIntegrationCredentials(_ context.Context, _ auth.Principal, patch settings.IntegrationCredentialsPatch) (settings.IntegrationStatus, error) {
	service.updates++
	service.patch = patch
	return settings.IntegrationStatus{Revision: 7}, nil
}

func (service *fakeIntegrationSettingsService) ListAuditEvents(_ context.Context, _ auth.Principal, cursor *int64, limit int) (settings.AuditPage, error) {
	service.audits++
	service.cursor = cursor
	service.limit = limit
	return settings.AuditPage{Events: []settings.AuditEvent{}}, nil
}

func TestIntegrationPatchRejectsNonAdministratorBeforeDecodingSecret(t *testing.T) {
	service := &fakeIntegrationSettingsService{}
	api := &API{integrationConfiguration: service}
	secret := "credential-that-must-not-leak"
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings/integrations", strings.NewReader(`{"tmdbAccessToken":"`+secret+`"}`))
	response := httptest.NewRecorder()
	api.updateIntegrationSettings(response, request, auth.Principal{Role: "member"})
	if response.Code != http.StatusForbidden || service.updates != 0 {
		t.Fatalf("non-admin status=%d updates=%d", response.Code, service.updates)
	}
	if strings.Contains(response.Body.String(), secret) {
		t.Fatal("forbidden response echoed credential material")
	}
}

func TestIntegrationPatchDecodesNullableSecretsWithoutEchoingThem(t *testing.T) {
	service := &fakeIntegrationSettingsService{}
	api := &API{integrationConfiguration: service}
	secret := "credential-that-must-not-leak"
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings/integrations", strings.NewReader(`{"tmdbAccessToken":"`+secret+`","tvdbPin":null}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.updateIntegrationSettings(response, request, auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator})
	if response.Code != http.StatusOK || service.updates != 1 {
		t.Fatalf("admin status=%d updates=%d body=%s", response.Code, service.updates, response.Body.String())
	}
	if !service.patch.TMDBAccessToken.Set || service.patch.TMDBAccessToken.Value == nil || *service.patch.TMDBAccessToken.Value != secret || !service.patch.TVDBPIN.Set || service.patch.TVDBPIN.Value != nil {
		t.Fatal("decoded patch did not preserve replace/clear semantics")
	}
	if strings.Contains(response.Body.String(), secret) {
		t.Fatal("successful response echoed credential material")
	}
}

func TestConfigurationAuditParsesStrictKeysetPage(t *testing.T) {
	service := &fakeIntegrationSettingsService{}
	api := &API{integrationConfiguration: service}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/settings/audit?cursor=81&limit=25", nil)
	response := httptest.NewRecorder()
	api.configurationAudit(response, request, auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator})
	if response.Code != http.StatusOK || service.audits != 1 || service.cursor == nil || *service.cursor != 81 || service.limit != 25 {
		t.Fatalf("audit status=%d calls=%d cursor=%v limit=%d", response.Code, service.audits, service.cursor, service.limit)
	}

	invalid := httptest.NewRecorder()
	api.configurationAudit(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/settings/audit?limit=101", nil), auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator})
	if invalid.Code != http.StatusBadRequest || service.audits != 1 {
		t.Fatalf("invalid audit status=%d calls=%d", invalid.Code, service.audits)
	}
}
