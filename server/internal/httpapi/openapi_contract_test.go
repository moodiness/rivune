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
	"github.com/moodiness/rivune/server/internal/category"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/instance"
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
			SetupRequired  bool  `json:"setupRequired"`
			SetupCompleted *bool `json:"setupCompleted"`
			DemoAvailable  *bool `json:"demoAvailable"`
		}
		decodeResponse(t, response, &state)
		if state.SetupRequired || state.SetupCompleted == nil || !*state.SetupCompleted || state.DemoAvailable == nil || *state.DemoAvailable {
			t.Fatalf("unexpected configured discovery lifecycle state: %+v", state)
		}
		validateContractResponse(t, document, "/.well-known/rivune", nil, request, response)
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
				Genres: []metadata.Genre{{ID: 18, Name: "Drama"}}, Cast: []metadata.CastMember{{ID: "819", Name: "Contract Actor", Character: "Narrator", ProfileURL: "https://image.example/actor.jpg"}}, VoteAverage: 7.5, VoteCount: 10,
				PosterURL: "https://fanart.example/movie-poster.jpg", BackdropURL: "https://fanart.example/movie-background.jpg",
				LogoURL:     "https://fanart.example/movie-logo.png",
				ExternalIDs: map[string]string{"tmdb": "550"},
			},
			seriesDetailsValue: metadata.Series{
				ID: contractTitleID, MediaType: metadata.MediaTypeSeries, Name: "Contract Series",
				OriginalName: "Contract Series", OriginalLanguage: "en", Overview: "Mapped series fixture.",
				Genres: []metadata.Genre{}, Cast: []metadata.CastMember{}, VoteAverage: 8, VoteCount: 20, Seasons: []metadata.SeasonSummary{},
				PosterURL: "https://fanart.example/series-poster.jpg", BackdropURL: "https://fanart.example/series-background.jpg",
				LogoURL: "https://fanart.example/series-logo.png",
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

	t.Run("transcoding settings", func(t *testing.T) {
		instanceAllowed := false
		profileMode := settings.TranscodingModeEnabled
		settingService := &fakeSettingsService{
			instance: settings.Layer{
				SchemaVersion: 1,
				Values:        settings.Values{AllowTranscoding: &instanceAllowed},
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
			Reason: "subtitle_burn_required", VideoAction: "transcode", AudioAction: "copy",
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
					ID: "source-1", SourceRef: "opaque-contract-source", AddonID: contractAddonID,
					ManifestID: "org.rivune.contract", StreamIndex: 0, Name: "Contract source",
					Protocol: "hls", Container: "mpegts", ExpiresAt: expiresAt,
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
				Diagnostics: playback.MediaDiagnostics{VideoEncoder: "libx264", HardwareToneMap: false},
				Sessions: []playback.ActivitySession{{
					ID: contractSessionID, Title: "Contract Movie", MediaType: "movie", Mode: "transcode",
					Decision: decision, Username: "admin", ProfileID: contractProfileID, Profile: "Admin",
					Device: "Contract iPhone", Platform: "ios", Processing: true, PositionSeconds: 120,
					DurationSeconds: 7200, CreatedAt: expiresAt.Add(-time.Hour), LastSeenAt: expiresAt.Add(-time.Minute),
					ExpiresAt: expiresAt,
				}},
				Jobs: []playback.MediaActivityJob{{
					SessionID: contractSessionID, AssetID: "source-1", Mode: "transcode", State: "processing",
					CreatedAt: expiresAt.Add(-time.Hour), LastSeenAt: expiresAt.Add(-time.Minute),
				}},
			},
		}
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: contractPrincipal()}
		api.playback = playbackService
		api.settings = &fakeSettingsService{}

		sourcesBody := `{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mpegts"],"processingModes":["remux","transcode_audio","transcode"],"maximumHeight":4320,"maximumVideoBitrateKbps":200000,"maximumAudioChannels":32,"subtitleModes":["external","burn"],"mediaProfiles":[{"container":"mp4","videoCodec":"h264","audioCodec":"aac"}]}}`
		validateContractRequestBody(t, document, http.MethodPost, "/api/v1/playback/sources", sourcesBody, true)
		sources := authenticatedContractRequest(http.MethodPost, "/api/v1/playback/sources", bytes.NewBufferString(sourcesBody))
		sources.Header.Set("Content-Type", "application/json")
		sourcesResponse := serveContractRequest(t, api, sources, http.StatusOK)
		validateContractResponse(t, document, "/playback/sources", nil, sources, sourcesResponse)

		prepareBody := `{"sourceRef":"opaque-contract-source","startSeconds":604800}`
		prepare := authenticatedContractRequest(http.MethodPost, "/api/v1/playback/prepare", bytes.NewBufferString(prepareBody))
		prepare.Header.Set("Content-Type", "application/json")
		prepareResponse := serveContractRequest(t, api, prepare, http.StatusOK)
		validateContractResponse(t, document, "/playback/prepare", nil, prepare, prepareResponse)

		resolveBody := `{"sourceRef":"opaque-contract-source","titleId":"` + contractTitleID + `","preferredAudioTrack":2,"preferredSubtitleId":"subtitle-1","startSeconds":0}`
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
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumHeight":144,"maximumVideoBitrateKbps":64,"maximumAudioChannels":1}}`},
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
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"processingModes":["transcode","transcode"]}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumHeight":143}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumHeight":4321}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumVideoBitrateKbps":63}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumVideoBitrateKbps":200001}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"maximumAudioChannels":33}}`},
			{path: "/api/v1/playback/sources", body: `{"mediaType":"movie","resourceId":"tt0137523","capabilities":{"streamingProtocols":["hls"],"containers":["mp4"],"subtitleModes":["external","external"]}}`},
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

	t.Run("collection management", func(t *testing.T) {
		const collectionID = "88888888-8888-4888-8888-888888888888"
		now := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)
		service := &fakeCollectionService{managementValue: collection.Collection{
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
			ProfileIDs: []string{contractProfileID}, Version: 1, CreatedAt: now, UpdatedAt: now,
		}}
		api := collectionAPI(service)
		request := authenticatedContractRequest(http.MethodGet, "/api/v1/collections/"+collectionID+"/management", nil)
		response := serveContractRequest(t, api, request, http.StatusOK)
		validateContractResponse(t, document, "/collections/{collectionId}/management", map[string]string{"collectionId": collectionID}, request, response)
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
