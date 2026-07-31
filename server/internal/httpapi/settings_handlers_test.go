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
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id/settings", bytes.NewBufferString(`{"preferDirectPlay":false,"hideUnreleased":true,"theme":null}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
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
}

func TestUpdateProfileSettingsDecodesEveryNewField(t *testing.T) {
	service := &fakeSettingsService{profile: settings.Layer{SchemaVersion: 1}}
	api := authenticatedSettingsAPI(service)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id/settings", bytes.NewBufferString(
		`{"autoplayNextEpisode":false,"cardDensity":"compact","animationsEnabled":false,"subtitleSizePercent":75,"subtitleTextColor":"#a1b2c3","subtitleBackgroundOpacityPercent":25,"notificationsEnabled":false,"notificationDurationSeconds":7,"notificationPollIntervalSeconds":45}`,
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
		`{"autoplayNextEpisode":null,"cardDensity":null,"animationsEnabled":null,"subtitleSizePercent":null,"subtitleTextColor":null,"subtitleBackgroundOpacityPercent":null,"notificationsEnabled":null,"notificationDurationSeconds":null,"notificationPollIntervalSeconds":null}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	patch := service.profilePatch
	if !patch.AutoplayNextEpisode.Set || patch.AutoplayNextEpisode.Value != nil ||
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

func TestEffectiveSettingsResponseIncludesNewFieldsAndSources(t *testing.T) {
	effective := settings.Effective{
		SchemaVersion: 1,
		Values: settings.EffectiveValues{
			AutoplayNextEpisode: true, CardDensity: "comfortable", AnimationsEnabled: true,
			SubtitleSizePercent: 100, SubtitleTextColor: "#FFFFFF", SubtitleBackgroundOpacityPercent: 60,
			NotificationsEnabled: true, NotificationDurationSeconds: 5, NotificationPollIntervalSeconds: 5,
		},
		Sources: map[string]string{"autoplayNextEpisode": "default", "subtitleTextColor": "profile"},
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
	if body.SchemaVersion != 1 || body.Settings.SubtitleTextColor != "#FFFFFF" ||
		body.Settings.NotificationPollIntervalSeconds != 5 || body.Sources["autoplayNextEpisode"] != "default" ||
		body.Sources["subtitleTextColor"] != "profile" {
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

func authenticatedSettingsAPI(service settingsService) *API {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{UserID: "user-id", DeviceID: "device-id", Role: "admin", SessionID: "session-id"}}
	api.settings = service
	return api
}
