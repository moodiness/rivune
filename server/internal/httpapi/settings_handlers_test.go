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
