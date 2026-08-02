package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
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

func TestUpdateProfileSettingsDecodesEveryNewField(t *testing.T) {
	service := &fakeSettingsService{profile: settings.Layer{SchemaVersion: 1}}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id/settings", bytes.NewBufferString(
		`{"autoplayNextEpisode":false,"skipIntroEnabled":true,"skipRecapEnabled":false,"skipOutroEnabled":true,"cardDensity":"compact","animationsEnabled":false,"subtitleSizePercent":75,"subtitleTextColor":"#a1b2c3","subtitleBackgroundOpacityPercent":25,"notificationsEnabled":false,"notificationDurationSeconds":7,"notificationPollIntervalSeconds":45}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	patch := service.profilePatch
	if !patch.AutoplayNextEpisode.Set || patch.AutoplayNextEpisode.Value == nil || *patch.AutoplayNextEpisode.Value ||
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

func TestUpdateProfileSettingsDecodesNullForEveryNewField(t *testing.T) {
	service := &fakeSettingsService{profile: settings.Layer{SchemaVersion: 1}}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id/settings", bytes.NewBufferString(
		`{"interfaceLanguage":null,"autoplayNextEpisode":null,"skipIntroEnabled":null,"skipRecapEnabled":null,"skipOutroEnabled":null,"cardDensity":null,"animationsEnabled":null,"subtitleSizePercent":null,"subtitleTextColor":null,"subtitleBackgroundOpacityPercent":null,"notificationsEnabled":null,"notificationDurationSeconds":null,"notificationPollIntervalSeconds":null}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	patch := service.profilePatch
	if !patch.InterfaceLanguage.Set || patch.InterfaceLanguage.Value != nil {
		t.Fatalf("interface language null was not preserved: %+v", patch)
	}
	if !patch.AutoplayNextEpisode.Set || patch.AutoplayNextEpisode.Value != nil ||
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
			AutoplayNextEpisode: true, CardDensity: "comfortable", AnimationsEnabled: true,
			SubtitleSizePercent: 100, SubtitleTextColor: "#FFFFFF", SubtitleBackgroundOpacityPercent: 60,
			NotificationsEnabled: true, NotificationDurationSeconds: 5, NotificationPollIntervalSeconds: 5, ForcedSubtitleLanguage: "fr-CA",
		},
		Sources: map[string]string{"interfaceLanguage": "profile", "autoplayNextEpisode": "default", "subtitleTextColor": "profile", "forcedSubtitleLanguage": "instance"},
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
		SchemaVersion int                      `json:"schemaVersion"`
		Settings      settings.EffectiveValues `json:"settings"`
		Sources       map[string]string        `json:"sources"`
	}
	decodeResponse(t, response, &body)
	if body.SchemaVersion != 1 || body.Settings.InterfaceLanguage != "pt-BR" || body.Settings.SubtitleTextColor != "#FFFFFF" ||
		body.Settings.NotificationPollIntervalSeconds != 5 || body.Settings.ForcedSubtitleLanguage != "fr-CA" ||
		body.Sources["interfaceLanguage"] != "profile" || body.Sources["autoplayNextEpisode"] != "default" ||
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

func TestMaintenanceSettingsAreAdminOnly(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{UserID: "user-id", Role: "member", SessionID: "session-id"}}
	api.settings = &fakeSettingsService{}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/settings/maintenance", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", response.Code, response.Body.String())
	}
}

func authenticatedSettingsAPI(service settingsService) *API {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{UserID: "user-id", DeviceID: "device-id", Role: "admin", SessionID: "session-id"}}
	api.settings = service
	return api
}
