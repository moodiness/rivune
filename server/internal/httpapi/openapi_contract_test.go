package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/instance"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/profile"
	"github.com/pb33f/libopenapi"
	oasvalidator "github.com/pb33f/libopenapi-validator"
)

const (
	contractUserID    = "11111111-1111-4111-8111-111111111111"
	contractSessionID = "22222222-2222-4222-8222-222222222222"
	contractDeviceID  = "33333333-3333-4333-8333-333333333333"
	contractProfileID = "44444444-4444-4444-8444-444444444444"
	contractTitleID   = "55555555-5555-4555-8555-555555555555"
	contractAddonID   = "66666666-6666-4666-8666-666666666666"
	contractSeasonID  = "tvdb:77777777-7777-4777-8777-777777777777:1"
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
		validateContractResponse(t, document, "/.well-known/rivune", nil, request, response)
	})

	t.Run("authentication and profiles", func(t *testing.T) {
		now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
		authService := &fakeAuthService{
			loginTokens: auth.TokenPair{
				AccessToken: "rivune_at_contract", AccessExpiresAt: now.Add(15 * time.Minute),
				RefreshToken: "rivune_rt_contract", RefreshExpiresAt: now.Add(30 * 24 * time.Hour),
				SessionID: contractSessionID, DeviceID: contractDeviceID,
			},
			principal: contractPrincipal(),
			account: auth.Account{
				Principal: contractPrincipal(),
				Profiles: []auth.Profile{{
					ID: contractProfileID, Name: "Admin", CanManage: true, Enabled: true,
					AccessTimezone: "UTC", Accessible: true, AvatarKind: "preset", AvatarPreset: "aurora",
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

		account := authenticatedContractRequest(http.MethodGet, "/api/v1/auth/me", nil)
		accountResponse := serveContractRequest(t, api, account, http.StatusOK)
		validateContractResponse(t, document, "/auth/me", nil, account, accountResponse)

		profiles := httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil)
		profiles.Header.Set("Authorization", "Bearer rivune_at_contract")
		profilesResponse := serveContractRequest(t, api, profiles, http.StatusOK)
		validateContractResponse(t, document, "/profiles", nil, profiles, profilesResponse)
	})

	t.Run("metadata and plural trailers", func(t *testing.T) {
		metadataService := &fakeMetadataService{
			detailsMovie: metadata.Movie{
				ID: contractTitleID, MediaType: metadata.MediaTypeMovie, Title: "Contract Movie",
				OriginalTitle: "Contract Movie", OriginalLanguage: "en", Overview: "A deterministic contract fixture.",
				Genres: []metadata.Genre{{ID: 18, Name: "Drama"}}, VoteAverage: 7.5, VoteCount: 10,
				ExternalIDs: map[string]string{"tmdb": "550"},
			},
			seriesDetailsValue: metadata.Series{
				ID: contractTitleID, MediaType: metadata.MediaTypeSeries, Name: "Contract Series",
				OriginalName: "Contract Series", OriginalLanguage: "en", Overview: "Mapped series fixture.",
				Genres: []metadata.Genre{}, VoteAverage: 8, VoteCount: 20, Seasons: []metadata.SeasonSummary{},
				Aliases: []metadata.Alias{}, EpisodeOrders: []metadata.EpisodeOrder{}, MappingProvider: "tvdb",
				ExternalIDs: map[string]string{"tmdb": "1396", "tvdb": "81189"},
			},
			seasonDetailsValue: metadata.Season{
				ID: contractSeasonID, MediaType: metadata.MediaTypeSeason, SeriesID: contractTitleID,
				Name: "Season 1", Overview: "Mapped season fixture.", SeasonNumber: 1, VoteAverage: 8,
				Episodes: []metadata.Episode{}, ExternalIDs: map[string]string{"tvdb": "349232"},
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

	t.Run("playback", func(t *testing.T) {
		expiresAt := time.Date(2026, time.July, 31, 13, 0, 0, 0, time.UTC)
		playbackService := &fakePlaybackService{
			sources: playback.SourceList{
				Sources: []playback.SourceOption{{
					ID: "source-1", SourceRef: "opaque-contract-source", AddonID: contractAddonID,
					ManifestID: "org.rivune.contract", StreamIndex: 0, Name: "Contract source",
					Protocol: "hls", Container: "mpegts", ExpiresAt: expiresAt,
				}},
				ProviderErrors: []playback.ProviderFailure{},
			},
			session: playback.Session{
				ID: contractSessionID, SelectedSourceID: "source-1",
				Sources: []playback.Source{{
					ID: "source-1", AddonID: contractAddonID, ManifestID: "org.rivune.contract",
					Mode: "direct", URL: "/api/v1/playback/sessions/" + contractSessionID + "/assets/media?token=opaque",
					Protocol: "hls", Container: "mpegts", Compatible: true,
				}},
				Subtitles: []playback.Subtitle{}, ProviderErrors: []playback.ProviderFailure{}, ExpiresAt: expiresAt,
			},
		}
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		api.playback = playbackService
		api.settings = &fakeSettingsService{}

		sources := authenticatedContractRequest(http.MethodPost, "/api/v1/playback/sources", bytes.NewBufferString(`{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mpegts"]}}`))
		sources.Header.Set("Content-Type", "application/json")
		sourcesResponse := serveContractRequest(t, api, sources, http.StatusOK)
		validateContractResponse(t, document, "/playback/sources", nil, sources, sourcesResponse)

		resolve := authenticatedContractRequest(http.MethodPost, "/api/v1/playback/resolve", bytes.NewBufferString(`{"sourceRef":"opaque-contract-source","titleId":"`+contractTitleID+`"}`))
		resolve.Header.Set("Content-Type", "application/json")
		resolveResponse := serveContractRequest(t, api, resolve, http.StatusCreated)
		validateContractResponse(t, document, "/playback/resolve", nil, resolve, resolveResponse)
	})
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
		Username: "admin", Role: "admin", ActiveProfileID: new(contractProfileID),
		ProfileGrantExpiresAt: &grantExpiresAt,
	}
}

func contractProfile() profile.Profile {
	return profile.Profile{
		ID: contractProfileID, Name: "Admin", CanManage: true, Enabled: true,
		AccessTimezone: "UTC", Accessible: true, AvatarKind: "preset", AvatarPreset: "aurora",
	}
}

func instanceInfoForContract() instance.Info {
	return instance.Info{Name: "Rivune Contract", SetupRequired: false}
}
