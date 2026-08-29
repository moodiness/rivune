package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/category"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/coordination"
	"github.com/moodiness/rivune/server/internal/instance"
	"github.com/moodiness/rivune/server/internal/jellyfin"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/operations"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/profile"
	"github.com/moodiness/rivune/server/internal/settings"
	"github.com/moodiness/rivune/server/internal/watchstate"
	"github.com/pb33f/libopenapi"
	oasvalidator "github.com/pb33f/libopenapi-validator"
)

const (
	contractUserID     = "11111111-1111-4111-8111-111111111111"
	contractSessionID  = "22222222-2222-4222-8222-222222222222"
	contractDeviceID   = "33333333-3333-4333-8333-333333333333"
	contractProfileID  = "44444444-4444-4444-8444-444444444444"
	contractCategoryID = "77777777-7777-4777-8777-777777777777"
	contractTitleID    = "55555555-5555-4555-8555-555555555555"
	contractAddonID    = "66666666-6666-4666-8666-666666666666"
	contractSeasonID   = "tvdb:77777777-7777-4777-8777-777777777777:1"
)

var (
	contractValidatorOnce sync.Once
	contractValidator     oasvalidator.Validator
	contractValidatorErr  error
)

func TestOpenAPIResponseContracts(t *testing.T) {
	document := loadOpenAPIContract(t)

	t.Run("discovery", func(t *testing.T) {
		api := testAPI(&fakeInstanceService{info: instanceInfoForContract()})
		request := httptest.NewRequest(http.MethodGet, "/.well-known/rivune", nil)
		response := serveContractRequest(t, api, request, http.StatusOK)
		var state struct {
			SetupRequired  bool     `json:"setupRequired"`
			SetupCompleted *bool    `json:"setupCompleted"`
			DemoAvailable  *bool    `json:"demoAvailable"`
			Capabilities   []string `json:"capabilities"`
		}
		decodeResponse(t, response, &state)
		if state.SetupRequired || state.SetupCompleted == nil || !*state.SetupCompleted || state.DemoAvailable == nil || *state.DemoAvailable ||
			!slices.Equal(state.Capabilities, nativeCapabilities[:]) {
			t.Fatalf("unexpected configured discovery state: %+v", state)
		}
		validateContractResponse(t, document, "/.well-known/rivune", nil, request, response)
	})

	t.Run("liveness", func(t *testing.T) {
		api := testAPI(&fakeInstanceService{})
		api.pool = databasePingerFunc(func(context.Context) error {
			return errors.New("database unavailable")
		})
		request := httptest.NewRequest(http.MethodGet, "/live", nil)
		response := serveContractRequest(t, api, request, http.StatusOK)
		validateContractResponse(t, document, "/live", nil, request, response)
	})

	t.Run("readiness", func(t *testing.T) {
		for _, path := range []string{"/health", "/ready"} {
			for _, test := range []struct {
				name   string
				err    error
				status int
			}{
				{name: "ready", status: http.StatusOK},
				{name: "database unavailable", err: errors.New("database unavailable"), status: http.StatusServiceUnavailable},
			} {
				t.Run(path+"/"+test.name, func(t *testing.T) {
					api := testAPI(&fakeInstanceService{})
					api.pool = databasePingerFunc(func(context.Context) error { return test.err })
					request := httptest.NewRequest(http.MethodGet, path, nil)
					response := serveContractRequest(t, api, request, test.status)
					validateContractResponse(t, document, path, nil, request, response)
				})
			}
		}
	})

	t.Run("setup status", func(t *testing.T) {
		api := testAPI(&fakeInstanceService{info: instance.Info{Name: "Rivune Contract", SetupRequired: true}})
		request := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
		response := serveContractRequest(t, api, request, http.StatusOK)
		var state struct {
			SetupRequired  bool  `json:"setupRequired"`
			SetupCompleted *bool `json:"setupCompleted"`
			DemoAvailable  *bool `json:"demoAvailable"`
		}
		decodeResponse(t, response, &state)
		if !state.SetupRequired || state.SetupCompleted == nil || *state.SetupCompleted || state.DemoAvailable == nil || !*state.DemoAvailable {
			t.Fatalf("unexpected pre-setup status lifecycle state: %+v", state)
		}
		validateContractResponse(t, document, "/setup/status", nil, request, response)
	})

	t.Run("demo lifecycle", func(t *testing.T) {
		instanceService := &fakeInstanceService{info: instance.Info{Name: "Rivune Contract", SetupRequired: true}}
		api := testAPI(instanceService)

		missingSession := httptest.NewRequest(http.MethodGet, "/api/v1/demo/session", nil)
		missingSessionResponse := serveContractRequest(t, api, missingSession, http.StatusUnauthorized)
		validateContractResponse(t, document, "/demo/session", nil, missingSession, missingSessionResponse)

		create := httptest.NewRequest(http.MethodPost, "/api/v1/demo/sessions", nil)
		createResponse := serveContractRequest(t, api, create, http.StatusCreated)
		validateContractResponse(t, document, "/demo/sessions", nil, create, createResponse)

		var demoCookie *http.Cookie
		for _, cookie := range createResponse.Result().Cookies() {
			if cookie.Name == "rivune_demo" {
				demoCookie = cookie
				break
			}
		}
		if demoCookie == nil {
			t.Fatal("demo session response did not set the rivune_demo cookie")
		}

		forbiddenSetup := httptest.NewRequest(http.MethodPost, "/api/v1/setup", nil)
		forbiddenSetup.AddCookie(demoCookie)
		forbiddenSetupResponse := serveContractRequest(t, api, forbiddenSetup, http.StatusForbidden)
		validateContractResponse(t, document, "/setup", nil, forbiddenSetup, forbiddenSetupResponse)

		resume := httptest.NewRequest(http.MethodGet, "/api/v1/demo/session", nil)
		resume.AddCookie(demoCookie)
		resumeResponse := serveContractRequest(t, api, resume, http.StatusOK)
		validateContractResponse(t, document, "/demo/session", nil, resume, resumeResponse)

		reset := httptest.NewRequest(http.MethodPost, "/api/v1/demo/session/reset", nil)
		reset.AddCookie(demoCookie)
		resetResponse := serveContractRequest(t, api, reset, http.StatusOK)
		validateContractResponse(t, document, "/demo/session/reset", nil, reset, resetResponse)

		rangeRequest := httptest.NewRequest(http.MethodGet, "/api/v1/demo/assets/demo-720p.mp4", nil)
		rangeRequest.AddCookie(demoCookie)
		rangeRequest.Header.Set("Range", "bytes=0-15")
		rangeResponse := serveContractRequest(t, api, rangeRequest, http.StatusPartialContent)
		validateContractResponse(t, document, "/demo/assets/{name}", map[string]string{"name": "demo-720p.mp4"}, rangeRequest, rangeResponse)

		exit := httptest.NewRequest(http.MethodDelete, "/api/v1/demo/session", nil)
		exit.AddCookie(demoCookie)
		exitResponse := serveContractRequest(t, api, exit, http.StatusNoContent)
		validateContractResponse(t, document, "/demo/session", nil, exit, exitResponse)

		recreate := httptest.NewRequest(http.MethodPost, "/api/v1/demo/sessions", nil)
		recreateResponse := serveContractRequest(t, api, recreate, http.StatusCreated)
		var staleCookie *http.Cookie
		for _, cookie := range recreateResponse.Result().Cookies() {
			if cookie.Name == "rivune_demo" {
				staleCookie = cookie
				break
			}
		}
		if staleCookie == nil {
			t.Fatal("second demo session response did not set the rivune_demo cookie")
		}

		instanceService.info = instance.Info{Name: "Rivune Contract", SetupRequired: false}
		unavailable := httptest.NewRequest(http.MethodGet, "/api/v1/demo/session", nil)
		unavailable.AddCookie(staleCookie)
		unavailableResponse := serveContractRequest(t, api, unavailable, http.StatusGone)
		validateContractResponse(t, document, "/demo/session", nil, unavailable, unavailableResponse)
	})

	t.Run("authentication and profiles", func(t *testing.T) {
		now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
		authService := &fakeAuthService{
			loginTokens: auth.TokenPair{
				AccessToken: "rivune_at_contract", AccessExpiresAt: now.Add(15 * time.Minute),
				RefreshToken: "rivune_rt_contract", RefreshExpiresAt: now.Add(30 * 24 * time.Hour),
				SessionID: contractSessionID, DeviceID: contractDeviceID,
				AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
			},
			principal: contractPrincipal(),
			account: auth.Account{
				Principal: contractPrincipal(),
				Profiles: []auth.Profile{{
					ID: contractProfileID, Name: "Admin", CanManage: true, Enabled: true,
					AccessTimezone: "UTC", Accessible: true, AvatarKind: "preset", AvatarPreset: "aurora",
					Category: category.CategoryRef{ID: contractCategoryID, Name: "Uncategorized"},
				}},
			},
		}
		profileService := &fakeProfileService{profiles: []profile.Profile{contractProfile()}}
		api := testAPI(&fakeInstanceService{})
		api.auth = authService
		api.profiles = profileService

		login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"contract-password","device":{"name":"Contract iPhone","platform":"ios"}}`))
		login.Header.Set("Content-Type", "application/json")
		loginResponse := serveContractRequest(t, api, login, http.StatusOK)
		validateContractResponse(t, document, "/auth/login", nil, login, loginResponse)
		webLoginBody := `{"username":"admin","password":"contract-password","device":{"id":"33333333-3333-4333-8333-333333333333","ids":["33333333-3333-4333-8333-333333333333","88888888-8888-4888-8888-888888888888"],"name":"Contract browser","platform":"web"}}`
		webLogin := httptest.NewRequest(http.MethodPost, "https://media.example/api/v1/auth/web/login", bytes.NewBufferString(webLoginBody))
		webLogin.Header.Set("Content-Type", "application/json")
		webLogin.Header.Set("Origin", "https://media.example")
		webLogin.Header.Set(webCSRFHeader, "1")
		if valid, validationErrors := document.ValidateHttpRequest(webLogin); !valid {
			t.Fatalf("web login request violates OpenAPI contract: %v", validationErrors)
		}
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/auth/login", webLoginBody, false)

		account := authenticatedContractRequest(http.MethodGet, "/api/v1/auth/me", nil)
		accountResponse := serveContractRequest(t, api, account, http.StatusOK)
		validateContractResponse(t, document, "/auth/me", nil, account, accountResponse)

		profiles := httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil)
		profiles.Header.Set("Authorization", "Bearer rivune_at_contract")
		profilesResponse := serveContractRequest(t, api, profiles, http.StatusOK)
		validateContractResponse(t, document, "/profiles", nil, profiles, profilesResponse)
	})

	t.Run("add-on diagnostics", func(t *testing.T) {
		now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
		latency := int64(12)
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		api.addons = &fakeAddonService{diagnosticsValue: addon.Diagnostics{
			ObservedSince: now.Add(-time.Hour),
			Diagnostics: []addon.DiagnosticEntry{{
				AddonID:              contractAddonID,
				State:                addon.DiagnosticStateDegraded,
				LastSuccessAt:        &now,
				ApproximateLatencyMS: &latency,
				LastError:            &addon.DiagnosticLastError{Code: addon.DiagnosticErrorUnavailable, At: now},
				Capabilities: addon.AddonCapabilities{
					Resources: []string{"catalog", "meta"}, Search: true, Pagination: true, SearchPagination: true,
				},
			}},
		}}
		request := authenticatedContractRequest(http.MethodGet, "/api/v1/addons/diagnostics", nil)
		response := serveContractRequest(t, api, request, http.StatusOK)
		validateContractResponse(t, document, "/addons/diagnostics", nil, request, response)
	})

	t.Run("add-on verification", func(t *testing.T) {
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		manifest := addon.Manifest{ID: "org.rivune.contract-verification", Version: "1.0.0", Name: "Contract Verification", Types: []string{"movie"}, Resources: []addon.ManifestResource{{Name: "catalog", Short: true}}, Catalogs: []addon.ManifestCatalog{}}
		capabilities := addon.AddonCapabilities{Resources: []string{"catalog"}}
		api.addons = &fakeAddonService{verificationValue: addon.AddonVerification{
			ID: "66666666-6666-4666-8666-666666666666", Status: "passed", Summary: "ready",
			Checks: []addon.VerificationCheck{{Code: "manifest_fetch", Status: "passed"}, {Code: "manifest_valid", Status: "passed"}, {Code: "catalog_probe", Status: "skipped"}},
			Manifest: &manifest, Capabilities: &capabilities, ProfileIDs: []string{}, CategoryIDs: []string{contractCategoryID},
			CreatedAt: time.Date(2026, time.August, 21, 20, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, time.August, 21, 20, 10, 0, 0, time.UTC),
		}}
		body := `{"transportUrl":"stremio://contract-addon.example/config","profileIds":[],"categoryIds":["` + contractCategoryID + `"]}`
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/addons/verifications", body, true)
		request := authenticatedContractRequest(http.MethodPost, "/api/v1/addons/verifications", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		response := serveContractRequest(t, api, request, http.StatusCreated)
		validateContractResponse(t, document, "/addons/verifications", nil, request, response)
	})

	t.Run("add-on availability", func(t *testing.T) {
		now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
		installed := addon.InstalledAddon{
			ID:       contractAddonID,
			Manifest: json.RawMessage(`{"id":"org.rivune.contract","version":"1.0.0","name":"Contract Add-on","types":["movie"],"resources":["catalog"],"catalogs":[]}`),
			Position: 0, ProfileIDs: []string{}, CategoryIDs: []string{contractCategoryID}, Enabled: false,
			InstalledAt: now, UpdatedAt: now,
		}
		service := &fakeAddonService{
			installValue:    installed,
			listValue:       []addon.InstalledAddon{installed},
			managementValue: addon.ManagedAddon{InstalledAddon: installed, TransportURL: "https://contract-addon.example/manifest.json?token=private"},
			updateValue:     installed,
		}
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		api.addons = service
		installBody := `{"verificationId":"66666666-6666-4666-8666-666666666666"}`
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/addons", installBody, true)
		install := authenticatedContractRequest(http.MethodPost, "/api/v1/addons", bytes.NewBufferString(installBody))
		install.Header.Set("Content-Type", "application/json")
		installResponse := serveContractRequest(t, api, install, http.StatusCreated)
		validateContractResponse(t, document, "/addons", nil, install, installResponse)

		list := authenticatedContractRequest(http.MethodGet, "/api/v1/addons", nil)
		listResponse := serveContractRequest(t, api, list, http.StatusOK)
		validateContractResponse(t, document, "/addons", nil, list, listResponse)

		delegatedInstalled := installed
		delegatedInstalled.ProfileIDs = []string{contractProfileID}
		delegatedInstalled.CategoryIDs = []string{}
		delegatedPrincipal := contractPrincipal()
		delegatedPrincipal.AuthorizationScope = auth.AuthorizationScopeCategory
		delegatedPrincipal.CategoryID = contractStringPointer(contractCategoryID)
		delegatedAPI := testAPI(&fakeInstanceService{})
		delegatedAPI.auth = &fakeAuthService{principal: delegatedPrincipal}
		delegatedAPI.addons = &fakeAddonService{listValue: []addon.InstalledAddon{delegatedInstalled}}
		delegatedList := authenticatedContractRequest(http.MethodGet, "/api/v1/addons", nil)
		delegatedResponse := serveContractRequest(t, delegatedAPI, delegatedList, http.StatusOK)
		validateContractResponse(t, document, "/addons", nil, delegatedList, delegatedResponse)
		var delegatedBody struct {
			Addons []addon.InstalledAddon `json:"addons"`
		}
		decodeResponse(t, delegatedResponse, &delegatedBody)
		if len(delegatedBody.Addons) != 1 || len(delegatedBody.Addons[0].CategoryIDs) != 0 || len(delegatedBody.Addons[0].ProfileIDs) != 1 || delegatedBody.Addons[0].ProfileIDs[0] != contractProfileID {
			t.Fatalf("delegated add-on list did not preserve explicit-only service projection: %+v", delegatedBody.Addons)
		}

		management := authenticatedContractRequest(http.MethodGet, "/api/v1/addons/"+contractAddonID+"/management", nil)
		managementResponse := serveContractRequest(t, api, management, http.StatusOK)
		validateContractResponse(t, document, "/addons/{addonId}/management", map[string]string{"addonId": contractAddonID}, management, managementResponse)

		updatePath := "/api/v1/addons/" + contractAddonID
		withEnabled := `{"profileIds":[],"categoryIds":["` + contractCategoryID + `"],"enabled":false}`
		withoutEnabled := `{"profileIds":["` + contractProfileID + `"]}`
		validateContractRequestBody(t, document, http.MethodPut, updatePath, withEnabled, true)
		validateContractRequestBody(t, document, http.MethodPut, updatePath, withoutEnabled, true)
		validateContractRequestBody(t, document, http.MethodPut, updatePath, `{}`, true)
		validateContractRequestBody(t, document, http.MethodPut, updatePath, `{"profileIds":[],"categoryIds":[]}`, false)
		validateContractRequestBody(t, document, http.MethodPut, updatePath, `{"profileIds":[]}`, true)
		validateContractRequestBody(t, document, http.MethodPut, updatePath, `{"categoryIds":[]}`, true)
		validateContractRequestBody(t, document, http.MethodPut, updatePath, `{"categoryIds":["`+contractCategoryID+`","`+contractCategoryID+`"]}`, false)
		validateContractRequestBody(t, document, http.MethodPut, updatePath, `{"profileIds":["not-a-uuid"]}`, false)
		validateContractRequestBody(t, document, http.MethodPut, updatePath, `{"profileIds":`+contractUUIDArray(101)+`}`, false)
		validateContractRequestBody(t, document, http.MethodPut, updatePath, `{"profileIds":["`+contractProfileID+`"],"enabled":"false"}`, false)
		invalidAssignments := []struct {
			name   string
			method string
			path   string
			body   string
		}{
			{name: "update null profileIds", method: http.MethodPut, path: updatePath, body: `{"profileIds":null}`},
			{name: "update null categoryIds", method: http.MethodPut, path: updatePath, body: `{"categoryIds":null}`},
			{name: "update unknown member", method: http.MethodPut, path: updatePath, body: `{"unexpectedAssignment":[]}`},
		}
		for _, invalid := range invalidAssignments {
			t.Run(invalid.name, func(t *testing.T) {
				validateContractRequestBody(t, document, invalid.method, invalid.path, invalid.body, false)
				request := authenticatedContractRequest(invalid.method, invalid.path, bytes.NewBufferString(invalid.body))
				request.Header.Set("Content-Type", "application/json")
				response := serveContractRequest(t, api, request, http.StatusBadRequest)
				validateContractResponse(t, document, invalid.path, nil, request, response)
			})
		}
		update := authenticatedContractRequest(http.MethodPut, updatePath, bytes.NewBufferString(withEnabled))
		update.Header.Set("Content-Type", "application/json")
		updateResponse := serveContractRequest(t, api, update, http.StatusOK)
		validateContractResponse(t, document, "/addons/{addonId}", map[string]string{"addonId": contractAddonID}, update, updateResponse)
		if service.updateInput.Enabled == nil || *service.updateInput.Enabled {
			t.Fatalf("enabled input was not forwarded exactly: %v", service.updateInput.Enabled)
		}
	})

	t.Run("add-on catalog descriptors", func(t *testing.T) {
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		api.addons = &fakeAddonService{catalogs: []addon.CatalogDescriptor{
			{
				AddonID: contractAddonID, AddonName: "Contract Add-on", AddonLogoURL: "https://origin.invalid/contract-logo.png", ManifestID: "org.rivune.contract",
				Position: 0, Catalog: addon.ManifestCatalog{Type: "movie", ID: "featured", Name: "Featured"}, Searchable: true,
			},
			{
				AddonID: contractAddonID, AddonName: "Contract Add-on", AddonLogoURL: "https://origin.invalid/contract-logo.png", ManifestID: "org.rivune.contract",
				Position: 0, Catalog: addon.ManifestCatalog{Type: "all", ID: "community", Name: "Community"}, AddonCatalog: true,
			},
		}}
		api.catalogArtwork = &fakeCatalogArtworkPresenter{}
		request := authenticatedContractRequest(http.MethodGet, "/api/v1/addons/catalogs", nil)
		response := serveContractRequest(t, api, request, http.StatusOK)
		validateContractResponse(t, document, "/addons/catalogs", nil, request, response)
	})

	t.Run("metadata and plural trailers", func(t *testing.T) {
		metadataService := &fakeMetadataService{
			detailsMovie: metadata.Movie{
				ID: contractTitleID, MediaType: metadata.MediaTypeMovie, Title: "Contract Movie",
				OriginalTitle: "Contract Movie", OriginalLanguage: "en", Overview: "A deterministic contract fixture.",
				Genres: []metadata.Genre{{ID: 18, Name: "Drama"}}, Cast: []metadata.CastMember{{ID: "819", Name: "Contract Actor", Character: "Narrator", ProfileURL: "https://image.example/actor.jpg"}}, VoteAverage: 7.5, VoteCount: 10,
				PosterURL: "https://fanart.example/movie-poster.jpg", BackdropURL: "https://fanart.example/movie-background.jpg",
				LogoURL:     "https://fanart.example/movie-logo.png",
				ExternalIDs: map[string]string{"tmdb": "900101"},
			},
			seriesDetailsValue: metadata.Series{
				ID: contractTitleID, MediaType: metadata.MediaTypeSeries, Name: "Contract Series",
				OriginalName: "Contract Series", OriginalLanguage: "en", Overview: "Mapped series fixture.",
				Genres: []metadata.Genre{}, Cast: []metadata.CastMember{}, VoteAverage: 8, VoteCount: 20, Seasons: []metadata.SeasonSummary{},
				PosterURL: "https://fanart.example/series-poster.jpg", BackdropURL: "https://fanart.example/series-background.jpg",
				LogoURL: "https://fanart.example/series-logo.png",
				Aliases: []metadata.Alias{}, EpisodeOrders: []metadata.EpisodeOrder{}, MappingProvider: "tvdb",
				ExternalIDs: map[string]string{"tmdb": "92001", "tvdb": "93001"},
			},
			seasonDetailsValue: metadata.Season{
				ID: contractSeasonID, MediaType: metadata.MediaTypeSeason, SeriesID: contractTitleID,
				Name: "Season 1", Overview: "Mapped season fixture.", SeasonNumber: 1, VoteAverage: 8,
				Episodes: []metadata.Episode{}, ExternalIDs: map[string]string{"tvdb": "93011"},
			},
			trailersValue: metadata.TrailerList{Trailers: []metadata.Trailer{{
				YouTubeID: "contract-video", Name: "Official Trailer", Language: "en-US", IsFallback: false,
			}}},
		}
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		api.metadata = metadataService

		movie := authenticatedContractRequest(http.MethodGet, "/api/v1/metadata/titles/"+contractTitleID+"?language=en-US", nil)
		movieResponse := serveContractRequest(t, api, movie, http.StatusOK)
		validateContractResponse(t, document, "/metadata/titles/{titleId}", map[string]string{"titleId": contractTitleID}, movie, movieResponse)

		series := authenticatedContractRequest(http.MethodGet, "/api/v1/metadata/series/"+contractTitleID+"?language=en-US&mappingProvider=tvdb", nil)
		seriesResponse := serveContractRequest(t, api, series, http.StatusOK)
		validateContractResponse(t, document, "/metadata/series/{titleId}", map[string]string{"titleId": contractTitleID}, series, seriesResponse)

		season := authenticatedContractRequest(http.MethodGet, "/api/v1/metadata/seasons/"+contractSeasonID+"?language=en-US&mappingProvider=tvdb", nil)
		seasonResponse := serveContractRequest(t, api, season, http.StatusOK)
		validateContractResponse(t, document, "/metadata/seasons/{seasonId}", map[string]string{"seasonId": contractSeasonID}, season, seasonResponse)

		trailers := authenticatedContractRequest(http.MethodGet, "/api/v1/metadata/titles/"+contractTitleID+"/trailers?language=en-US", nil)
		trailersResponse := serveContractRequest(t, api, trailers, http.StatusOK)
		validateContractResponse(t, document, "/metadata/titles/{titleId}/trailers", map[string]string{"titleId": contractTitleID}, trailers, trailersResponse)
	})

	t.Run("Jellyfin profile credentials", func(t *testing.T) {
		createdAt := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
		service := &fakeJellyfinCredentialService{
			status: jellyfin.CredentialStatus{
				Username: contractProfileID, Active: true, CanIssue: true, Generation: 1, CreatedAt: createdAt,
			},
			credential: jellyfin.ProfileCredential{
				CredentialStatus: jellyfin.CredentialStatus{
					Username: contractProfileID, Active: true, CanIssue: true, Generation: 2, CreatedAt: createdAt,
				},
				Password: "rivune_jfa_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			},
		}
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		api.jellyfinCredentials = service
		path := "/api/v1/profiles/" + contractProfileID + "/jellyfin-credential"
		parameters := map[string]string{"profileId": contractProfileID}

		statusRequest := authenticatedContractRequest(http.MethodGet, path, nil)
		statusResponse := serveContractRequest(t, api, statusRequest, http.StatusOK)
		validateContractResponse(t, document, "/profiles/{profileId}/jellyfin-credential", parameters, statusRequest, statusResponse)

		createRequest := authenticatedContractRequest(http.MethodPost, path, nil)
		createResponse := serveContractRequest(t, api, createRequest, http.StatusCreated)
		validateContractResponse(t, document, "/profiles/{profileId}/jellyfin-credential", parameters, createRequest, createResponse)

		rotateRequest := authenticatedContractRequest(http.MethodPost, path+"/rotate", nil)
		rotateResponse := serveContractRequest(t, api, rotateRequest, http.StatusOK)
		validateContractResponse(t, document, "/profiles/{profileId}/jellyfin-credential/rotate", parameters, rotateRequest, rotateResponse)

		revokeRequest := authenticatedContractRequest(http.MethodDelete, path, nil)
		revokeResponse := serveContractRequest(t, api, revokeRequest, http.StatusNoContent)
		validateContractResponse(t, document, "/profiles/{profileId}/jellyfin-credential", parameters, revokeRequest, revokeResponse)
	})

	t.Run("transcoding settings", func(t *testing.T) {
		instanceAllowed := false
		jellyfinEnabled := false
		profileMode := settings.TranscodingModeEnabled
		timezone := settings.DefaultTimezone
		jellyfinDebug := false
		hardwareAcceleration := settings.DefaultHardwareAcceleration
		preferredTranscodeVideoCodec := settings.DefaultPreferredTranscodeVideoCodec
		transcodeQualityPreset := settings.DefaultTranscodeQualityPreset
		transcodeConcurrency := settings.DefaultTranscodeConcurrency
		transcodeMaxBitrateKbps := settings.DefaultTranscodeMaxBitrateKbps
		mediaMaxStorageMB := settings.DefaultMediaMaxStorageMB
		artworkMaxStorageMB := settings.DefaultArtworkMaxStorageMB
		settingService := &fakeSettingsService{
			instance: settings.Layer{
				SchemaVersion: 3,
				Values: settings.Values{
					AllowTranscoding: &instanceAllowed, JellyfinEnabled: &jellyfinEnabled,
					Timezone: &timezone, JellyfinDebug: &jellyfinDebug, HardwareAcceleration: &hardwareAcceleration,
					PreferredTranscodeVideoCodec: &preferredTranscodeVideoCodec, TranscodeQualityPreset: &transcodeQualityPreset, TranscodeConcurrency: &transcodeConcurrency,
					TranscodeMaxBitrateKbps: &transcodeMaxBitrateKbps, MediaMaxStorageMB: &mediaMaxStorageMB, ArtworkMaxStorageMB: &artworkMaxStorageMB,
				},
			},
			profile: settings.Layer{
				SchemaVersion: 1,
				Values:        settings.Values{Transcoding: &profileMode},
			},
			effective: settings.Effective{
				SchemaVersion: 1,
				Values: settings.EffectiveValues{
					InterfaceLanguage: "en", Theme: "system", MaximumResolution: "1080p", MaximumCastMembers: settings.DefaultMaximumCastMembers, MaximumDirectTitles: settings.DefaultMaximumDirectTitles,
					AllowTranscoding: false, Transcoding: settings.TranscodingModeEnabled, PreferDirectPlay: true,
					HideUnreleased: false, MetadataLanguage: "auto", MetadataRegion: "auto",
					SeriesMappingProvider: "tmdb", AudioLanguage: "auto", SubtitleLanguage: "auto",
					ForcedSubtitleLanguage: "off", AutoplayNextEpisode: true,
					SkipIntroEnabled: true, SkipRecapEnabled: true, SkipOutroEnabled: true,
					CardDensity: "comfortable", AnimationsEnabled: true, SubtitleSizePercent: 100,
					SubtitleTextColor: "#FFFFFF", SubtitleBackgroundOpacityPercent: 75,
					NotificationsEnabled: true, NotificationDurationSeconds: 5, NotificationPollIntervalSeconds: 30,
				},
				Sources: map[string]string{
					"interfaceLanguage": "default", "theme": "default", "maximumResolution": "instance", "maximumCastMembers": "default", "maximumDirectTitles": "default",
					"allowTranscoding": "instance", "transcoding": "profile", "preferDirectPlay": "default",
					"hideUnreleased": "default", "metadataLanguage": "default", "metadataRegion": "default",
					"seriesMappingProvider": "default", "audioLanguage": "default", "subtitleLanguage": "default",
					"forcedSubtitleLanguage": "default", "autoplayNextEpisode": "default",
					"skipIntroEnabled": "default", "skipRecapEnabled": "default", "skipOutroEnabled": "default",
					"cardDensity": "default", "animationsEnabled": "default", "subtitleSizePercent": "default",
					"subtitleTextColor": "default", "subtitleBackgroundOpacityPercent": "default",
					"notificationsEnabled": "default", "notificationDurationSeconds": "default",
					"notificationPollIntervalSeconds": "default",
				},
			},
		}
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		api.settings = settingService

		instanceRequest := authenticatedContractRequest(http.MethodGet, "/api/v1/settings", nil)
		instanceResponse := serveContractRequest(t, api, instanceRequest, http.StatusOK)
		validateContractResponse(t, document, "/settings", nil, instanceRequest, instanceResponse)

		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"allowTranscoding":null}`, true)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"allowTranscoding":"false"}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"jellyfinEnabled":true}`, true)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"jellyfinEnabled":null}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"preferredTranscodeVideoCodec":"av1","transcodeQualityPreset":"quality","transcodeConcurrency":32}`, true)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"preferredTranscodeVideoCodec":null}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"preferredTranscodeVideoCodec":"vp9"}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"transcodeQualityPreset":"ultrafast"}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"transcodeConcurrency":0}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"transcodeConcurrency":33}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, "/api/v1/settings", `{"hardwareAcceleration":"amf"}`, true)
		instancePatch := authenticatedContractRequest(http.MethodPatch, "/api/v1/settings", bytes.NewBufferString(`{"allowTranscoding":false}`))
		instancePatch.Header.Set("Content-Type", "application/json")
		instancePatchResponse := serveContractRequest(t, api, instancePatch, http.StatusOK)
		validateContractResponse(t, document, "/settings", nil, instancePatch, instancePatchResponse)

		profilePath := "/api/v1/profiles/" + contractProfileID + "/settings"
		profileRequest := authenticatedContractRequest(http.MethodGet, profilePath, nil)
		profileResponse := serveContractRequest(t, api, profileRequest, http.StatusOK)
		validateContractResponse(t, document, "/profiles/{profileId}/settings", nil, profileRequest, profileResponse)

		validateContractRequestBody(t, document, http.MethodPatch, profilePath, `{"transcoding":null}`, true)
		validateContractRequestBody(t, document, http.MethodPatch, profilePath, `{"transcoding":"future"}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, profilePath, `{"jellyfinEnabled":true}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, profilePath, `{"preferredTranscodeVideoCodec":"av1"}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, profilePath, `{"transcodeQualityPreset":"quality"}`, false)
		validateContractRequestBody(t, document, http.MethodPatch, profilePath, `{"transcodeConcurrency":8}`, false)
		profilePatch := authenticatedContractRequest(http.MethodPatch, profilePath, bytes.NewBufferString(`{"transcoding":"enabled"}`))
		profilePatch.Header.Set("Content-Type", "application/json")
		profilePatchResponse := serveContractRequest(t, api, profilePatch, http.StatusOK)
		validateContractResponse(t, document, "/profiles/{profileId}/settings", nil, profilePatch, profilePatchResponse)

		effectiveRequest := authenticatedContractRequest(http.MethodGet, profilePath+"/effective", nil)
		effectiveResponse := serveContractRequest(t, api, effectiveRequest, http.StatusOK)
		validateContractResponse(t, document, "/profiles/{profileId}/settings/effective", nil, effectiveRequest, effectiveResponse)
	})

	t.Run("playback", func(t *testing.T) {
		expiresAt := time.Date(2026, time.July, 31, 13, 0, 0, 0, time.UTC)
		selectedAudioTrack := 2
		decision := &playback.PlaybackDecision{
			Reason: "subtitle_burn_required", Reasons: []string{"subtitle_burn_required"}, VideoAction: "transcode", AudioAction: "copy",
			SubtitleAction: "burn", ToneMapping: true,
			Source: &playback.PlaybackDecisionSource{
				Container: "matroska", VideoCodec: "hevc", AudioCodec: "aac",
				Height: 2160, VideoBitrateKbps: 24000, HDRFormat: "dolby_vision",
			},
			Target: &playback.PlaybackDecisionTarget{
				Protocol: "hls", Container: "mpegts", VideoCodec: "h264", AudioCodec: "aac",
				Height: 1080, VideoBitrateKbps: 12000,
			},
		}
		playbackService := &fakePlaybackService{
			sources: playback.SourceList{
				Sources: []playback.SourceOption{{
					ID: "source-1", SourceRef: "opaque-contract-source", StableIdentity: "stable-contract-source", AddonID: contractAddonID,
					ManifestID: "org.rivune.contract", AddonName: "Contract Add-on", StreamIndex: 0, Name: "Contract source",
					Protocol: "hls", Mode: "direct", Container: "mpegts", ExpiresAt: expiresAt,
				}},
				ProviderErrors: []playback.ProviderFailure{},
			},
			preparation: playback.Preparation{
				SourceRef: "opaque-contract-source", Mode: "transcode", Protocol: "hls",
				Container: "mpegts", Decision: decision, SubtitleCount: 1, ExpiresAt: expiresAt,
			},
			session: playback.Session{
				ID: contractSessionID, SelectedSourceID: "source-1", SelectedAudioTrack: &selectedAudioTrack,
				SelectedSubtitleID: "subtitle-1",
				Sources: []playback.Source{{
					ID: "source-1", AddonID: contractAddonID, ManifestID: "org.rivune.contract",
					Mode: "transcode", URL: "/api/v1/playback/sessions/" + contractSessionID + "/assets/media?token=opaque",
					Protocol: "hls", Container: "mpegts", Compatible: true, Decision: decision,
				}},
				Subtitles: []playback.Subtitle{{
					ID: "subtitle-1", AddonID: contractAddonID, ManifestID: "org.rivune.contract",
					Language: "en", Delivery: "burn", Default: true,
				}},
				ProviderErrors: []playback.ProviderFailure{}, ExpiresAt: expiresAt,
			},
			activity: playback.Activity{
				Summary: playback.ActivitySummary{
					ActiveSessions: 1, ActiveJobs: 1, ProcessingSlots: 1, ProcessingLimit: 2,
					StorageBytes: 4096, StorageLimitBytes: 1024 * 1024,
				},
				Diagnostics: playback.MediaDiagnostics{FFmpegVersion: "7.1", FFprobeVersion: "7.1", HardwareAcceleration: "software", VideoEncoder: "libx264", PreferredVideoCodec: "auto", EncodeCodecs: []string{"h264", "hevc", "av1"}, DecodeCodecs: []string{"h264", "hevc", "av1"}, QualityPreset: "balanced", HardwareToneMap: false, ToneMapBackend: "software", TranscodeThreads: 4, MaximumReadRate: 1.5},
				Sessions: []playback.ActivitySession{{
					ID: contractSessionID, Title: "Contract Movie", MediaType: "movie", Mode: "transcode",
					Decision: decision, Username: "admin", ProfileID: contractProfileID, Profile: "Admin",
					Device: "Contract iPhone", Platform: "ios", Processing: true, PositionSeconds: 120,
					DurationSeconds: 7200, CreatedAt: expiresAt.Add(-time.Hour), LastSeenAt: expiresAt.Add(-time.Minute),
					ExpiresAt: expiresAt,
				}},
				Jobs: []playback.MediaActivityJob{{
					SessionID: contractSessionID, AssetID: "source-1", Mode: "transcode", State: "failed", ErrorClass: "processing",
					CreatedAt: expiresAt.Add(-time.Hour), LastSeenAt: expiresAt.Add(-time.Minute),
				}},
			},
		}
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		api.playback = playbackService
		api.settings = &fakeSettingsService{}

		sourcesBody := `{"mediaType":"movie","resourceId":"tt9001001","capabilities":{"streamingProtocols":["hls"],"containers":["mpegts"],"processingModes":["remux","transcode_audio","transcode"],"maximumHeight":4320,"maximumVideoBitrateKbps":200000,"maximumAudioChannels":32,"subtitleModes":["external","burn"],"mediaProfiles":[{"container":"mp4","videoCodec":"h264","audioCodec":"aac"}]}}`
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/playback/sources", sourcesBody, true)
		sources := authenticatedContractRequest(http.MethodPost, "/api/v1/playback/sources", bytes.NewBufferString(sourcesBody))
		sources.Header.Set("Content-Type", "application/json")
		sourcesResponse := serveContractRequest(t, api, sources, http.StatusOK)
		validateContractResponse(t, document, "/playback/sources", nil, sources, sourcesResponse)

		prepareBody := `{"sourceRef":"opaque-contract-source","startSeconds":604800,"externalPlayer":true}`
		prepare := authenticatedContractRequest(http.MethodPost, "/api/v1/playback/prepare", bytes.NewBufferString(prepareBody))
		prepare.Header.Set("Content-Type", "application/json")
		prepareResponse := serveContractRequest(t, api, prepare, http.StatusOK)
		validateContractResponse(t, document, "/playback/prepare", nil, prepare, prepareResponse)

		resolveBody := `{"sourceRef":"opaque-contract-source","titleId":"` + contractTitleID + `","preferredAudioTrack":2,"preferredSubtitleId":"subtitle-1","startSeconds":0,"externalPlayer":true}`
		resolve := authenticatedContractRequest(http.MethodPost, "/api/v1/playback/resolve", bytes.NewBufferString(resolveBody))
		resolve.Header.Set("Content-Type", "application/json")
		resolveResponse := serveContractRequest(t, api, resolve, http.StatusCreated)
		validateContractResponse(t, document, "/playback/resolve", nil, resolve, resolveResponse)

		activity := authenticatedContractRequest(http.MethodGet, "/api/v1/playback/activity", nil)
		activityResponse := serveContractRequest(t, api, activity, http.StatusOK)
		validateContractResponse(t, document, "/playback/activity", nil, activity, activityResponse)

		validBodies := []struct {
			path string
			body string
		}{
			{path: "/api/v1/playback/prepare", body: `{"sourceRef":"opaque-contract-source","startSeconds":0}`},
			{path: "/api/v1/playback/prepare", body: `{"sourceRef":"opaque-contract-source","startSeconds":604800}`},
			{path: "/api/v1/playback/resolve", body: `{"sourceRef":"opaque-contract-source","startSeconds":0}`},
			{path: "/api/v1/playback/resolve", body: `{"sourceRef":"opaque-contract-source","startSeconds":604800}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt9001001","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumHeight":144,"maximumVideoBitrateKbps":64,"maximumAudioChannels":1}}`},
		}
		for _, fixture := range validBodies {
			validateContractRequestBody(t, document, http.MethodPost, fixture.path, fixture.body, true)
		}

		invalidBodies := []struct {
			path string
			body string
		}{
			{path: "/api/v1/playback/prepare", body: `{"sourceRef":"opaque-contract-source","startSeconds":-1}`},
			{path: "/api/v1/playback/prepare", body: `{"sourceRef":"opaque-contract-source","startSeconds":1.5}`},
			{path: "/api/v1/playback/prepare", body: `{"sourceRef":"opaque-contract-source","startSeconds":604801}`},
			{path: "/api/v1/playback/resolve", body: `{"sourceRef":"opaque-contract-source","startSeconds":-1}`},
			{path: "/api/v1/playback/resolve", body: `{"sourceRef":"opaque-contract-source","startSeconds":1.5}`},
			{path: "/api/v1/playback/resolve", body: `{"sourceRef":"opaque-contract-source","startSeconds":604801}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt9001001","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"processingModes":["transcode","transcode"]}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt9001001","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumHeight":143}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt9001001","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumHeight":4321}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt9001001","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumVideoBitrateKbps":63}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt9001001","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumVideoBitrateKbps":200001}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt9001001","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumAudioChannels":33}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt9001001","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"subtitleModes":["external","external"]}}`},
		}
		for _, fixture := range invalidBodies {
			validateContractRequestBody(t, document, http.MethodPost, fixture.path, fixture.body, false)
		}

		errorFixtures := []struct {
			name       string
			path       string
			err        error
			status     int
			retryAfter string
		}{
			{name: "prepare transcoding disabled", path: "/api/v1/playback/prepare", err: playback.ErrTranscodingDisabled, status: http.StatusUnprocessableEntity},
			{name: "prepare capability missing", path: "/api/v1/playback/prepare", err: playback.ErrClientCapabilityMissing, status: http.StatusUnprocessableEntity},
			{name: "prepare upstream", path: "/api/v1/playback/prepare", err: playback.ErrMediaSourceFailed, status: http.StatusBadGateway},
			{name: "prepare capacity", path: "/api/v1/playback/prepare", err: playback.ErrMediaCapacityReached, status: http.StatusServiceUnavailable, retryAfter: "10"},
			{name: "prepare storage", path: "/api/v1/playback/prepare", err: playback.ErrMediaStorageLimit, status: http.StatusInsufficientStorage},
			{name: "resolve transcoding disabled", path: "/api/v1/playback/resolve", err: playback.ErrTranscodingDisabled, status: http.StatusUnprocessableEntity},
			{name: "resolve capability missing", path: "/api/v1/playback/resolve", err: playback.ErrClientCapabilityMissing, status: http.StatusUnprocessableEntity},
			{name: "resolve upstream", path: "/api/v1/playback/resolve", err: playback.ErrMediaSourceFailed, status: http.StatusBadGateway},
			{name: "resolve capacity", path: "/api/v1/playback/resolve", err: playback.ErrMediaCapacityReached, status: http.StatusServiceUnavailable, retryAfter: "10"},
			{name: "resolve storage", path: "/api/v1/playback/resolve", err: playback.ErrMediaStorageLimit, status: http.StatusInsufficientStorage},
		}
		for _, fixture := range errorFixtures {
			t.Run(fixture.name, func(t *testing.T) {
				service := &fakePlaybackService{}
				if fixture.path == "/api/v1/playback/prepare" {
					service.prepareErr = fixture.err
				} else {
					service.resolveErr = fixture.err
				}
				errorAPI := testAPI(&fakeInstanceService{})
				errorAPI.auth = &fakeAuthService{principal: contractPrincipal()}
				errorAPI.playback = service
				errorAPI.settings = &fakeSettingsService{}
				request := authenticatedContractRequest(http.MethodPost, fixture.path, bytes.NewBufferString(`{"sourceRef":"opaque-contract-source"}`))
				request.Header.Set("Content-Type", "application/json")
				response := serveContractRequest(t, errorAPI, request, fixture.status)
				if response.Header().Get("Retry-After") != fixture.retryAfter {
					t.Fatalf("unexpected Retry-After header %q", response.Header().Get("Retry-After"))
				}
				validateContractResponse(t, document, fixture.path, nil, request, response)
			})
		}
	})

	t.Run("collection assignments", func(t *testing.T) {
		const collectionID = "88888888-8888-4888-8888-888888888888"
		now := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)
		stored := collection.Collection{
			ID: collectionID, Title: "Contract collection",
			BackdropImageURL: "https://art.example/private/backdrop.jpg?token=contract-secret",
			ViewMode:         "rows", FolderCoverShape: "poster",
			Folders: []collection.Folder{{
				ID: "99999999-9999-4999-8999-999999999999", Title: "Featured", TileShape: "poster", SourceView: "merged",
				CoverImageURL: "https://art.example/private/cover.jpg?token=contract-secret",
				Sources: []collection.Source{{
					ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Kind: collection.SourceKindTMDB, Title: "Popular",
					TMDB: &collection.TMDBSource{SourceType: "discover", MediaType: "movie", Sort: "popularity.desc"},
				}},
			}},
			ProfileIDs: []string{}, CategoryIDs: []string{contractCategoryID}, Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		service := &fakeCollectionService{managementValue: stored, listValue: []collection.Collection{stored}, saveValue: stored}
		api := collectionAPI(service)

		list := authenticatedContractRequest(http.MethodGet, "/api/v1/collections", nil)
		listResponse := serveContractRequest(t, api, list, http.StatusOK)
		validateContractResponse(t, document, "/collections", nil, list, listResponse)

		assignmentBody := contractCollectionAssignmentBody(`,"profileIds":[],"categoryIds":["` + contractCategoryID + `"]`)
		omittedBody := contractCollectionAssignmentBody("")
		bothEmptyBody := contractCollectionAssignmentBody(`,"profileIds":[],"categoryIds":[]`)
		profileEmptyBody := contractCollectionAssignmentBody(`,"profileIds":[]`)
		categoryEmptyBody := contractCollectionAssignmentBody(`,"categoryIds":[]`)
		duplicateCategoryBody := contractCollectionAssignmentBody(`,"categoryIds":["` + contractCategoryID + `","` + contractCategoryID + `"]`)
		invalidProfileBody := contractCollectionAssignmentBody(`,"profileIds":["not-a-uuid"]`)
		tooManyCategoriesBody := contractCollectionAssignmentBody(`,"categoryIds":` + contractUUIDArray(101))
		for _, endpoint := range []struct {
			method string
			path   string
			status int
		}{
			{method: http.MethodPost, path: "/api/v1/collections", status: http.StatusCreated},
			{method: http.MethodPut, path: "/api/v1/collections/" + collectionID, status: http.StatusOK},
		} {
			validateContractRequestBody(t, document, endpoint.method, endpoint.path, assignmentBody, true)
			validateContractRequestBody(t, document, endpoint.method, endpoint.path, omittedBody, true)
			validateContractRequestBody(t, document, endpoint.method, endpoint.path, duplicateCategoryBody, false)
			validateContractRequestBody(t, document, endpoint.method, endpoint.path, invalidProfileBody, false)
			validateContractRequestBody(t, document, endpoint.method, endpoint.path, tooManyCategoriesBody, false)
			request := authenticatedContractRequest(endpoint.method, endpoint.path, bytes.NewBufferString(assignmentBody))
			request.Header.Set("Content-Type", "application/json")
			response := serveContractRequest(t, api, request, endpoint.status)
			validateContractResponse(t, document, endpoint.path, nil, request, response)
		}
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/collections", bothEmptyBody, false)
		validateContractRequestBody(t, document, http.MethodPut, "/api/v1/collections/"+collectionID, bothEmptyBody, false)
		validateContractRequestBody(t, document, http.MethodPut, "/api/v1/collections/"+collectionID, profileEmptyBody, true)
		validateContractRequestBody(t, document, http.MethodPut, "/api/v1/collections/"+collectionID, categoryEmptyBody, true)
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/collections", profileEmptyBody, false)
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/collections", categoryEmptyBody, false)

		management := authenticatedContractRequest(http.MethodGet, "/api/v1/collections/"+collectionID+"/management", nil)
		managementResponse := serveContractRequest(t, api, management, http.StatusOK)
		validateContractResponse(t, document, "/collections/{collectionId}/management", map[string]string{"collectionId": collectionID}, management, managementResponse)

		portable := `{"schemaVersion":1,"collections":[{"title":"Portable","heroEnabled":false,"pinToTop":false,"focusGlowEnabled":false,"viewMode":"rows","folderCoverShape":"poster","folders":[{"title":"Featured","tileShape":"poster","sourceView":"merged","focusGifEnabled":false,"hideTitle":false,"sources":[{"kind":"tmdb","title":"Popular","tmdb":{"sourceType":"discover","mediaType":"movie","sort":"popularity.desc","filters":{}}}]}]}]}`
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/collections/import", portable, true)
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/collections/import", `{"schemaVersion":1,"collections":[{"title":"Portable","heroEnabled":false,"pinToTop":false,"focusGlowEnabled":false,"viewMode":"rows","folderCoverShape":"poster","folders":[],"categoryIds":["`+contractCategoryID+`"]}]}`, false)
		invalidAssignments := []struct {
			name   string
			method string
			path   string
			body   string
		}{
			{name: "create null profileIds", method: http.MethodPost, path: "/api/v1/collections", body: contractCollectionAssignmentBody(`,"profileIds":null`)},
			{name: "create null categoryIds", method: http.MethodPost, path: "/api/v1/collections", body: contractCollectionAssignmentBody(`,"categoryIds":null`)},
			{name: "update null profileIds", method: http.MethodPut, path: "/api/v1/collections/" + collectionID, body: contractCollectionAssignmentBody(`,"profileIds":null`)},
			{name: "update null categoryIds", method: http.MethodPut, path: "/api/v1/collections/" + collectionID, body: contractCollectionAssignmentBody(`,"categoryIds":null`)},
			{name: "create unknown member", method: http.MethodPost, path: "/api/v1/collections", body: contractCollectionAssignmentBody(`,"unexpectedAssignment":[]`)},
			{name: "portable null profileIds", method: http.MethodPost, path: "/api/v1/collections/import", body: `{"schemaVersion":1,"collections":[{"profileIds":null}]}`},
			{name: "portable null categoryIds", method: http.MethodPost, path: "/api/v1/collections/import", body: `{"schemaVersion":1,"collections":[{"categoryIds":null}]}`},
			{name: "portable unknown member", method: http.MethodPost, path: "/api/v1/collections/import", body: `{"schemaVersion":1,"collections":[{"unexpectedAssignment":[]}]}`},
		}
		for _, invalid := range invalidAssignments {
			t.Run(invalid.name, func(t *testing.T) {
				validateContractRequestBody(t, document, invalid.method, invalid.path, invalid.body, false)
				request := authenticatedContractRequest(invalid.method, invalid.path, bytes.NewBufferString(invalid.body))
				request.Header.Set("Content-Type", "application/json")
				response := serveContractRequest(t, api, request, http.StatusBadRequest)
				validateContractResponse(t, document, invalid.path, nil, request, response)
			})
		}
	})

	t.Run("custom series resolution", func(t *testing.T) {
		seasonID := "88888888-8888-4888-8888-888888888888"
		episodeID := "99999999-9999-4999-8999-999999999999"
		service := &fakeWatchstateService{customValue: watchstate.ResolveCustomSeriesResult{
			Series:  watchstate.CustomSeriesReference{TitleID: contractTitleID, ResourceID: "opaque:series"},
			Seasons: []watchstate.CustomSeasonReference{{TitleID: seasonID, SeasonNumber: 1}},
			Videos:  []watchstate.CustomVideoReference{{TitleID: episodeID, ResourceID: "opaque:episode", SeasonTitleID: seasonID, SeasonNumber: 1, EpisodeNumber: 2}},
		}}
		api := watchstateAPI(service)
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		body := `{"sourceAddonId":"` + contractAddonID + `","sourceType":"anime","series":{"resourceId":"opaque:series","title":"Contract Custom Series","posterUrl":"/api/v1/artwork/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"videos":[{"resourceId":"opaque:episode","title":"Episode","seasonNumber":1,"episodeNumber":2}]}`
		request := authenticatedContractRequest(http.MethodPost, "/api/v1/titles/custom-series/resolve", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		response := serveContractRequest(t, api, request, http.StatusOK)
		validateContractResponse(t, document, "/titles/custom-series/resolve", nil, request, response)

		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/titles/custom-series/resolve", body, true)
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/titles/custom-series/resolve", `{"sourceAddonId":"`+contractAddonID+`","sourceType":"anime","series":{"resourceId":"opaque","title":"Show"},"videos":[{"resourceId":"episode","seasonNumber":-1,"episodeNumber":0}]}`, false)
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/titles/custom-series/resolve", `{"sourceAddonId":"`+contractAddonID+`","sourceType":"anime","series":{"resourceId":"opaque","title":"Show"},"videos":[{"resourceId":"episode","seasonNumber":2147483648,"episodeNumber":0}]}`, false)
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/titles/custom-series/resolve", `{"sourceAddonId":"`+contractAddonID+`","sourceType":"anime","series":{"resourceId":"opaque","title":"Show"},"videos":[],"externalId":"canonical-leak"}`, false)
	})

	t.Run("local recommendations", func(t *testing.T) {
		service := &fakeWatchstateService{recommendationValue: watchstate.RecommendationPage{Items: []watchstate.Recommendation{{
			Item: watchstate.RecommendationTitle{
				ID: contractTitleID, MediaType: "movie", Title: "Contract recommendation",
				PosterURL:  "/api/v1/artwork/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				ResourceID: "opaque-title", ResourceProvider: "tmdb", ProviderIDs: map[string]string{"tmdb": "42"},
			},
			Reason: "Because you like Drama", Score: 12.5,
		}}}}
		api := watchstateAPI(service)
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		request := authenticatedContractRequest(http.MethodGet, "/api/v1/recommendations?limit=12", nil)
		response := serveContractRequest(t, api, request, http.StatusOK)
		validateContractResponse(t, document, "/recommendations", nil, request, response)
	})

	t.Run("playback coordination", func(t *testing.T) {
		now := time.Date(2026, time.August, 21, 20, 0, 0, 0, time.UTC)
		item := coordination.PlaybackItem{TitleID: contractTitleID, MediaType: "movie", ResourceID: "opaque", Title: "Contract movie"}
		service := &fakeCoordinationService{
			device:  coordination.Device{SessionID: contractSessionID, DeviceID: contractDeviceID, Name: "TV", Platform: "tvos", Capabilities: []string{"remote-control"}, State: coordination.DeviceState{Status: "paused", Item: &item, PositionMilliseconds: 1000, DurationMilliseconds: 10000, UpdatedAt: now}, Revision: 4, Current: true, LastSeenAt: now},
			command: coordination.Command{OperationID: "77777777-7777-4777-8777-777777777777", Command: "play", SenderDeviceName: "Phone", Status: "pending", CreatedAt: now, ExpiresAt: now.Add(2 * time.Minute)},
			room:    coordination.Room{ID: "88888888-8888-4888-8888-888888888888", JoinCode: "23456789AB", Item: item, State: "paused", PositionMilliseconds: 1000, DurationMilliseconds: 10000, Version: 1, UpdatedAt: now, ExpiresAt: now.Add(8 * time.Hour), Members: []coordination.RoomMember{{MemberID: "99999999-9999-4999-8999-999999999999", Profile: "Viewer", DeviceName: "Phone", Platform: "ios", Role: "host", Current: true, JoinedAt: now, LastSeenAt: now}}},
		}
		api := testAPI(&fakeInstanceService{})
		api.coordination = service
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		roomPath := "/api/v1/playback/rooms/88888888-8888-4888-8888-888888888888"
		requests := []struct {
			method, target, contractPath, body string
			status                             int
		}{
			{http.MethodPut, "/api/v1/playback/device", "/playback/device", `{"capabilities":["remote-control"],"state":{"status":"paused","item":{"titleId":"` + contractTitleID + `","mediaType":"movie","resourceId":"opaque","title":"Contract movie"},"positionMilliseconds":1000,"durationMilliseconds":10000}}`, http.StatusOK},
			{http.MethodGet, "/api/v1/playback/devices", "/playback/devices", "", http.StatusOK},
			{http.MethodPost, "/api/v1/playback/devices/" + contractSessionID + "/commands", "/playback/devices/{sessionId}/commands", `{"operationId":"77777777-7777-4777-8777-777777777777","command":"play"}`, http.StatusCreated},
			{http.MethodGet, "/api/v1/playback/commands?after=77777777-7777-4777-8777-777777777777", "/playback/commands", "", http.StatusOK},
			{http.MethodPut, "/api/v1/playback/commands/incoming/77777777-7777-4777-8777-777777777777/result", "/playback/commands/incoming/{operationId}/result", `{"status":"applied","code":"applied"}`, http.StatusOK},
			{http.MethodGet, "/api/v1/playback/commands/outgoing/77777777-7777-4777-8777-777777777777", "/playback/commands/outgoing/{operationId}", "", http.StatusOK},
			{http.MethodPost, "/api/v1/playback/rooms", "/playback/rooms", `{"item":{"titleId":"` + contractTitleID + `","mediaType":"movie","resourceId":"opaque","title":"Contract movie"},"state":"paused","positionMilliseconds":1000,"durationMilliseconds":10000}`, http.StatusCreated},
			{http.MethodPost, "/api/v1/playback/rooms/join", "/playback/rooms/join", `{"code":"23456789AB"}`, http.StatusOK},
			{http.MethodGet, roomPath, "/playback/rooms/{roomId}", "", http.StatusOK},
			{http.MethodPut, roomPath, "/playback/rooms/{roomId}", `{"state":"playing","positionMilliseconds":2000,"durationMilliseconds":10000,"expectedVersion":1}`, http.StatusOK},
			{http.MethodDelete, roomPath, "/playback/rooms/{roomId}", "", http.StatusNoContent},
		}
		for _, test := range requests {
			request := authenticatedContractRequest(test.method, test.target, bytes.NewBufferString(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := serveContractRequest(t, api, request, test.status)
			parameters := map[string]string(nil)
			if strings.Contains(test.contractPath, "{sessionId}") {
				parameters = map[string]string{"sessionId": contractSessionID}
			}
			if strings.Contains(test.contractPath, "{operationId}") {
				parameters = map[string]string{"operationId": "77777777-7777-4777-8777-777777777777"}
			}
			if strings.Contains(test.contractPath, "{roomId}") {
				parameters = map[string]string{"roomId": service.room.ID}
			}
			validateContractResponse(t, document, test.contractPath, parameters, request, response)
		}
	})

	t.Run("operations", func(t *testing.T) {
		nextRun := time.Date(2026, time.August, 3, 2, 0, 0, 0, time.UTC)
		lastStarted := nextRun.Add(-24 * time.Hour)
		lastCompleted := lastStarted.Add(72 * time.Second)
		lastStatus := "partial"
		started := time.Date(2026, time.August, 2, 14, 30, 0, 0, time.UTC)
		completed := started.Add(42 * time.Second)
		service := &fakeOperationsService{
			overview: operations.OperationsOverview{
				MetadataCache: operations.MetadataCacheStatus{
					Entries: 1842, FreshEntries: 1730, ExpiredEntries: 112,
					RootTitles: 920, MissingTitles: 23, ArtworkSnapshots: 917,
				},
				MetadataRefresh: operations.MetadataRefreshSchedule{
					Task: "metadata-refresh", Enabled: true, IntervalHours: 24, Language: "en-US", BatchSize: 50,
					NextRunAt: &nextRun, LastStartedAt: &lastStarted, LastCompletedAt: &lastCompleted,
					LastStatus: &lastStatus, LastResult: &operations.MetadataRefreshResult{Candidates: 50, Refreshed: 48, Failed: 2},
				},
				PostgreSQLPool: operations.PostgreSQLPoolStatus{
					Acquired: 2, Idle: 3, Total: 5, Max: 10, WaitCount: 7, WaitDurationMilliseconds: 145,
				},
				TrackingOutbox: operations.TrackingOutboxStatus{Pending: 12, Due: 3, OldestAgeSeconds: 420},
				Addons:         operations.AddonStatus{Total: 8, Enabled: 7, LatestUpdatedAt: &lastCompleted},
				Playback:       operations.PlaybackStatus{Active: 4, Transcoding: 2},
				SemanticExtension: operations.SemanticExtensionOperationsStatus{
					Enabled: true, WarmupStatus: "ready", PersistentStatus: "ready",
					MemoryEntries: 3, PersistentEntries: 2, Hits: 11, Misses: 5, CoalescedWaiters: 4,
					Executions: 6, Successes: 3, Timeouts: 1, Failures: 1, Cancellations: 1, BusyFallbacks: 2,
					Active: 1, Queued: 1, LatencyP50Milliseconds: 18, LatencyP95Milliseconds: 91,
				},
				HousekeepingIntervalMinutes: 5,
			},
			schedule: operations.MetadataRefreshSchedule{
				Task: "metadata-refresh", Enabled: true, IntervalHours: 168, Language: "fr-FR", BatchSize: 25, NextRunAt: &nextRun,
			},
		}
		api := authenticatedOperationsAPI(service)

		overview := authenticatedContractRequest(http.MethodGet, "/api/v1/operations", nil)
		overviewResponse := serveContractRequest(t, api, overview, http.StatusOK)
		validateContractResponse(t, document, "/operations", nil, overview, overviewResponse)

		schedule := authenticatedContractRequest(http.MethodPut, "/api/v1/operations/schedules/metadata-refresh", bytes.NewBufferString(`{"enabled":true,"intervalHours":168,"language":"fr-FR","batchSize":25}`))
		schedule.Header.Set("Content-Type", "application/json")
		scheduleResponse := serveContractRequest(t, api, schedule, http.StatusOK)
		validateContractResponse(t, document, "/operations/schedules/metadata-refresh", nil, schedule, scheduleResponse)

		actionFixtures := []struct {
			name   string
			action operations.OperationAction
			status string
			result operations.OperationResult
		}{
			{
				name: "metadata refresh", action: operations.ActionFetchMissingMetadata, status: "partial",
				result: operations.OperationResult{Metadata: &operations.MetadataRefreshResult{Candidates: 50, Refreshed: 48, Failed: 2}},
			},
			{
				name: "metadata cache", action: operations.ActionClearMetadataCache, status: "succeeded",
				result: operations.OperationResult{MetadataCache: &operations.MetadataCacheClearResult{EntriesDeleted: 1842}},
			},
			{
				name: "playback cache", action: operations.ActionClearStreamCache, status: "succeeded",
				result: operations.OperationResult{Playback: &playback.PurgeResult{SessionsRemoved: 11, JobsStopped: 4, StorageBytes: 0}},
			},
		}
		for _, fixture := range actionFixtures {
			t.Run(fixture.name, func(t *testing.T) {
				service.run = operations.OperationRun{
					Action: fixture.action, StartedAt: started, CompletedAt: completed, Status: fixture.status, Result: fixture.result,
				}
				action := authenticatedContractRequest(http.MethodPost, "/api/v1/operations/actions/"+string(fixture.action), nil)
				actionResponse := serveContractRequest(t, api, action, http.StatusOK)
				validateContractResponse(t, document, "/operations/actions/{action}", map[string]string{"action": string(fixture.action)}, action, actionResponse)
			})
		}
	})
}

func TestOpenAPISavedResourceUpdateBodies(t *testing.T) {
	document := loadOpenAPIContract(t)
	saved := `{"name":"Space","query":"space opera","sort":"relevance","expectedRevision":2}`
	validateContractRequestBody(t, document, http.MethodPut, "/api/v1/saved-searches/11111111-1111-4111-8111-111111111111", saved, true)
	validateContractRequestBody(t, document, http.MethodPut, "/api/v1/saved-searches/11111111-1111-4111-8111-111111111111", `{"name":"Space","query":"space opera","sort":"relevance","expectedRevision":2,"sql":"unsafe"}`, false)
	smart := `{"name":"Drama","rules":{"type":"genre","operator":"equals","value":"drama"},"sort":"title","expectedRevision":3}`
	validateContractRequestBody(t, document, http.MethodPut, "/api/v1/smart-collections/11111111-1111-4111-8111-111111111111", smart, true)
	validateContractRequestBody(t, document, http.MethodPut, "/api/v1/smart-collections/11111111-1111-4111-8111-111111111111", `{"name":"Drama","rules":{"type":"genre","operator":"equals","value":"drama"},"sort":"title","expectedRevision":3,"expression":"unsafe"}`, false)
}

func TestAddonDiagnosticsOpenAPIRejectsPrivateDetails(t *testing.T) {
	validator := loadOpenAPIContract(t)
	request := authenticatedContractRequest(http.MethodGet, "/api/v1/addons/diagnostics", nil)
	response := httptest.NewRecorder()
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_, _ = response.WriteString(`{"observedSince":"2026-08-06T12:00:00Z","diagnostics":[{"addonId":"66666666-6666-4666-8666-666666666666","state":"unavailable","lastError":{"code":"unavailable","at":"2026-08-06T12:00:01Z","message":"private upstream body"},"capabilities":{"resources":["catalog"],"search":false,"pagination":false,"searchPagination":false},"transportUrl":"https://private.invalid/manifest.json?token=secret"}]}`)

	valid, _ := validator.ValidateHttpResponse(request, response.Result())
	if valid {
		t.Fatal("diagnostics OpenAPI response accepted private transport and raw error fields")
	}
}

func TestOperationsOpenAPIRejectsPrivateSemanticExtensionDetails(t *testing.T) {
	validator := loadOpenAPIContract(t)
	baseline := `{"metadataCache":{"entries":0,"freshEntries":0,"expiredEntries":0,"rootTitles":0,"missingTitles":0,"artworkSnapshots":0},"metadataRefresh":{"task":"metadata-refresh","enabled":false,"intervalHours":24,"language":"en-US","batchSize":25,"nextRunAt":null,"lastStartedAt":null,"lastCompletedAt":null,"lastStatus":null,"lastResult":null},"postgresqlPool":{"acquired":0,"idle":1,"total":1,"max":10,"waitCount":0,"waitDurationMilliseconds":0},"trackingOutbox":{"pending":0,"due":0,"oldestAgeSeconds":0},"addons":{"total":0,"enabled":0,"latestUpdatedAt":null},"playback":{"active":0,"transcoding":0},"semanticExtension":{"enabled":true,"warmupStatus":"failed","persistentStatus":"ready","memoryEntries":0,"persistentEntries":0,"hits":0,"misses":0,"coalescedWaiters":0,"executions":1,"successes":0,"timeouts":0,"failures":0,"cancellations":1,"busyFallbacks":0,"active":0,"queued":0,"latencyP50Milliseconds":0,"latencyP95Milliseconds":0},"housekeepingIntervalMinutes":5}`
	validate := func(body string) (bool, []error) {
		request := authenticatedContractRequest(http.MethodGet, "/api/v1/operations", nil)
		response := httptest.NewRecorder()
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("X-Request-ID", "operations-semantic-contract")
		response.WriteHeader(http.StatusOK)
		_, _ = response.WriteString(body)
		valid, validationErrors := validator.ValidateHttpResponse(request, response.Result())
		joined := make([]error, len(validationErrors))
		for index, validationError := range validationErrors {
			joined[index] = validationError
		}
		return valid, joined
	}
	if valid, validationErrors := validate(baseline); !valid {
		t.Fatalf("safe semantic extension operations response was invalid: %v", validationErrors)
	}
	private := strings.Replace(baseline, `"latencyP95Milliseconds":0`, `"latencyP95Milliseconds":0,"model":"private-model","rawError":"private upstream body","query":"private query","key":"private key"`, 1)
	if valid, _ := validate(private); valid {
		t.Fatal("operations OpenAPI response accepted private semantic extension details")
	}
}

func TestSemanticSearchOpenAPIAcceptsUnavailableResponse(t *testing.T) {
	validator := loadOpenAPIContract(t)
	request := authenticatedContractRequest(http.MethodPost, "/api/v1/search/semantic", bytes.NewBufferString(`{"query":"recent science fiction"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	response.Header().Set("X-Request-ID", "contract-semantic-unavailable")
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusServiceUnavailable)
	_, _ = response.WriteString(`{"error":{"code":"semantic_search_unavailable","message":"Semantic search is temporarily unavailable"}}`)
	valid, validationErrors := validator.ValidateHttpResponse(request, response.Result())
	if !valid {
		t.Fatalf("semantic search 503 response violates OpenAPI: %v", validationErrors)
	}
}

func TestAddonVerificationOpenAPIRejectsPrivateDetails(t *testing.T) {
	validator := loadOpenAPIContract(t)
	request := authenticatedContractRequest(http.MethodPost, "/api/v1/addons/verifications", bytes.NewBufferString(`{"transportUrl":"https://request.invalid/manifest.json"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusCreated)
	_, _ = response.WriteString(`{"id":"66666666-6666-4666-8666-666666666666","status":"failed","summary":"manifest_unavailable","checks":[{"code":"manifest_fetch","status":"failed"},{"code":"manifest_valid","status":"skipped"},{"code":"catalog_probe","status":"skipped"}],"profileIds":[],"categoryIds":[],"createdAt":"2026-08-21T20:00:00Z","expiresAt":"2026-08-21T20:10:00Z","transportUrl":"https://private.invalid/manifest.json?token=secret","providerError":"private response body"}`)
	valid, _ := validator.ValidateHttpResponse(request, response.Result())
	if valid {
		t.Fatal("verification OpenAPI response accepted private transport and provider error fields")
	}
}

func TestAssignmentOpenAPIResponsesRequireBoundedSeparateUUIDArrays(t *testing.T) {
	validator := loadOpenAPIContract(t)
	verificationPrefix := `{"id":"66666666-6666-4666-8666-666666666666","status":"passed","summary":"ready","checks":[{"code":"manifest_fetch","status":"passed"},{"code":"manifest_valid","status":"passed"},{"code":"catalog_probe","status":"skipped"}],"manifest":{"id":"org.rivune.verify","version":"1.0.0","name":"Verify","types":["movie"],"resources":["catalog"],"catalogs":[]},"capabilities":{"resources":["catalog"],"search":false,"pagination":false,"searchPagination":false},"profileIds":[]`
	addonPrefix := `{"addons":[{"id":"` + contractAddonID + `","manifest":{"id":"org.rivune.contract","version":"1.0.0","name":"Contract","types":["movie"],"resources":["catalog"],"catalogs":[]},"position":0,"profileIds":[],"categoryIds":`
	collectionWithInvalidProfile := `{"collections":[{"id":"88888888-8888-4888-8888-888888888888","title":"Contract","heroEnabled":false,"pinToTop":false,"focusGlowEnabled":false,"viewMode":"rows","folderCoverShape":"poster","folders":[],"profileIds":["not-a-uuid"],"categoryIds":[],"position":0,"version":1,"createdAt":"2026-08-06T12:00:00Z","updatedAt":"2026-08-06T12:00:00Z"}]}`
	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "verification missing categoryIds", method: http.MethodPost, target: "/api/v1/addons/verifications", body: verificationPrefix + `,"createdAt":"2026-08-21T20:00:00Z","expiresAt":"2026-08-21T20:10:00Z"}`},
		{name: "verification duplicate categoryIds", method: http.MethodPost, target: "/api/v1/addons/verifications", body: verificationPrefix + `,"categoryIds":["` + contractCategoryID + `","` + contractCategoryID + `"],"createdAt":"2026-08-21T20:00:00Z","expiresAt":"2026-08-21T20:10:00Z"}`},
		{name: "addon list exceeds category maximum", method: http.MethodGet, target: "/api/v1/addons", body: addonPrefix + contractUUIDArray(101) + `,"enabled":true,"installedAt":"2026-08-06T12:00:00Z","updatedAt":"2026-08-06T12:00:00Z"}]}`},
		{name: "collection list rejects non-UUID profile", method: http.MethodGet, target: "/api/v1/collections", body: collectionWithInvalidProfile},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var request *http.Request
			if test.method == http.MethodPost {
				request = authenticatedContractRequest(test.method, test.target, bytes.NewBufferString(`{"transportUrl":"https://request.invalid/manifest.json"}`))
				request.Header.Set("Content-Type", "application/json")
			} else {
				request = authenticatedContractRequest(test.method, test.target, nil)
			}
			response := httptest.NewRecorder()
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusOK)
			_, _ = response.WriteString(test.body)
			valid, _ := validator.ValidateHttpResponse(request, response.Result())
			if valid {
				t.Fatalf("OpenAPI response accepted invalid assignment arrays: %s", test.body)
			}
		})
	}
}

func loadOpenAPIContract(t *testing.T) oasvalidator.Validator {
	t.Helper()
	contractValidatorOnce.Do(func() {
		specification, err := os.ReadFile("../../../protocol/openapi.yaml")
		if err != nil {
			contractValidatorErr = err
			return
		}
		document, err := libopenapi.NewDocument(specification)
		if err != nil {
			contractValidatorErr = err
			return
		}
		var validatorErrors []error
		contractValidator, validatorErrors = oasvalidator.NewValidator(document)
		if len(validatorErrors) > 0 {
			contractValidatorErr = errors.Join(validatorErrors...)
			return
		}
		if valid, validationErrors := contractValidator.ValidateDocument(); !valid {
			joined := make([]error, len(validationErrors))
			for index, validationError := range validationErrors {
				joined[index] = validationError
			}
			contractValidatorErr = errors.Join(joined...)
		}
	})
	if contractValidatorErr != nil {
		t.Fatalf("load OpenAPI response contract: %v", contractValidatorErr)
	}
	return contractValidator
}

func validateContractResponse(t *testing.T, validator oasvalidator.Validator, contractPath string, _ map[string]string, request *http.Request, response *httptest.ResponseRecorder) {
	t.Helper()
	valid, validationErrors := validator.ValidateHttpResponse(request, response.Result())
	if !valid {
		t.Fatalf("response violates OpenAPI contract for %s %s: %v\nbody: %s", request.Method, contractPath, validationErrors, response.Body.String())
	}
}

func validateContractRequestBody(t *testing.T, validator oasvalidator.Validator, method, target, body string, wantValid bool) {
	t.Helper()
	request := authenticatedContractRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	valid, validationErrors := validator.ValidateHttpRequest(request)
	if valid != wantValid {
		t.Fatalf("request validity for %s %s was %t, want %t: %v\nbody: %s", method, target, valid, wantValid, validationErrors, body)
	}
}

func serveContractRequest(t *testing.T, api *API, request *http.Request, expectedStatus int) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != expectedStatus {
		t.Fatalf("handler returned %d, want %d: %s", response.Code, expectedStatus, response.Body.String())
	}
	return response
}

func authenticatedContractRequest(method, target string, body *bytes.Buffer) *http.Request {
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
	}
	request.Header.Set("Authorization", "Bearer rivune_at_contract")
	return request
}

func contractPrincipal() auth.Principal {
	grantExpiresAt := time.Date(2026, time.July, 31, 14, 0, 0, 0, time.UTC)
	return auth.Principal{
		SessionID: contractSessionID, UserID: contractUserID, DeviceID: contractDeviceID,
		Username: "admin", Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: contractStringPointer(contractProfileID), ProfileGrantExpiresAt: &grantExpiresAt,
	}
}

func contractProfile() profile.Profile {
	return profile.Profile{
		ID: contractProfileID, CategoryID: contractCategoryID, CategoryName: "Uncategorized",
		Name: "Admin", CanManage: true, Enabled: true,
		AccessTimezone: "UTC", Accessible: true, AvatarKind: "preset", AvatarPreset: "aurora",
	}
}

func contractStringPointer(value string) *string {
	return &value
}

func instanceInfoForContract() instance.Info {
	return instance.Info{Name: "Rivune Contract", SetupRequired: false}
}

func contractUUIDArray(count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = fmt.Sprintf("%08x-0000-4000-8000-%012x", index+1, index+1)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func contractCollectionAssignmentBody(assignments string) string {
	return `{"title":"Contract collection","heroEnabled":false,"pinToTop":false,"focusGlowEnabled":false,"viewMode":"rows","folderCoverShape":"poster","folders":[{"title":"Featured","tileShape":"poster","focusGifEnabled":false,"hideTitle":false,"sources":[{"kind":"tmdb","title":"Popular","tmdb":{"sourceType":"discover","mediaType":"movie","sort":"popularity.desc","filters":{}}}]}]` + assignments + `}`
}

type fakeCoordinationService struct {
	device  coordination.Device
	command coordination.Command
	room    coordination.Room
}

func (f *fakeCoordinationService) Heartbeat(context.Context, auth.Principal, coordination.DeviceHeartbeatInput) (coordination.Device, error) {
	return f.device, nil
}
func (f *fakeCoordinationService) Devices(context.Context, auth.Principal) (coordination.DeviceList, error) {
	return coordination.DeviceList{Devices: []coordination.Device{f.device}}, nil
}
func (f *fakeCoordinationService) SendCommand(context.Context, auth.Principal, string, coordination.CommandInput) (coordination.Command, error) {
	return f.command, nil
}
func (f *fakeCoordinationService) Commands(context.Context, auth.Principal, string) (coordination.CommandList, error) {
	return coordination.CommandList{Commands: []coordination.Command{f.command}}, nil
}
func (f *fakeCoordinationService) CompleteCommand(context.Context, auth.Principal, string, coordination.CommandResultInput) (coordination.Command, error) {
	return f.command, nil
}
func (f *fakeCoordinationService) OutgoingCommand(context.Context, auth.Principal, string) (coordination.Command, error) {
	return f.command, nil
}
func (f *fakeCoordinationService) CreateRoom(context.Context, auth.Principal, coordination.CreateRoomInput) (coordination.Room, error) {
	return f.room, nil
}
func (f *fakeCoordinationService) JoinRoom(context.Context, auth.Principal, string) (coordination.Room, error) {
	return f.room, nil
}
func (f *fakeCoordinationService) Room(context.Context, auth.Principal, string) (coordination.Room, error) {
	return f.room, nil
}
func (f *fakeCoordinationService) UpdateRoom(context.Context, auth.Principal, string, coordination.UpdateRoomInput) (coordination.Room, error) {
	return f.room, nil
}
func (f *fakeCoordinationService) LeaveRoom(context.Context, auth.Principal, string) error {
	return nil
}
func (f *fakeCoordinationService) RunScheduled(context.Context) error { return nil }
