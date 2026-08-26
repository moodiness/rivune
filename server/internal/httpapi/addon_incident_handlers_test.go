package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addonincident"
	"github.com/moodiness/rivune/server/internal/auth"
)

type fakeAddonIncidentService struct {
	list addonincident.List
	detail addonincident.Detail
	acknowledged addonincident.Incident
	listCalls, detailCalls, acknowledgeCalls int
	principal auth.Principal
	incidentID string
}

func (service *fakeAddonIncidentService) List(_ context.Context, principal auth.Principal) (addonincident.List, error) {
	service.listCalls++; service.principal = principal; return service.list, nil
}
func (service *fakeAddonIncidentService) Detail(_ context.Context, principal auth.Principal, id string) (addonincident.Detail, error) {
	service.detailCalls++; service.principal, service.incidentID = principal, id; return service.detail, nil
}
func (service *fakeAddonIncidentService) Acknowledge(_ context.Context, principal auth.Principal, id string) (addonincident.Incident, error) {
	service.acknowledgeCalls++; service.principal, service.incidentID = principal, id; return service.acknowledged, nil
}

func TestAddonIncidentRoutesRequireActiveProfileManagerBeforeService(t *testing.T) {
	profileID := "88000000-0000-4000-8000-000000000001"
	for _, principal := range []auth.Principal{
		{Role: "member", ActiveProfileID: &profileID},
		{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator},
	} {
		service := &fakeAddonIncidentService{}
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: principal}
		api.addonIncidents = service
		request := httptest.NewRequest(http.MethodGet, "/api/v1/operations/extension-incidents", nil)
		request.Header.Set("Authorization", "Bearer access")
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || service.listCalls != 0 {
			t.Fatalf("unauthorized response=%d service calls=%d", response.Code, service.listCalls)
		}
	}
}

func TestAddonIncidentRoutesExposeOnlySafeProfileScopedFields(t *testing.T) {
	profileID := "88000000-0000-4000-8000-000000000001"
	incidentID := "88000000-0000-4000-8000-000000000002"
	addonID := "88000000-0000-4000-8000-000000000003"
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	incident := addonincident.Incident{ID: incidentID, ProfileID: profileID, AddonID: addonID, AddonName: "Safe extension", Code: addonincident.CodeUnavailable, State: addonincident.StateOpen, Impact: addonincident.ImpactAvailability, OccurrenceCount: 2, FirstOccurredAt: now, LastOccurredAt: now, UpdatedAt: now}
	service := &fakeAddonIncidentService{list: addonincident.List{Incidents: []addonincident.Incident{incident}}, detail: addonincident.Detail{Incident: incident, Events: []addonincident.Event{{ID: 1, Type: "opened", Code: addonincident.CodeUnavailable, OccurredAt: now}}}, acknowledged: incident}
	principal := auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator, ActiveProfileID: &profileID, ActiveProfileCanManage: true}
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: principal}
	api.addonIncidents = service

	for _, route := range []struct{ method, path string; calls *int }{
		{http.MethodGet, "/api/v1/operations/extension-incidents", &service.listCalls},
		{http.MethodGet, "/api/v1/operations/extension-incidents/" + incidentID, &service.detailCalls},
		{http.MethodPost, "/api/v1/operations/extension-incidents/" + incidentID + "/acknowledgement", &service.acknowledgeCalls},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		request.Header.Set("Authorization", "Bearer access")
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK || *route.calls != 1 { t.Fatalf("%s %s response=%d calls=%d", route.method, route.path, response.Code, *route.calls) }
		lower := strings.ToLower(response.Body.String())
		for _, private := range []string{"transporturl", "https://", "token=", "authorization", "query", "response body", "raw error"} {
			if strings.Contains(lower, private) { t.Fatalf("incident response exposed %q: %s", private, response.Body.String()) }
		}
	}
}
