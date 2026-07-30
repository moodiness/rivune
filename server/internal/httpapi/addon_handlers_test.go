package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

type fakeAddonService struct {
	installInput addon.InstallInput
	installValue addon.InstalledAddon
	installErr   error
	listValue    []addon.InstalledAddon
	listErr      error
	removeID     string
	removeErr    error
	reorderInput addon.ReorderInput
	reorderValue []addon.InstalledAddon
	reorderErr   error
	refreshID    string
	refreshValue addon.InstalledAddon
	refreshErr   error
	assignID     string
	assignInput  addon.ProfileAssignmentInput
	assignValue  addon.InstalledAddon
	assignErr    error
	catalogs     []addon.CatalogDescriptor
	catalogsErr  error
	fetchAddonID string
	fetchPath    addon.ResourcePath
	fetchValue   addon.ResourceResult
	fetchErr     error
	allPath      addon.ResourcePath
	allValue     addon.ResourceBatch
	allErr       error
	catalogType  string
	catalogExtra []addon.ExtraValue
	addonCatalog bool
	catalogValue addon.ResourceBatch
	catalogErr   error
}

func (fake *fakeAddonService) Install(_ context.Context, _ auth.Principal, input addon.InstallInput) (addon.InstalledAddon, error) {
	fake.installInput = input
	return fake.installValue, fake.installErr
}

func (fake *fakeAddonService) List(context.Context, auth.Principal) ([]addon.InstalledAddon, error) {
	return fake.listValue, fake.listErr
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
func (fake *fakeAddonService) AssignProfiles(_ context.Context, _ auth.Principal, addonID string, input addon.ProfileAssignmentInput) (addon.InstalledAddon, error) {
	fake.assignID = addonID
	fake.assignInput = input
	return fake.assignValue, fake.assignErr
}

func (fake *fakeAddonService) Catalogs(context.Context, auth.Principal) ([]addon.CatalogDescriptor, error) {
	return fake.catalogs, fake.catalogsErr
}

func (fake *fakeAddonService) Fetch(_ context.Context, _ auth.Principal, addonID string, path addon.ResourcePath) (addon.ResourceResult, error) {
	fake.fetchAddonID = addonID
	fake.fetchPath = path
	return fake.fetchValue, fake.fetchErr
}

func (fake *fakeAddonService) FetchAll(_ context.Context, _ auth.Principal, path addon.ResourcePath) (addon.ResourceBatch, error) {
	fake.allPath = path
	return fake.allValue, fake.allErr
}

func (fake *fakeAddonService) FetchCatalogs(_ context.Context, _ auth.Principal, contentType string, extra []addon.ExtraValue, addonCatalog bool) (addon.ResourceBatch, error) {
	fake.catalogType = contentType
	fake.catalogExtra = extra
	fake.addonCatalog = addonCatalog
	return fake.catalogValue, fake.catalogErr
}

func TestInstallAddonUsesProfileScopedRegistry(t *testing.T) {
	installedAt := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	service := &fakeAddonService{installValue: addon.InstalledAddon{
		ID: "11111111-1111-4111-8111-111111111111", TransportURL: "https://addon.example/config/manifest.json",
		Manifest:    []byte(`{"id":"org.example","version":"1.0.0","name":"Example","types":["movie"],"resources":["stream"],"catalogs":[]}`),
		InstalledAt: installedAt, UpdatedAt: installedAt,
	}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/addons", bytes.NewBufferString(`{"transportUrl":"stremio://addon.example/config","profileIds":["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"]}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.installInput.TransportURL != "stremio://addon.example/config" {
		t.Fatalf("unexpected install input: %+v", service.installInput)
	}
	if len(service.installInput.ProfileIDs) != 1 || service.installInput.ProfileIDs[0] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("unexpected install profiles: %+v", service.installInput.ProfileIDs)
	}
}

func TestAssignAddonProfilesReplacesProfileSelection(t *testing.T) {
	service := &fakeAddonService{assignValue: addon.InstalledAddon{
		ID: "11111111-1111-4111-8111-111111111111", ProfileIDs: []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
	}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/addons/11111111-1111-4111-8111-111111111111/profiles", bytes.NewBufferString(`{"profileIds":["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"]}`))
	request.Header.Set("Authorization", "Bearer access")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.assignID != "11111111-1111-4111-8111-111111111111" || len(service.assignInput.ProfileIDs) != 1 || service.assignInput.ProfileIDs[0] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("unexpected assignment input: id=%q input=%+v", service.assignID, service.assignInput)
	}

}

func TestAddonResourceRoutePreservesOpaqueIDAndRepeatedExtras(t *testing.T) {
	service := &fakeAddonService{fetchValue: addon.ResourceResult{Payload: []byte(`{"streams":[]}`)}}
	api := addonAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/addons/11111111-1111-4111-8111-111111111111/resource/stream/anime-special/kitsu%3Aanime%2F42%3Aepisode%2F7?genre=Sci%20Fi&genre=Drama%2FAction&custom=a%2Bb", nil)
	request.Header.Set("Authorization", "Bearer access")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.fetchAddonID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected addon ID: %q", service.fetchAddonID)
	}
	if service.fetchPath.Resource != "stream" || service.fetchPath.Type != "anime-special" || service.fetchPath.ID != "kitsu:anime/42:episode/7" {
		t.Fatalf("opaque resource path changed: %+v", service.fetchPath)
	}
	wantExtra := []addon.ExtraValue{{Name: "genre", Value: "Sci Fi"}, {Name: "genre", Value: "Drama/Action"}, {Name: "custom", Value: "a+b"}}
	if len(service.fetchPath.Extra) != len(wantExtra) {
		t.Fatalf("unexpected extras: %+v", service.fetchPath.Extra)
	}
	for index := range wantExtra {
		if service.fetchPath.Extra[index] != wantExtra[index] {
			t.Fatalf("extra %d = %+v, want %+v", index, service.fetchPath.Extra[index], wantExtra[index])
		}
	}
}

func TestSearchAndAddonCatalogRoutesForwardArbitraryExtras(t *testing.T) {
	service := &fakeAddonService{}
	api := addonAPI(service)
	search := httptest.NewRequest(http.MethodGet, "/api/v1/addons/search/custom-type?search=hello&genre=A&genre=B&skip=100", nil)
	search.Header.Set("Authorization", "Bearer access")
	searchResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(searchResponse, search)
	if searchResponse.Code != http.StatusOK || service.catalogType != "custom-type" || service.addonCatalog || len(service.catalogExtra) != 4 {
		t.Fatalf("unexpected search request: status=%d type=%q addonCatalog=%v extra=%+v", searchResponse.Code, service.catalogType, service.addonCatalog, service.catalogExtra)
	}

	discover := httptest.NewRequest(http.MethodGet, "/api/v1/addons/discover?type=all&skip=100", nil)
	discover.Header.Set("Authorization", "Bearer access")
	discoverResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(discoverResponse, discover)
	if discoverResponse.Code != http.StatusOK || service.catalogType != "all" || !service.addonCatalog || len(service.catalogExtra) != 1 || service.catalogExtra[0].Name != "skip" {
		t.Fatalf("unexpected addon catalog request: status=%d type=%q addonCatalog=%v extra=%+v", discoverResponse.Code, service.catalogType, service.addonCatalog, service.catalogExtra)
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
