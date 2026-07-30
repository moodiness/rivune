package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/profile"
)

type fakeProfileService struct {
	profiles          []profile.Profile
	listErr           error
	createInput       profile.CreateInput
	created           profile.Profile
	createErr         error
	updatedID         string
	updateInput       profile.UpdateInput
	updated           profile.Profile
	updateErr         error
	deletedID         string
	deleteErr         error
	selectedID        string
	selectedPIN       *string
	selection         profile.Selection
	selectionErr      error
	clearSelectionErr error
	avatarPresetID    string
	avatarPresetValue profile.Profile
	avatarPresetErr   error
	avatarImageData   []byte
	avatarImageValue  profile.Profile
	avatarImageErr    error
	avatarValue       profile.AvatarImage
	avatarErr         error
}

func (f *fakeProfileService) List(context.Context, auth.Principal) ([]profile.Profile, error) {
	return f.profiles, f.listErr
}

func (f *fakeProfileService) Create(_ context.Context, _ auth.Principal, input profile.CreateInput) (profile.Profile, error) {
	f.createInput = input
	return f.created, f.createErr
}

func (f *fakeProfileService) Update(_ context.Context, _ auth.Principal, id string, input profile.UpdateInput) (profile.Profile, error) {
	f.updatedID = id
	f.updateInput = input
	return f.updated, f.updateErr
}

func (f *fakeProfileService) Delete(_ context.Context, _ auth.Principal, id string) error {
	f.deletedID = id
	return f.deleteErr
}

func (f *fakeProfileService) Select(_ context.Context, _ auth.Principal, id string, pin *string) (profile.Selection, error) {
	f.selectedID = id
	f.selectedPIN = pin
	return f.selection, f.selectionErr
}

func (f *fakeProfileService) ClearSelection(context.Context, auth.Principal) error {
	return f.clearSelectionErr
}

func (f *fakeProfileService) SetAvatarPreset(_ context.Context, _ auth.Principal, id, presetID string) (profile.Profile, error) {
	f.updatedID = id
	f.avatarPresetID = presetID
	return f.avatarPresetValue, f.avatarPresetErr
}

func (f *fakeProfileService) SetAvatarImage(_ context.Context, _ auth.Principal, id string, image []byte) (profile.Profile, error) {
	f.updatedID = id
	f.avatarImageData = image
	return f.avatarImageValue, f.avatarImageErr
}

func (f *fakeProfileService) AvatarImage(context.Context, auth.Principal, string) (profile.AvatarImage, error) {
	return f.avatarValue, f.avatarErr
}

func TestCreateProfileReturnsSafeRepresentation(t *testing.T) {
	pin := "1234"
	profiles := &fakeProfileService{created: profile.Profile{ID: "profile-id", Name: "Kids", IsChild: true, HasPIN: true, CanManage: true}}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles", bytes.NewBufferString(`{"name":"Kids","isChild":true,"pin":"1234"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if profiles.createInput.Name != "Kids" || profiles.createInput.PIN == nil || *profiles.createInput.PIN != pin {
		t.Fatalf("unexpected create input: %+v", profiles.createInput)
	}
	var body profileResponse
	decodeResponse(t, response, &body)
	if body.ID != "profile-id" || !body.HasPIN || !body.IsChild || !body.CanManage {
		t.Fatalf("unexpected profile response: %+v", body)
	}
}

func TestUpdateProfileDistinguishesClearedPIN(t *testing.T) {
	profiles := &fakeProfileService{updated: profile.Profile{ID: "profile-id", Name: "Kids", CanManage: true}}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id", bytes.NewBufferString(`{"pin":null}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if profiles.updatedID != "profile-id" || !profiles.updateInput.PINSet || profiles.updateInput.PIN != nil {
		t.Fatalf("expected explicit PIN removal, got %+v", profiles.updateInput)
	}
}

func TestSelectProfileMapsInvalidPIN(t *testing.T) {
	profiles := &fakeProfileService{selectionErr: profile.ErrInvalidPIN}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile-id/select", bytes.NewBufferString(`{"pin":"0000"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s", response.Code, response.Body.String())
	}
	if profiles.selectedID != "profile-id" || profiles.selectedPIN == nil || *profiles.selectedPIN != "0000" {
		t.Fatalf("unexpected profile selection input")
	}
}

func TestDeleteProtectsFinalProfile(t *testing.T) {
	profiles := &fakeProfileService{deleteErr: profile.ErrLastProfile}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/profiles/profile-id", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", response.Code, response.Body.String())
	}
}

func authenticatedProfileAPI(profiles profileService) *API {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{UserID: "user-id", Role: "admin", SessionID: "session-id"}}
	api.profiles = profiles
	return api
}
