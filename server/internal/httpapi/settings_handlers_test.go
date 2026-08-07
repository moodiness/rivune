package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/jellyfin"
	"github.com/moodiness/rivune/server/internal/settings"
)

type fakeSettingsService struct {
	instance           settings.Layer
	instanceErr        error
	instancePatch      settings.Patch
	maintenance        settings.Maintenance
	maintenanceErr     error
	maintenanceUpdate  settings.Maintenance
	profile            settings.Layer
	profileErr         error
	profilePatch       settings.Patch
	effective          settings.Effective
	effectiveErr       error
	requestedProfileID string
}

func (f *fakeSettingsService) Instance(context.Context) (settings.Layer, error) {
	return f.instance, f.instanceErr
}
func (f *fakeSettingsService) InitializeJellyfinEnabled(context.Context, bool) (bool, error) {
	if f.instance.Values.JellyfinEnabled == nil {
		return false, f.instanceErr
	}
	return *f.instance.Values.JellyfinEnabled, f.instanceErr
}

func (f *fakeSettingsService) Maintenance(context.Context) (settings.Maintenance, error) {
	return f.maintenance, f.maintenanceErr
}

func (f *fakeSettingsService) UpdateMaintenance(_ context.Context, _ auth.Principal, state settings.Maintenance) (settings.Maintenance, error) {
	f.maintenanceUpdate = state
	if f.maintenanceErr != nil {
		return settings.Maintenance{}, f.maintenanceErr
	}
	f.maintenance = state
	return state, nil
}

func (f *fakeSettingsService) UpdateInstance(_ context.Context, _ auth.Principal, patch settings.Patch) (settings.Layer, error) {
	f.instancePatch = patch
	return f.instance, f.instanceErr
}

func (f *fakeSettingsService) Profile(_ context.Context, _ auth.Principal, id string) (settings.Layer, error) {
	f.requestedProfileID = id
	return f.profile, f.profileErr
}

func (f *fakeSettingsService) UpdateProfile(_ context.Context, _ auth.Principal, id string, patch settings.Patch) (settings.Layer, error) {
	f.requestedProfileID = id
	f.profilePatch = patch
	return f.profile, f.profileErr
}

func (f *fakeSettingsService) Effective(_ context.Context, _ auth.Principal, id string) (settings.Effective, error) {
	f.requestedProfileID = id
	return f.effective, f.effectiveErr
}

func TestUpdateProfileSettingsPreservesFalseAndNull(t *testing.T) {
	service := &fakeSettingsService{profile: settings.Layer{SchemaVersion: 1}}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id/settings", bytes.NewBufferString(`{"interfaceLanguage":"ar","preferDirectPlay":false,"hideUnreleased":true,"theme":null,"forcedSubtitleLanguage":"fr-CA"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if !service.profilePatch.InterfaceLanguage.Set || service.profilePatch.InterfaceLanguage.Value == nil || *service.profilePatch.InterfaceLanguage.Value != "ar" {
		t.Fatalf("interface language override was not preserved: %+v", service.profilePatch)
	}
	if service.requestedProfileID != "profile-id" || !service.profilePatch.PreferDirectPlay.Set || service.profilePatch.PreferDirectPlay.Value == nil || *service.profilePatch.PreferDirectPlay.Value {
		t.Fatalf("false boolean override was not preserved: %+v", service.profilePatch)
	}
	if !service.profilePatch.HideUnreleased.Set || service.profilePatch.HideUnreleased.Value == nil || !*service.profilePatch.HideUnreleased.Value {
		t.Fatalf("hide-unreleased boolean override was not preserved: %+v", service.profilePatch)
	}
	if !service.profilePatch.Theme.Set || service.profilePatch.Theme.Value != nil {
		t.Fatalf("null theme did not clear the override: %+v", service.profilePatch)
	}
	if !service.profilePatch.ForcedSubtitleLanguage.Set || service.profilePatch.ForcedSubtitleLanguage.Value == nil || *service.profilePatch.ForcedSubtitleLanguage.Value != "fr-CA" {
		t.Fatalf("forced subtitle language override was not preserved: %+v", service.profilePatch)
	}
}

func TestUpdateInstanceSettingsDecodesEveryNewField(t *testing.T) {
	service := &fakeSettingsService{instance: settings.Layer{SchemaVersion: 1}}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", bytes.NewBufferString(
		`{"maximumCastMembers":35,"maximumDirectTitles":24,"autoplayNextEpisode":false,"skipIntroEnabled":true,"skipRecapEnabled":false,"skipOutroEnabled":true,"cardDensity":"compact","animationsEnabled":false,"subtitleSizePercent":75,"subtitleTextColor":"#a1b2c3","subtitleBackgroundOpacityPercent":25,"notificationsEnabled":false,"notificationDurationSeconds":7,"notificationPollIntervalSeconds":45}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	patch := service.instancePatch
	if !patch.MaximumCastMembers.Set || patch.MaximumCastMembers.Value == nil || *patch.MaximumCastMembers.Value != 35 ||
		!patch.MaximumDirectTitles.Set || patch.MaximumDirectTitles.Value == nil || *patch.MaximumDirectTitles.Value != 24 ||
		!patch.AutoplayNextEpisode.Set || patch.AutoplayNextEpisode.Value == nil || *patch.AutoplayNextEpisode.Value ||
		!patch.SkipIntroEnabled.Set || patch.SkipIntroEnabled.Value == nil || !*patch.SkipIntroEnabled.Value ||
		!patch.SkipRecapEnabled.Set || patch.SkipRecapEnabled.Value == nil || *patch.SkipRecapEnabled.Value ||
		!patch.SkipOutroEnabled.Set || patch.SkipOutroEnabled.Value == nil || !*patch.SkipOutroEnabled.Value ||
		!patch.CardDensity.Set || patch.CardDensity.Value == nil || *patch.CardDensity.Value != "compact" ||
		!patch.AnimationsEnabled.Set || patch.AnimationsEnabled.Value == nil || *patch.AnimationsEnabled.Value ||
		!patch.SubtitleSizePercent.Set || patch.SubtitleSizePercent.Value == nil || *patch.SubtitleSizePercent.Value != 75 ||
		!patch.SubtitleTextColor.Set || patch.SubtitleTextColor.Value == nil || *patch.SubtitleTextColor.Value != "#a1b2c3" ||
		!patch.SubtitleBackgroundOpacityPercent.Set || patch.SubtitleBackgroundOpacityPercent.Value == nil || *patch.SubtitleBackgroundOpacityPercent.Value != 25 ||
		!patch.NotificationsEnabled.Set || patch.NotificationsEnabled.Value == nil || *patch.NotificationsEnabled.Value ||
		!patch.NotificationDurationSeconds.Set || patch.NotificationDurationSeconds.Value == nil || *patch.NotificationDurationSeconds.Value != 7 ||
		!patch.NotificationPollIntervalSeconds.Set || patch.NotificationPollIntervalSeconds.Value == nil || *patch.NotificationPollIntervalSeconds.Value != 45 {
		t.Fatalf("new settings patch was not decoded exactly: %+v", patch)
	}
}

func TestUpdateInstanceSettingsDecodesNullForEveryNewField(t *testing.T) {
	service := &fakeSettingsService{instance: settings.Layer{SchemaVersion: 1}}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", bytes.NewBufferString(
		`{"interfaceLanguage":null,"maximumCastMembers":null,"maximumDirectTitles":null,"autoplayNextEpisode":null,"skipIntroEnabled":null,"skipRecapEnabled":null,"skipOutroEnabled":null,"cardDensity":null,"animationsEnabled":null,"subtitleSizePercent":null,"subtitleTextColor":null,"subtitleBackgroundOpacityPercent":null,"notificationsEnabled":null,"notificationDurationSeconds":null,"notificationPollIntervalSeconds":null}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	patch := service.instancePatch
	if !patch.InterfaceLanguage.Set || patch.InterfaceLanguage.Value != nil {
		t.Fatalf("interface language null was not preserved: %+v", patch)
	}
	if !patch.MaximumCastMembers.Set || patch.MaximumCastMembers.Value != nil ||
		!patch.MaximumDirectTitles.Set || patch.MaximumDirectTitles.Value != nil ||
		!patch.AutoplayNextEpisode.Set || patch.AutoplayNextEpisode.Value != nil ||
		!patch.SkipIntroEnabled.Set || patch.SkipIntroEnabled.Value != nil ||
		!patch.SkipRecapEnabled.Set || patch.SkipRecapEnabled.Value != nil ||
		!patch.SkipOutroEnabled.Set || patch.SkipOutroEnabled.Value != nil ||
		!patch.CardDensity.Set || patch.CardDensity.Value != nil ||
		!patch.AnimationsEnabled.Set || patch.AnimationsEnabled.Value != nil ||
		!patch.SubtitleSizePercent.Set || patch.SubtitleSizePercent.Value != nil ||
		!patch.SubtitleTextColor.Set || patch.SubtitleTextColor.Value != nil ||
		!patch.SubtitleBackgroundOpacityPercent.Set || patch.SubtitleBackgroundOpacityPercent.Value != nil ||
		!patch.NotificationsEnabled.Set || patch.NotificationsEnabled.Value != nil ||
		!patch.NotificationDurationSeconds.Set || patch.NotificationDurationSeconds.Value != nil ||
		!patch.NotificationPollIntervalSeconds.Set || patch.NotificationPollIntervalSeconds.Value != nil {
		t.Fatalf("new settings nulls were not preserved: %+v", patch)
	}
}
func TestInstanceSettingsResponseIncludesInterfaceLanguage(t *testing.T) {
	spanish := "es"
	service := &fakeSettingsService{instance: settings.Layer{
		SchemaVersion: 1,
		Values:        settings.Values{InterfaceLanguage: &spanish},
	}}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Settings settings.Values `json:"settings"`
	}
	decodeResponse(t, response, &body)
	if body.Settings.InterfaceLanguage == nil || *body.Settings.InterfaceLanguage != "es" {
		t.Fatalf("instance settings omitted interface language: %+v", body)
	}
}

func TestEffectiveSettingsResponseIncludesNewFieldsAndSources(t *testing.T) {
	effective := settings.Effective{
		SchemaVersion: 1,
		Values: settings.EffectiveValues{
			InterfaceLanguage:   "pt-BR",
			MaximumCastMembers:  20,
			MaximumDirectTitles: 12,
			AutoplayNextEpisode: true, CardDensity: "comfortable", AnimationsEnabled: true,
			SubtitleSizePercent: 100, SubtitleTextColor: "#FFFFFF", SubtitleBackgroundOpacityPercent: 60,
			NotificationsEnabled: true, NotificationDurationSeconds: 5, NotificationPollIntervalSeconds: 5, ForcedSubtitleLanguage: "fr-CA",
		},
		Sources: map[string]string{"interfaceLanguage": "profile", "maximumCastMembers": "instance", "maximumDirectTitles": "profile", "autoplayNextEpisode": "default", "subtitleTextColor": "profile", "forcedSubtitleLanguage": "instance"},
	}
	api := authenticatedSettingsAPI(&fakeSettingsService{effective: effective})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles/profile-id/settings/effective", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"maximumDirectTitles":12`)) {
		t.Fatalf("effective response omitted maximumDirectTitles JSON field: %s", response.Body.String())
	}
	var body struct {
		SchemaVersion int                      `json:"schemaVersion"`
		Settings      settings.EffectiveValues `json:"settings"`
		Sources       map[string]string        `json:"sources"`
	}
	decodeResponse(t, response, &body)
	if body.SchemaVersion != 1 || body.Settings.InterfaceLanguage != "pt-BR" || body.Settings.MaximumCastMembers != 20 || body.Settings.MaximumDirectTitles != 12 || body.Settings.SubtitleTextColor != "#FFFFFF" ||
		body.Settings.NotificationPollIntervalSeconds != 5 || body.Settings.ForcedSubtitleLanguage != "fr-CA" ||
		body.Sources["interfaceLanguage"] != "profile" || body.Sources["maximumCastMembers"] != "instance" || body.Sources["maximumDirectTitles"] != "profile" || body.Sources["autoplayNextEpisode"] != "default" ||
		body.Sources["subtitleTextColor"] != "profile" || body.Sources["forcedSubtitleLanguage"] != "instance" {
		t.Fatalf("effective response omitted new settings data: %+v", body)
	}
}

func TestEffectiveSettingsRequireSelectedProfile(t *testing.T) {
	service := &fakeSettingsService{effectiveErr: settings.ErrSelectionRequired}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles/profile-id/settings/effective", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", response.Code, response.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "profile_selection_required" {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

func TestUpdateMaintenanceSettingsPreservesDisabledAndMessage(t *testing.T) {
	service := &fakeSettingsService{}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/maintenance", bytes.NewBufferString(`{"enabled":false,"message":"Back shortly"}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.maintenanceUpdate.Enabled {
		t.Fatal("expected disabled maintenance setting")
	}
	if service.maintenanceUpdate.Message == nil || *service.maintenanceUpdate.Message != "Back shortly" {
		t.Fatalf("unexpected maintenance message %#v", service.maintenanceUpdate.Message)
	}
}

func TestUpdateMaintenanceSettingsRequiresEnabledBoolean(t *testing.T) {
	api := authenticatedSettingsAPI(&fakeSettingsService{})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/maintenance", bytes.NewBufferString(`{"message":"Back shortly"}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d: %s", response.Code, response.Body.String())
	}
}

func TestMaintenanceSettingsRequireGlobalAdministrator(t *testing.T) {
	categoryID := "11111111-1111-4111-8111-111111111111"
	for _, principal := range []auth.Principal{
		{UserID: "user-id", Role: "member", SessionID: "session-id", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID},
		{UserID: "user-id", Role: "admin", SessionID: "session-id", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID},
	} {
		api := testAPI(&fakeInstanceService{})
		api.auth = &fakeAuthService{principal: principal}
		api.settings = &fakeSettingsService{}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/settings/maintenance", nil)
		request.Header.Set("Authorization", "Bearer access-token")
		response := httptest.NewRecorder()

		api.Handler().ServeHTTP(response, request)

		if response.Code != http.StatusForbidden {
			t.Fatalf("expected status 403 for %#v, got %d: %s", principal, response.Code, response.Body.String())
		}
	}
}

func TestUpdateInstanceSettingsDecodesTranscodingBooleanAndNull(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		wantValue *bool
	}{
		{name: "false", body: `{"allowTranscoding":false}`, wantValue: new(bool)},
		{name: "true", body: `{"allowTranscoding":true}`, wantValue: func() *bool { value := true; return &value }()},
		{name: "null", body: `{"allowTranscoding":null}`, wantValue: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeSettingsService{instance: settings.Layer{SchemaVersion: 1}}
			api := authenticatedSettingsAPI(service)
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
			}
			if !service.instancePatch.AllowTranscoding.Set {
				t.Fatalf("allowTranscoding was not decoded: %+v", service.instancePatch)
			}
			if test.wantValue == nil {
				if service.instancePatch.AllowTranscoding.Value != nil {
					t.Fatalf("null allowTranscoding was not preserved: %+v", service.instancePatch)
				}
			} else if service.instancePatch.AllowTranscoding.Value == nil || *service.instancePatch.AllowTranscoding.Value != *test.wantValue {
				t.Fatalf("allowTranscoding value was not preserved: %+v", service.instancePatch)
			}
		})
	}
}

func TestUpdateProfileSettingsDecodesTranscodingModesAndNull(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		wantValue *string
	}{
		{name: "inherit", body: `{"transcoding":"inherit"}`, wantValue: func() *string { value := "inherit"; return &value }()},
		{name: "enabled", body: `{"transcoding":"enabled"}`, wantValue: func() *string { value := "enabled"; return &value }()},
		{name: "disabled", body: `{"transcoding":"disabled"}`, wantValue: func() *string { value := "disabled"; return &value }()},
		{name: "null", body: `{"transcoding":null}`, wantValue: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeSettingsService{profile: settings.Layer{SchemaVersion: 1}}
			api := authenticatedSettingsAPI(service)
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id/settings", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
			}
			if !service.profilePatch.Transcoding.Set {
				t.Fatalf("transcoding was not decoded: %+v", service.profilePatch)
			}
			if test.wantValue == nil {
				if service.profilePatch.Transcoding.Value != nil {
					t.Fatalf("null transcoding was not preserved: %+v", service.profilePatch)
				}
			} else if service.profilePatch.Transcoding.Value == nil || *service.profilePatch.Transcoding.Value != *test.wantValue {
				t.Fatalf("transcoding value was not preserved: %+v", service.profilePatch)
			}
		})
	}
}

func TestSettingsScopeAndPermissionErrorsUseExistingHTTPContract(t *testing.T) {
	for _, test := range []struct {
		name       string
		path       string
		body       string
		service    *fakeSettingsService
		wantStatus int
		wantCode   string
	}{
		{
			name:       "profile field at instance scope",
			path:       "/api/v1/settings",
			body:       `{"transcoding":"enabled"}`,
			service:    &fakeSettingsService{instanceErr: settings.ErrInvalidInput},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_settings",
		},
		{
			name:       "instance field at profile scope",
			path:       "/api/v1/profiles/profile-id/settings",
			body:       `{"allowTranscoding":true}`,
			service:    &fakeSettingsService{profileErr: settings.ErrInvalidInput},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_settings",
		},
		{
			name:       "instance permission remains admin only",
			path:       "/api/v1/settings",
			body:       `{"allowTranscoding":true}`,
			service:    &fakeSettingsService{instanceErr: settings.ErrForbidden},
			wantStatus: http.StatusForbidden,
			wantCode:   "settings_forbidden",
		},
		{
			name:       "profile update requires management access",
			path:       "/api/v1/profiles/profile-id/settings",
			body:       `{"interfaceLanguage":"fr"}`,
			service:    &fakeSettingsService{profileErr: settings.ErrForbidden},
			wantStatus: http.StatusForbidden,
			wantCode:   "settings_forbidden",
		},
		{
			name:       "notification fields are invalid at profile scope",
			path:       "/api/v1/profiles/profile-id/settings",
			body:       `{"notificationsEnabled":false}`,
			service:    &fakeSettingsService{profileErr: settings.ErrInvalidInput},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_settings",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := authenticatedSettingsAPI(test.service)
			request := httptest.NewRequest(http.MethodPatch, test.path, bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
			var body errorEnvelope
			decodeResponse(t, response, &body)
			if body.Error.Code != test.wantCode {
				t.Fatalf("error code = %q, want %q", body.Error.Code, test.wantCode)
			}
		})
	}
}

func TestEffectiveSettingsResponseIncludesTranscodingPolicyAndSources(t *testing.T) {
	effective := settings.Effective{
		SchemaVersion: 1,
		Values: settings.EffectiveValues{
			AllowTranscoding: false,
			Transcoding:      settings.TranscodingModeEnabled,
		},
		Sources: map[string]string{
			"allowTranscoding": "instance",
			"transcoding":      "profile",
		},
	}
	api := authenticatedSettingsAPI(&fakeSettingsService{effective: effective})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles/profile-id/settings/effective", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Settings settings.EffectiveValues `json:"settings"`
		Sources  map[string]string        `json:"sources"`
	}
	decodeResponse(t, response, &body)
	if body.Settings.AllowTranscoding || body.Settings.Transcoding != settings.TranscodingModeEnabled ||
		body.Sources["allowTranscoding"] != "instance" || body.Sources["transcoding"] != "profile" {
		t.Fatalf("effective transcoding policy omitted or altered: %+v", body)
	}
}

func TestInstanceSettingsJellyfinEnabledCommitsBeforeSupervisorNotification(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "false", true: "true"}[enabled], func(t *testing.T) {
			service := &fakeSettingsService{instance: settings.Layer{SchemaVersion: 1}}
			api := authenticatedSettingsAPI(service)
			api.jellyfinCompatibilityDesired = !enabled
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", bytes.NewBufferString(map[bool]string{
				false: `{"jellyfinEnabled":false}`,
				true:  `{"jellyfinEnabled":true}`,
			}[enabled]))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("jellyfinEnabled=%t status=%d body=%s", enabled, response.Code, response.Body.String())
			}
			if !service.instancePatch.JellyfinEnabled.Set || service.instancePatch.JellyfinEnabled.Value == nil ||
				*service.instancePatch.JellyfinEnabled.Value != enabled {
				t.Fatalf("jellyfinEnabled=%t patch=%+v", enabled, service.instancePatch.JellyfinEnabled)
			}
			if api.HasJellyfinCompatibility() != enabled {
				t.Fatalf("supervisor desired=%t want=%t", api.HasJellyfinCompatibility(), enabled)
			}
		})
	}
}

func TestInstanceSettingsJellyfinEnabledRejectsNullWithoutNotification(t *testing.T) {
	service := &fakeSettingsService{instance: settings.Layer{SchemaVersion: 1}}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", bytes.NewBufferString(`{"jellyfinEnabled":null}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || service.instancePatch.JellyfinEnabled.Set || api.HasJellyfinCompatibility() {
		t.Fatalf("null jellyfinEnabled status=%d patch=%+v desired=%t", response.Code, service.instancePatch.JellyfinEnabled, api.HasJellyfinCompatibility())
	}
}

func TestInstanceSettingsJellyfinEnabledPersistenceFailureDoesNotToggle(t *testing.T) {
	service := &fakeSettingsService{instanceErr: errors.New("database unavailable")}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", bytes.NewBufferString(`{"jellyfinEnabled":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError || api.HasJellyfinCompatibility() {
		t.Fatalf("failed persistence status=%d desired=%t", response.Code, api.HasJellyfinCompatibility())
	}
}

type ambiguousJellyfinCommitSettings struct {
	*fakeSettingsService
	mu      sync.Mutex
	enabled bool
	reads   int
}

func (service *ambiguousJellyfinCommitSettings) UpdateInstance(_ context.Context, _ auth.Principal, patch settings.Patch) (settings.Layer, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.instancePatch = patch
	if patch.JellyfinEnabled.Set && patch.JellyfinEnabled.Value != nil {
		service.enabled = *patch.JellyfinEnabled.Value
	}
	return settings.Layer{}, errors.New("commit acknowledgement lost")
}

func (service *ambiguousJellyfinCommitSettings) Instance(context.Context) (settings.Layer, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.reads++
	if service.reads == 1 {
		return settings.Layer{}, errors.New("canonical read temporarily unavailable")
	}
	enabled := service.enabled
	return settings.Layer{SchemaVersion: 1, Values: settings.Values{JellyfinEnabled: &enabled}}, nil
}

func (service *ambiguousJellyfinCommitSettings) setCanonical(enabled bool) {
	service.mu.Lock()
	service.enabled = enabled
	service.mu.Unlock()
}

func (service *ambiguousJellyfinCommitSettings) readCount() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.reads
}

func TestInstanceSettingsJellyfinAmbiguousCommitReconcilesCanonicalStateAfterReadRetry(t *testing.T) {
	service := &ambiguousJellyfinCommitSettings{fakeSettingsService: &fakeSettingsService{}}
	api := authenticatedSettingsAPI(service)
	api.jellyfinCompatibilityPollInterval = time.Millisecond
	api.jellyfinCompatibilityBackoff = func(uint32) time.Duration { return time.Millisecond }
	api.jellyfinCompatibilityReconciler = func(ctx context.Context) (bool, error) {
		layer, err := service.Instance(ctx)
		if err != nil {
			return false, err
		}
		return *layer.Values.JellyfinEnabled, nil
	}
	handler := newJellyfinLifecycleHandler(t)
	api.jellyfinCompatibilityBuilder = func(context.Context) (*jellyfin.Handler, bool, error) {
		return handler, true, nil
	}
	started := make(chan struct{}, 1)
	cleaned := make(chan struct{}, 1)
	api.jellyfinCompatibilityRunner = func(ctx context.Context, _ *jellyfin.Handler) {
		started <- struct{}{}
		<-ctx.Done()
		cleaned <- struct{}{}
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", bytes.NewBufferString(`{"jellyfinEnabled":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || api.HasJellyfinCompatibility() {
		t.Fatalf("ambiguous commit response=%d desired=%t", response.Code, api.HasJellyfinCompatibility())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		api.RunJellyfinCompatibility(ctx)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		cancel()
		t.Fatal("committed canonical enable did not survive a failed reconciliation read")
	}
	if service.readCount() < 2 || !api.HasJellyfinCompatibility() {
		t.Fatalf("canonical reconciliation reads=%d desired=%t", service.readCount(), api.HasJellyfinCompatibility())
	}

	service.setCanonical(false)
	select {
	case <-cleaned:
	case <-time.After(500 * time.Millisecond):
		cancel()
		t.Fatal("periodic canonical reconciliation did not disable the active generation")
	}
	if api.HasJellyfinCompatibility() {
		t.Fatal("runtime remained enabled after canonical database flip")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("reconciliation supervisor did not terminate")
	}
}

func TestJellyfinEnabledRemainsInstanceOnlyAndGlobalAdministratorOnly(t *testing.T) {
	profileService := &fakeSettingsService{profileErr: settings.ErrInvalidInput}
	profileAPI := authenticatedSettingsAPI(profileService)
	profileRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id/settings", bytes.NewBufferString(`{"jellyfinEnabled":true}`))
	profileRequest.Header.Set("Content-Type", "application/json")
	profileRequest.Header.Set("Authorization", "Bearer access-token")
	profileResponse := httptest.NewRecorder()
	profileAPI.Handler().ServeHTTP(profileResponse, profileRequest)
	if profileResponse.Code != http.StatusUnprocessableEntity || !profileService.profilePatch.JellyfinEnabled.Set || profileAPI.HasJellyfinCompatibility() {
		t.Fatalf("profile jellyfinEnabled status=%d patch=%+v desired=%t", profileResponse.Code, profileService.profilePatch.JellyfinEnabled, profileAPI.HasJellyfinCompatibility())
	}

	forbiddenService := &fakeSettingsService{instanceErr: settings.ErrForbidden}
	forbiddenAPI := authenticatedSettingsAPI(forbiddenService)
	forbiddenAPI.auth = &fakeAuthService{principal: auth.Principal{Role: "member"}}
	forbiddenRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", bytes.NewBufferString(`{"jellyfinEnabled":true}`))
	forbiddenRequest.Header.Set("Content-Type", "application/json")
	forbiddenRequest.Header.Set("Authorization", "Bearer access-token")
	forbiddenResponse := httptest.NewRecorder()
	forbiddenAPI.Handler().ServeHTTP(forbiddenResponse, forbiddenRequest)
	if forbiddenResponse.Code != http.StatusForbidden || forbiddenAPI.HasJellyfinCompatibility() {
		t.Fatalf("non-global jellyfinEnabled status=%d desired=%t", forbiddenResponse.Code, forbiddenAPI.HasJellyfinCompatibility())
	}
}

func TestInstanceSettingsResponseIncludesJellyfinEnabled(t *testing.T) {
	enabled := false
	api := authenticatedSettingsAPI(&fakeSettingsService{instance: settings.Layer{
		SchemaVersion: 1,
		Values:        settings.Values{JellyfinEnabled: &enabled},
	}})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"jellyfinEnabled":false`)) {
		t.Fatalf("instance Jellyfin setting response status=%d body=%s", response.Code, response.Body.String())
	}
}

func authenticatedSettingsAPI(service settingsService) *API {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{
		UserID: "user-id", DeviceID: "device-id", Role: "admin", SessionID: "session-id",
		AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}}
	api.settings = service
	return api
}
