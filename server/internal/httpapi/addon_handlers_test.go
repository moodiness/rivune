package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

type fakeAddonService struct {
	installInput        addon.InstallInput
	installValue        addon.InstalledAddon
	installTransportURL string
	installErr          error
	verificationInput   addon.VerificationInput
	verificationAddon   string
	verificationValue   addon.AddonVerification
	verificationErr     error
	listValue           []addon.InstalledAddon
	listErr             error
	diagnosticsCalls    int
	diagnosticsValue    addon.Diagnostics
	diagnosticsErr      error
	managementID        string
	managementValue     addon.ManagedAddon
	managementErr       error
	removeID            string
	removeErr           error
	reorderInput        addon.ReorderInput
	reorderValue        []addon.InstalledAddon
	reorderErr          error
	refreshID           string
	refreshValue        addon.InstalledAddon
	refreshErr          error
	updateID            string
	updateInput         addon.UpdateAddonInput
	updateValue         addon.InstalledAddon
	updateTransportURL  string
	updateErr           error
	catalogs            []addon.CatalogDescriptor
	catalogsErr         error
	fetchAddonID        string
	fetchCalls          int
	fetchPath           addon.ResourcePath
	fetchValue          addon.ResourceResult
	fetchErr            error
	allPath             addon.ResourcePath
	allCalls            int
	allValue            addon.ResourceBatch
	allErr              error
	catalogType         string
	catalogExtra        []addon.ExtraValue
	addonCatalog        bool
	catalogValue        addon.ResourceBatch
	catalogErr          error
	searchType          string
	searchInput         addon.CatalogSearchInput
	searchValue         addon.ResourceBatch
	searchErr           error
}

type fakeCatalogArtworkPresenter struct {
	calls int
}

func (fake *fakeCatalogArtworkPresenter) LocalizeCatalogDescriptors(_ context.Context, values []addon.CatalogDescriptor) {
	fake.calls++
	for index := range values {
		if strings.HasPrefix(values[index].AddonLogoURL, "https://") {
			values[index].AddonLogoURL = "/api/v1/artwork/" + strings.Repeat("0", 64)
		} else {
			values[index].AddonLogoURL = ""
		}
	}
}

func (fake *fakeAddonService) Install(_ context.Context, _ auth.Principal, input addon.InstallInput) (addon.ManagedAddon, error) {
	fake.installInput = input
	return addon.ManagedAddon{InstalledAddon: fake.installValue, TransportURL: fake.installTransportURL}, fake.installErr
}

func (fake *fakeAddonService) VerifyCandidate(_ context.Context, _ auth.Principal, input addon.VerificationInput) (addon.AddonVerification, error) {
	fake.verificationInput = input
	return fake.verificationValue, fake.verificationErr
}

func (fake *fakeAddonService) VerifyInstalled(_ context.Context, _ auth.Principal, addonID string) (addon.AddonVerification, error) {
	fake.verificationAddon = addonID
	return fake.verificationValue, fake.verificationErr
}

func (fake *fakeAddonService) List(context.Context, auth.Principal) ([]addon.InstalledAddon, error) {
	return fake.listValue, fake.listErr
}

func (fake *fakeAddonService) Diagnostics(context.Context, auth.Principal) (addon.Diagnostics, error) {
	fake.diagnosticsCalls++
	return fake.diagnosticsValue, fake.diagnosticsErr
}

func (fake *fakeAddonService) Management(_ context.Context, _ auth.Principal, addonID string) (addon.ManagedAddon, error) {
	fake.managementID = addonID
	return fake.managementValue, fake.managementErr
}

func (fake *fakeAddonService) Remove(_ context.Context, _ auth.Principal, addonID string) error {
	fake.removeID = addonID
	return fake.removeErr
}

func (fake *fakeAddonService) Reorder(_ context.Context, _ auth.Principal, input addon.ReorderInput) ([]addon.InstalledAddon, error) {
	fake.reorderInput = input
	return fake.reorderValue, fake.reorderErr
}

func (fake *fakeAddonService) Refresh(_ context.Context, _ auth.Principal, addonID string) (addon.InstalledAddon, error) {
	fake.refreshID = addonID
	return fake.refreshValue, fake.refreshErr
}
func (fake *fakeAddonService) Update(_ context.Context, _ auth.Principal, addonID string, input addon.UpdateAddonInput) (addon.ManagedAddon, error) {
	fake.updateID = addonID
	fake.updateInput = input
	return addon.ManagedAddon{InstalledAddon: fake.updateValue, TransportURL: fake.updateTransportURL}, fake.updateErr
}

func (fake *fakeAddonService) Catalogs(context.Context, auth.Principal) ([]addon.CatalogDescriptor, error) {
	return fake.catalogs, fake.catalogsErr
}

func (fake *fakeAddonService) Fetch(_ context.Context, _ auth.Principal, addonID string, path addon.ResourcePath) (addon.ResourceResult, error) {
	fake.fetchCalls++
	fake.fetchAddonID = addonID
	fake.fetchPath = path
	return fake.fetchValue, fake.fetchErr
}

func (fake *fakeAddonService) FetchAll(_ context.Context, _ auth.Principal, path addon.ResourcePath) (addon.ResourceBatch, error) {
	fake.allCalls++
	fake.allPath = path
	return fake.allValue, fake.allErr
}

func (fake *fakeAddonService) SearchCatalogs(_ context.Context, _ auth.Principal, contentType string, input addon.CatalogSearchInput) (addon.ResourceBatch, error) {
	fake.searchType = contentType
	fake.searchInput = input
	return fake.searchValue, fake.searchErr
}

func (fake *fakeAddonService) FetchCatalogs(_ context.Context, _ auth.Principal, contentType string, extra []addon.ExtraValue, addonCatalog bool) (addon.ResourceBatch, error) {
	fake.catalogType = contentType
	fake.catalogExtra = extra
	fake.addonCatalog = addonCatalog
	return fake.catalogValue, fake.catalogErr
}

func TestAddonVerificationForwardsInputAndIsSafe(t *testing.T) {
	profileID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	categoryID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	service := &fakeAddonService{verificationValue: addon.AddonVerification{
		ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", Status: "passed", Summary: "ready",
		Checks:     []addon.VerificationCheck{{Code: "manifest_fetch", Status: "passed"}, {Code: "manifest_valid", Status: "passed"}, {Code: "catalog_probe", Status: "skipped"}},
		ProfileIDs: []string{profileID}, CategoryIDs: []string{categoryID}, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}}
	api := addonAPI(service)
	api.auth = &fakeAuthService{principal: auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/addons/verifications", bytes.NewBufferString(`{"transportUrl":"stremio://addon.example/config?token=private","profileIds":["`+profileID+`"],"categoryIds":["`+categoryID+`"]}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || service.verificationInput.TransportURL == "" {
		t.Fatalf("verification response=%d input=%+v body=%s", response.Code, service.verificationInput, response.Body.String())
	}
	for _, private := range []string{"transportUrl", "addon.example", "token=private"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("verification response exposed %q: %s", private, response.Body.String())
		}
	}
}

func TestAddonVerificationRequiresGlobalAdministratorBeforeService(t *testing.T) {
	service := &fakeAddonService{}
	api := addonAPI(service)
	api.auth = &fakeAuthService{principal: auth.Principal{Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/addons/verifications", strings.NewReader(`{"transportUrl":"https://addon.example/manifest.json"}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || service.verificationInput.TransportURL != "" {
		t.Fatalf("verification authorization response=%d input=%+v", response.Code, service.verificationInput)
	}
}

func TestInstallAddonUsesProfileScopedRegistry(t *testing.T) {
	installedAt := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	service := &fakeAddonService{installValue: addon.InstalledAddon{
		ID:          "11111111-1111-4111-8111-111111111111",
		Manifest:    []byte(`{"id":"org.example","version":"1.0.0","name":"Example","types":["movie"],"resources":["stream"],"catalogs":[]}`),
		InstalledAt: installedAt, UpdatedAt: installedAt,
	}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/addons", bytes.NewBufferString(`{"verificationId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.installInput.VerificationID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("unexpected install input: %+v", service.installInput)
	}
}

func TestUpdateAddonForwardsCompleteInput(t *testing.T) {
	service := &fakeAddonService{updateValue: addon.InstalledAddon{
		ID:         "11111111-1111-4111-8111-111111111111",
		ProfileIDs: []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
	}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/addons/11111111-1111-4111-8111-111111111111", bytes.NewBufferString(`{"transportUrl":"https://new-addon.example","profileIds":["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"],"enabled":false}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body addon.InstalledAddon
	decodeResponse(t, response, &body)
	if body.ID != service.updateValue.ID {
		t.Fatalf("unexpected update response: %+v", body)
	}
	if service.updateID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected addon ID: %q", service.updateID)
	}
	if service.updateInput.TransportURL == nil || *service.updateInput.TransportURL != "https://new-addon.example" {
		t.Fatalf("unexpected transport URL: %v", service.updateInput.TransportURL)
	}
	if service.updateInput.Enabled == nil || *service.updateInput.Enabled {
		t.Fatalf("unexpected enabled state: %v", service.updateInput.Enabled)
	}
	if len(service.updateInput.ProfileIDs) != 1 || service.updateInput.ProfileIDs[0] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("unexpected profile IDs: %+v", service.updateInput.ProfileIDs)
	}
}

func TestUpdateAddonAllowsAssignmentOnlyInput(t *testing.T) {
	service := &fakeAddonService{updateValue: addon.InstalledAddon{
		ID:         "11111111-1111-4111-8111-111111111111",
		ProfileIDs: []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
	}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/addons/11111111-1111-4111-8111-111111111111", bytes.NewBufferString(`{"profileIds":["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"]}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.updateInput.TransportURL != nil {
		t.Fatalf("assignment-only update unexpectedly supplied transport URL: %v", service.updateInput.TransportURL)
	}
	if service.updateInput.Enabled != nil {
		t.Fatalf("assignment-only update unexpectedly supplied enabled state: %v", service.updateInput.Enabled)
	}
	if len(service.updateInput.ProfileIDs) != 1 || service.updateInput.ProfileIDs[0] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("unexpected profile IDs: %+v", service.updateInput.ProfileIDs)
	}
}

func TestUpdateAddonForwardsOptionalEnabledExactly(t *testing.T) {
	const addonID = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name        string
		body        string
		wantPresent bool
		wantEnabled bool
	}{
		{name: "enable", body: `{"profileIds":[],"enabled":true}`, wantPresent: true, wantEnabled: true},
		{name: "disable", body: `{"profileIds":[],"enabled":false}`, wantPresent: true, wantEnabled: false},
		{name: "preserve", body: `{"profileIds":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAddonService{updateValue: addon.InstalledAddon{ID: addonID}}
			api := addonAPI(service)
			request := httptest.NewRequest(http.MethodPut, "/api/v1/addons/"+addonID, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer access")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
			}
			if (service.updateInput.Enabled != nil) != test.wantPresent {
				t.Fatalf("enabled presence = %v, want %v", service.updateInput.Enabled != nil, test.wantPresent)
			}
			if service.updateInput.Enabled != nil && *service.updateInput.Enabled != test.wantEnabled {
				t.Fatalf("enabled = %v, want %v", *service.updateInput.Enabled, test.wantEnabled)
			}
		})
	}
}

func TestRefreshAddonPreservesSharedMutationOutcome(t *testing.T) {
	const addonID = "11111111-1111-4111-8111-111111111111"
	service := &fakeAddonService{refreshValue: addon.InstalledAddon{
		ID: addonID, Manifest: []byte(`{"id":"org.example","version":"2.0.0"}`),
	}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/addons/"+addonID+"/refresh", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.refreshID != addonID {
		t.Fatalf("refresh addon ID = %q, want %q", service.refreshID, addonID)
	}
	var refreshed addon.InstalledAddon
	decodeResponse(t, response, &refreshed)
	if refreshed.ID != addonID {
		t.Fatalf("refresh response addon ID = %q, want %q", refreshed.ID, addonID)
	}

	service.refreshErr = errors.Join(errors.New("wrapped"), addon.ErrForbidden)
	forbiddenRequest := httptest.NewRequest(http.MethodPost, "/api/v1/addons/"+addonID+"/refresh", nil)
	forbiddenRequest.Header.Set("Authorization", "Bearer access")
	forbiddenResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(forbiddenResponse, forbiddenRequest)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", forbiddenResponse.Code, forbiddenResponse.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, forbiddenResponse, &body)
	if body.Error.Code != "addon_forbidden" {
		t.Fatalf("refresh error code = %q, want addon_forbidden", body.Error.Code)
	}
}

func TestReorderAddonsReturnsStableForbiddenResponse(t *testing.T) {
	const addonID = "11111111-1111-4111-8111-111111111111"
	service := &fakeAddonService{reorderErr: errors.Join(errors.New("wrapped"), addon.ErrForbidden)}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/addons/order", strings.NewReader(`{"addonIds":["`+addonID+`"]}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", response.Code, response.Body.String())
	}
	if len(service.reorderInput.AddonIDs) != 1 || service.reorderInput.AddonIDs[0] != addonID {
		t.Fatalf("reorder input = %+v, want addon %q", service.reorderInput, addonID)
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "addon_forbidden" {
		t.Fatalf("reorder error code = %q, want addon_forbidden", body.Error.Code)
	}
}

func TestAddonDiagnosticsRequiresGlobalAdministratorBeforeService(t *testing.T) {
	categoryID := "22222222-2222-4222-8222-222222222222"
	for _, principal := range []auth.Principal{
		{Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID},
		{Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID},
	} {
		service := &fakeAddonService{}
		api := addonAPI(service)
		api.auth = &fakeAuthService{principal: principal}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/diagnostics", nil)
		request.Header.Set("Authorization", "Bearer access")
		response := httptest.NewRecorder()

		api.Handler().ServeHTTP(response, request)

		if response.Code != http.StatusForbidden {
			t.Fatalf("non-global diagnostics status = %d, want 403", response.Code)
		}
		if service.diagnosticsCalls != 0 {
			t.Fatal("non-global diagnostics request reached the service")
		}
	}
}

func TestAddonDiagnosticsReturnsOnlySafeStructuredDetails(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	latency := int64(7)
	service := &fakeAddonService{diagnosticsValue: addon.Diagnostics{
		ObservedSince: now.Add(-time.Hour),
		Diagnostics: []addon.DiagnosticEntry{{
			AddonID:              "11111111-1111-4111-8111-111111111111",
			State:                addon.DiagnosticStateDegraded,
			LastSuccessAt:        &now,
			ApproximateLatencyMS: &latency,
			LastError:            &addon.DiagnosticLastError{Code: addon.DiagnosticErrorUnavailable, At: now},
			Capabilities:         addon.AddonCapabilities{Resources: []string{"catalog"}, Search: true},
		}},
	}}
	api := addonAPI(service)
	api.auth = &fakeAuthService{principal: auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/diagnostics", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || service.diagnosticsCalls != 1 {
		t.Fatalf("global diagnostics response = %d with %d service calls", response.Code, service.diagnosticsCalls)
	}
	serialized := response.Body.String()
	for _, private := range []string{"transportUrl", "provider.example", "manifest.json", "token=", "HTTP 503", "connection refused"} {
		if strings.Contains(serialized, private) {
			t.Fatalf("diagnostics response exposed private detail %q: %s", private, serialized)
		}
	}
}

func TestAddonManagementReturnsExactTransportOnlyToGlobalAdministrator(t *testing.T) {
	const (
		addonID      = "11111111-1111-4111-8111-111111111111"
		profileID    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		categoryID   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		transportURL = "https://provider.example/configured/private/manifest.json?token=management-secret"
	)
	managed := addon.ManagedAddon{
		InstalledAddon: addon.InstalledAddon{ID: addonID, Manifest: json.RawMessage(`{"id":"org.example","name":"Example"}`), Enabled: false, ProfileIDs: []string{profileID}, CategoryIDs: []string{categoryID}},
		TransportURL:   transportURL,
	}

	service := &fakeAddonService{managementValue: managed}
	api := addonAPI(service)
	api.auth = &fakeAuthService{principal: auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/"+addonID+"/management", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("global management status = %d, want 200", response.Code)
	}
	var body addon.ManagedAddon
	decodeResponse(t, response, &body)
	if body.TransportURL != "" || strings.Contains(response.Body.String(), transportURL) {
		t.Fatal("management response exposed the stored transport URL")
	}
	if !reflect.DeepEqual(body.ProfileIDs, []string{profileID}) || !reflect.DeepEqual(body.CategoryIDs, []string{categoryID}) {
		t.Fatalf("management assignments = profileIds:%#v categoryIds:%#v", body.ProfileIDs, body.CategoryIDs)
	}
	if body.Enabled {
		t.Fatal("disabled management response unexpectedly serialized as enabled")
	}
	if !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("disabled management response omitted enabled:false: %s", response.Body.String())
	}
	if service.managementID != addonID {
		t.Fatalf("management addon ID = %q, want %q", service.managementID, addonID)
	}

	delegatedCategoryID := "22222222-2222-4222-8222-222222222222"
	for _, principal := range []auth.Principal{
		{Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &delegatedCategoryID},
		{Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &delegatedCategoryID},
	} {
		service := &fakeAddonService{managementValue: managed}
		api := addonAPI(service)
		api.auth = &fakeAuthService{principal: principal}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/"+addonID+"/management", nil)
		request.Header.Set("Authorization", "Bearer access")
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("non-global management status = %d, want 403", response.Code)
		}
		if service.managementID != "" {
			t.Fatal("non-global management request reached the service")
		}
		if strings.Contains(response.Body.String(), "transportUrl") || strings.Contains(response.Body.String(), "management-secret") {
			t.Fatal("non-global management response exposed transport details")
		}
	}
}

func TestAddonManagementMutationsUseManagedAddonResponse(t *testing.T) {
	const (
		addonID      = "11111111-1111-4111-8111-111111111111"
		transportURL = "https://provider.example/configured/private/manifest.json?token=mutation-secret"
	)
	installed := addon.InstalledAddon{ID: addonID, Manifest: json.RawMessage(`{"id":"org.example","name":"Example"}`)}
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		setup  func(*fakeAddonService)
	}{
		{name: "install", method: http.MethodPost, path: "/api/v1/addons", body: `{"verificationId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`, setup: func(service *fakeAddonService) {
			service.installValue = installed
			service.installTransportURL = transportURL
		}},
		{name: "update", method: http.MethodPut, path: "/api/v1/addons/" + addonID, body: `{"profileIds":[]}`, setup: func(service *fakeAddonService) {
			service.updateValue = installed
			service.updateTransportURL = transportURL
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAddonService{}
			test.setup(service)
			api := addonAPI(service)
			api.auth = &fakeAuthService{principal: auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}}
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer access")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
				t.Fatalf("management mutation status = %d", response.Code)
			}
			var body addon.ManagedAddon
			decodeResponse(t, response, &body)
			if body.TransportURL != "" || strings.Contains(response.Body.String(), transportURL) {
				t.Fatal("management mutation exposed the stored transport URL")
			}
		})
	}
}

func TestDelegatedAddonMutationResponseRedactsTransportURL(t *testing.T) {
	const addonID = "11111111-1111-4111-8111-111111111111"
	service := &fakeAddonService{
		updateValue:        addon.InstalledAddon{ID: addonID, Manifest: json.RawMessage(`{"id":"org.example","name":"Example"}`)},
		updateTransportURL: "https://provider.example/configured/private/manifest.json?token=delegated-secret",
	}
	categoryID := "22222222-2222-4222-8222-222222222222"
	api := addonAPI(service)
	api.auth = &fakeAuthService{principal: auth.Principal{Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID}}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/addons/"+addonID, strings.NewReader(`{"profileIds":[]}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("delegated mutation status = %d, want 200", response.Code)
	}
	if strings.Contains(response.Body.String(), "transportUrl") || strings.Contains(response.Body.String(), "delegated-secret") {
		t.Fatal("delegated mutation response exposed transport details")
	}
}

func TestDisabledAddonListSerializesEnabledFalse(t *testing.T) {
	const (
		addonID    = "11111111-1111-4111-8111-111111111111"
		profileID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		categoryID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	)
	service := &fakeAddonService{listValue: []addon.InstalledAddon{{
		ID: addonID, Manifest: json.RawMessage(`{"id":"org.example","name":"Example"}`), Enabled: false,
		ProfileIDs: []string{profileID}, CategoryIDs: []string{categoryID},
	}}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/addons", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("disabled addon list status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body struct {
		Addons []addon.InstalledAddon `json:"addons"`
	}
	decodeResponse(t, response, &body)
	if len(body.Addons) != 1 || !reflect.DeepEqual(body.Addons[0].ProfileIDs, []string{profileID}) || !reflect.DeepEqual(body.Addons[0].CategoryIDs, []string{categoryID}) {
		t.Fatalf("list assignments were not serialized separately: %+v", body.Addons)
	}
	if !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("disabled addon list omitted enabled:false: %s", response.Body.String())
	}
}

func TestNonManagementInstalledAddonResponsesNeverExposeTransportURL(t *testing.T) {
	const addonID = "11111111-1111-4111-8111-111111111111"
	installed := addon.InstalledAddon{
		ID:       addonID,
		Manifest: json.RawMessage(`{"id":"org.example","name":"Example"}`),
	}
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		setup  func(*fakeAddonService)
	}{
		{name: "list", method: http.MethodGet, path: "/api/v1/addons", setup: func(service *fakeAddonService) {
			service.listValue = []addon.InstalledAddon{installed}
		}},
		{name: "reorder", method: http.MethodPut, path: "/api/v1/addons/order", body: `{"addonIds":["` + addonID + `"]}`, setup: func(service *fakeAddonService) {
			service.reorderValue = []addon.InstalledAddon{installed}
		}},
		{name: "refresh", method: http.MethodPost, path: "/api/v1/addons/" + addonID + "/refresh", setup: func(service *fakeAddonService) {
			service.refreshValue = installed
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAddonService{}
			test.setup(service)
			api := addonAPI(service)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer access")
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
				t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
			}
			serialized := response.Body.String()
			if strings.Contains(serialized, "transportUrl") || strings.Contains(serialized, "request-secret") || strings.Contains(serialized, "/private/") {
				t.Fatalf("response exposed addon transport: %s", serialized)
			}
		})
	}
}

func TestAddonCatalogDescriptorsExposeNamesInServiceOrder(t *testing.T) {
	service := &fakeAddonService{catalogs: []addon.CatalogDescriptor{
		{
			AddonID: "11111111-1111-4111-8111-111111111111", AddonName: "First Add-on", AddonLogoURL: "https://origin.invalid/private-first-logo.png", ManifestID: "org.example.first",
			Position: 0, Catalog: addon.ManifestCatalog{Type: "movie", ID: "featured", Name: "Featured"}, Searchable: true,
		},
		{
			AddonID: "11111111-1111-4111-8111-111111111111", AddonName: "First Add-on", AddonLogoURL: "https://origin.invalid/private-first-logo.png", ManifestID: "org.example.first",
			Position: 0, Catalog: addon.ManifestCatalog{Type: "all", ID: "community", Name: "Community"}, AddonCatalog: true,
		},
		{
			AddonID: "22222222-2222-4222-8222-222222222222", AddonName: "Second Add-on", AddonLogoURL: "invalid logo", ManifestID: "org.example.second",
			Position: 1, Catalog: addon.ManifestCatalog{Type: "series", ID: "recent", Name: "Recent"},
		},
	}}
	presenter := &fakeCatalogArtworkPresenter{}
	api := addonAPI(service)
	api.catalogArtwork = presenter
	request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/catalogs", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body struct {
		Catalogs []addon.CatalogDescriptor `json:"catalogs"`
	}
	decodeResponse(t, response, &body)
	if presenter.calls != 1 {
		t.Fatalf("catalog artwork presenter calls = %d, want 1", presenter.calls)
	}
	if len(body.Catalogs) != 3 {
		t.Fatalf("catalog count = %d, want 3: %+v", len(body.Catalogs), body.Catalogs)
	}
	if body.Catalogs[0].AddonName != "First Add-on" || body.Catalogs[0].Catalog.ID != "featured" || body.Catalogs[0].AddonCatalog {
		t.Fatalf("regular descriptor = %+v", body.Catalogs[0])
	}
	if body.Catalogs[0].AddonLogoURL != "/api/v1/artwork/"+strings.Repeat("0", 64) || strings.Contains(response.Body.String(), "origin.invalid") {
		t.Fatalf("regular descriptor exposed an unlocalized logo: %s", response.Body.String())
	}
	if body.Catalogs[1].AddonName != "First Add-on" || body.Catalogs[1].Catalog.ID != "community" || !body.Catalogs[1].AddonCatalog {
		t.Fatalf("add-on catalog descriptor = %+v", body.Catalogs[1])
	}
	if body.Catalogs[2].AddonName != "Second Add-on" || body.Catalogs[2].Catalog.ID != "recent" {
		t.Fatalf("second add-on descriptor = %+v", body.Catalogs[2])
	}
	if body.Catalogs[2].AddonLogoURL != "" || strings.Contains(response.Body.String(), "invalid logo") {
		t.Fatalf("invalid descriptor logo did not fail closed: %s", response.Body.String())
	}
}

func TestAddonResourceRoutePreservesOpaqueIDAndRepeatedExtras(t *testing.T) {
	service := &fakeAddonService{fetchValue: addon.ResourceResult{Resource: "meta", Payload: []byte(`{"meta":{"id":"kitsu:anime/42"}}`)}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/11111111-1111-4111-8111-111111111111/resource/meta/anime-special/kitsu%3Aanime%2F42%3Aepisode%2F7?genre=Sci%20Fi&skip=0&genre=Drama%2FAction&limit=100&limit=24&custom=a%2Bb", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"id":"kitsu:anime/42"`) {
		t.Fatalf("meta payload was not returned: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "transportUrl") {
		t.Fatalf("resource response exposed addon transport URL: %s", response.Body.String())
	}
	if service.fetchAddonID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected addon ID: %q", service.fetchAddonID)
	}
	if service.fetchPath.Resource != "meta" || service.fetchPath.Type != "anime-special" || service.fetchPath.ID != "kitsu:anime/42:episode/7" {
		t.Fatalf("opaque resource path changed: %+v", service.fetchPath)
	}
	wantExtra := []addon.ExtraValue{{Name: "genre", Value: "Sci Fi"}, {Name: "skip", Value: "0"}, {Name: "genre", Value: "Drama/Action"}, {Name: "limit", Value: "100"}, {Name: "limit", Value: "24"}, {Name: "custom", Value: "a+b"}}
	if len(service.fetchPath.Extra) != len(wantExtra) {
		t.Fatalf("unexpected extras: %+v", service.fetchPath.Extra)
	}
	for index := range wantExtra {
		if service.fetchPath.Extra[index] != wantExtra[index] {
			t.Fatalf("extra %d = %+v, want %+v", index, service.fetchPath.Extra[index], wantExtra[index])
		}
	}
}

func TestExactAddonResourceValidatesBoundedPaginationBeforeFetch(t *testing.T) {
	paths := []string{
		"?skip=-1",
		"?skip=next",
		"?skip=0&skip=-1",
		"?limit=",
		"?limit=0",
		"?limit=101",
		"?limit=24&limit=101",
	}
	for _, query := range paths {
		t.Run(query, func(t *testing.T) {
			service := &fakeAddonService{}
			api := addonAPI(service)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/11111111-1111-4111-8111-111111111111/resource/catalog/movie/featured"+query, nil)
			request.Header.Set("Authorization", "Bearer access")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", response.Code, response.Body.String())
			}
			if service.fetchCalls != 0 {
				t.Fatalf("invalid exact pagination reached service %d times", service.fetchCalls)
			}
		})
	}
}

func TestAddonGenericRoutesRejectPlaybackResourcesWithoutServiceFetch(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "single stream", path: "/api/v1/addons/11111111-1111-4111-8111-111111111111/resource/stream/movie/provider-id"},
		{name: "single stream with malformed extra", path: "/api/v1/addons/11111111-1111-4111-8111-111111111111/resource/stream/movie/provider-id?malformed"},
		{name: "single subtitles", path: "/api/v1/addons/11111111-1111-4111-8111-111111111111/resource/subtitles/movie/provider-id"},
		{name: "all stream", path: "/api/v1/addons/resources/stream/movie/provider-id"},
		{name: "all subtitles", path: "/api/v1/addons/resources/subtitles/movie/provider-id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAddonService{
				fetchValue: addon.ResourceResult{Payload: []byte(`{"streams":[{"url":"https://provider.example/private?token=secret","behaviorHints":{"proxyHeaders":{"request":{"Authorization":"Bearer secret"}}}}]}`)},
				allValue:   addon.ResourceBatch{Results: []addon.ResourceResult{{Payload: []byte(`{"subtitles":[{"url":"https://provider.example/private.vtt?token=secret"}]}`)}}},
			}
			api := addonAPI(service)
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer access")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", response.Code, response.Body.String())
			}
			var body errorEnvelope
			decodeResponse(t, response, &body)
			if body.Error.Code != "addon_resource_unsupported" {
				t.Fatalf("error code = %q, want addon_resource_unsupported", body.Error.Code)
			}
			if service.fetchCalls != 0 || service.allCalls != 0 {
				t.Fatalf("rejected route reached addon service: fetch=%d fetchAll=%d", service.fetchCalls, service.allCalls)
			}
			for _, secret := range []string{"provider.example", "private.vtt", "token", "Authorization", "Bearer secret"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatalf("rejected route exposed %q: %s", secret, response.Body.String())
				}
			}
		})
	}
}

func TestAddonCatalogResourceRouteRemainsAvailable(t *testing.T) {
	service := &fakeAddonService{allValue: addon.ResourceBatch{Results: []addon.ResourceResult{{
		Resource: "catalog", Type: "movie", ID: "popular", Payload: []byte(`{"metas":[]}`),
	}}}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/resources/catalog/movie/popular", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if service.allCalls != 1 || service.allPath.Resource != "catalog" {
		t.Fatalf("catalog request was not forwarded: calls=%d path=%+v", service.allCalls, service.allPath)
	}
}

func TestSearchAndAddonCatalogRoutesForwardTheirParameters(t *testing.T) {
	service := &fakeAddonService{}
	api := addonAPI(service)
	search := httptest.NewRequest(http.MethodGet, "/api/v1/addons/catalogs/search/custom-type?search=hello&genre=A&genre=B&skip=100&limit=24", nil)
	search.Header.Set("Authorization", "Bearer access")
	searchResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(searchResponse, search)
	if searchResponse.Code != http.StatusOK || service.searchType != "custom-type" {
		t.Fatalf("unexpected search request: status=%d type=%q input=%+v", searchResponse.Code, service.searchType, service.searchInput)
	}
	if service.searchInput.Search != "hello" || service.searchInput.Skip != 100 || service.searchInput.Limit != 24 {
		t.Fatalf("unexpected search pagination: %+v", service.searchInput)
	}
	wantExtra := []addon.ExtraValue{{Name: "genre", Value: "A"}, {Name: "genre", Value: "B"}}
	if len(service.searchInput.Extra) != len(wantExtra) {
		t.Fatalf("unexpected search extras: %+v", service.searchInput.Extra)
	}
	for index := range wantExtra {
		if service.searchInput.Extra[index] != wantExtra[index] {
			t.Fatalf("search extra %d = %+v, want %+v", index, service.searchInput.Extra[index], wantExtra[index])
		}
	}

	discover := httptest.NewRequest(http.MethodGet, "/api/v1/addons/discover?type=all&skip=100", nil)
	discover.Header.Set("Authorization", "Bearer access")
	discoverResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(discoverResponse, discover)
	if discoverResponse.Code != http.StatusOK || service.catalogType != "all" || !service.addonCatalog || len(service.catalogExtra) != 1 || service.catalogExtra[0].Name != "skip" {
		t.Fatalf("unexpected addon catalog request: status=%d type=%q addonCatalog=%v extra=%+v", discoverResponse.Code, service.catalogType, service.addonCatalog, service.catalogExtra)
	}
}

func TestSearchAddonCatalogsValidatesPagination(t *testing.T) {
	for _, path := range []string{
		"/api/v1/addons/catalogs/search/tv?skip=0&limit=24",
		"/api/v1/addons/catalogs/search/tv?search=x&skip=-1&limit=24",
		"/api/v1/addons/catalogs/search/tv?search=x&skip=next&limit=24",
		"/api/v1/addons/catalogs/search/tv?search=x&skip=0&limit=0",
		"/api/v1/addons/catalogs/search/tv?search=x&skip=0&limit=101",
	} {
		service := &fakeAddonService{}
		api := addonAPI(service)
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer access")
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: expected status 422, got %d: %s", path, response.Code, response.Body.String())
		}
		if service.searchType != "" {
			t.Fatalf("%s: invalid pagination reached service", path)
		}
	}
}

func TestAddonErrorsHaveStableHTTPContracts(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "profile", err: addon.ErrActiveProfileRequired, status: http.StatusConflict, code: "profile_selection_required"},
		{name: "transport", err: addon.ErrInvalidTransportURL, status: http.StatusUnprocessableEntity, code: "invalid_addon_transport"},
		{name: "manifest", err: addon.ErrInvalidManifest, status: http.StatusUnprocessableEntity, code: "invalid_addon_manifest"},
		{name: "duplicate", err: addon.ErrAlreadyInstalled, status: http.StatusConflict, code: "addon_already_installed"},
		{name: "missing", err: addon.ErrNotFound, status: http.StatusNotFound, code: "addon_not_found"},
		{name: "forbidden", err: addon.ErrForbidden, status: http.StatusForbidden, code: "addon_forbidden"},
		{name: "unsupported", err: addon.ErrUnsupportedResource, status: http.StatusUnprocessableEntity, code: "addon_resource_unsupported"},
		{name: "provider", err: addon.ErrProviderUnavailable, status: http.StatusBadGateway, code: "addon_unavailable"},
		{name: "response", err: addon.ErrInvalidResponse, status: http.StatusBadGateway, code: "addon_invalid_response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAddonService{fetchErr: errors.Join(errors.New("wrapped"), test.err)}
			api := addonAPI(service)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/11111111-1111-4111-8111-111111111111/resource/meta/movie/id", nil)
			request.Header.Set("Authorization", "Bearer access")
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("expected status %d, got %d", test.status, response.Code)
			}
			var body errorEnvelope
			decodeResponse(t, response, &body)
			if body.Error.Code != test.code {
				t.Fatalf("expected code %q, got %q", test.code, body.Error.Code)
			}
		})
	}
}

func TestDisabledAddonRuntimeNotFoundRemainsOpaque(t *testing.T) {
	privateDetail := "https://provider.invalid/private/manifest.json?token=disabled-secret"
	service := &fakeAddonService{fetchErr: fmt.Errorf("%w: %s", addon.ErrNotFound, privateDetail)}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/11111111-1111-4111-8111-111111111111/resource/meta/movie/id", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled runtime lookup status = %d, want 404: %s", response.Code, response.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "addon_not_found" {
		t.Fatalf("disabled runtime lookup code = %q, want addon_not_found", body.Error.Code)
	}
	for _, private := range []string{"provider.invalid", "private", "disabled-secret"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("disabled runtime lookup exposed private detail %q: %s", private, response.Body.String())
		}
	}
}

func TestDirectTMDBCatalogRoutesAreRemoved(t *testing.T) {
	api := addonAPI(&fakeAddonService{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/movies", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected removed TMDB route to return 404, got %d", response.Code)
	}
}

func addonAPI(service addonService) *API {
	future := time.Now().UTC().Add(time.Hour)
	profileID := "22222222-2222-4222-8222-222222222222"
	return &API{
		addons: service,
		auth: &fakeAuthService{principal: auth.Principal{
			SessionID: "session", UserID: "user", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &future,
		}},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}
