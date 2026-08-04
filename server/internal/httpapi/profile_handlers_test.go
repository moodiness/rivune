package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/profile"
	"github.com/moodiness/rivune/server/internal/settings"
)

type fakeProfileService struct {
	profiles                   []profile.Profile
	listErr                    error
	createInput                profile.CreateInput
	created                    profile.Profile
	createErr                  error
	updatedID                  string
	updateInput                profile.UpdateInput
	updated                    profile.Profile
	updateErr                  error
	deletedID                  string
	deleteErr                  error
	selectedID                 string
	selectedPIN                *string
	selectionRequireManagement bool
	selection                  profile.Selection
	selectionErr               error
	clearSelectionErr          error
	avatarPresetID             string
	avatarPresetValue          profile.Profile
	avatarPresetErr            error
	avatarImageData            []byte
	avatarImageValue           profile.Profile
	avatarImageErr             error
	avatarValue                profile.AvatarImage
	avatarErr                  error
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

func (f *fakeProfileService) Select(_ context.Context, _ auth.Principal, id string, pin *string, requireManagement bool) (profile.Selection, error) {
	f.selectedID = id
	f.selectedPIN = pin
	f.selectionRequireManagement = requireManagement
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
	profiles := &fakeProfileService{created: profile.Profile{
		ID: "profile-id", CategoryID: "11111111-1111-4111-8111-111111111111", CategoryName: "Kids devices",
		Name: "Kids", Description: new("Shared family profile"), IsChild: true, HasPIN: true, CanManage: true,
		Enabled: true, AccessTimezone: "UTC", Accessible: true,
	}}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles", bytes.NewBufferString(`{"name":"Kids","description":"Shared family profile","categoryId":"11111111-1111-4111-8111-111111111111","isChild":true,"pin":"1234"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if profiles.createInput.Name != "Kids" || profiles.createInput.Description == nil || *profiles.createInput.Description != "Shared family profile" ||
		profiles.createInput.CategoryID != "11111111-1111-4111-8111-111111111111" ||
		profiles.createInput.PIN == nil || *profiles.createInput.PIN != pin {
		t.Fatalf("unexpected create input: %+v", profiles.createInput)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"color":null`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"icon":null`)) {
		t.Fatalf("nullable category fields were omitted: %s", response.Body.String())
	}
	var body profileResponse
	decodeResponse(t, response, &body)
	if body.ID != "profile-id" || body.Description == nil || *body.Description != "Shared family profile" ||
		body.CategoryID != "11111111-1111-4111-8111-111111111111" ||
		body.Category.ID != body.CategoryID || body.Category.Name != "Kids devices" ||
		!body.HasPIN || !body.IsChild || !body.CanManage ||
		!body.Enabled || !body.Accessible || body.AccessTimezone != "UTC" {
		t.Fatalf("unexpected profile response: %+v", body)
	}
}

func TestCreateProfileAcceptsTemporaryAccess(t *testing.T) {
	profiles := &fakeProfileService{created: profile.Profile{ID: "profile-id", Name: "Guest", Enabled: true, AccessTimezone: "Europe/Paris"}}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles", bytes.NewBufferString(`{
		"name":"Guest","enabled":true,"availableFrom":"2026-08-01","availableUntil":"2026-08-31",
		"accessStartTime":"20:00","accessEndTime":"08:00"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	input := profiles.createInput
	if input.AvailableFrom == nil || *input.AvailableFrom != "2026-08-01" ||
		input.AvailableUntil == nil || *input.AvailableUntil != "2026-08-31" ||
		input.AccessStartTime == nil || *input.AccessStartTime != "20:00" ||
		input.AccessEndTime == nil || *input.AccessEndTime != "08:00" {
		t.Fatalf("unexpected temporary profile input: %+v", input)
	}
}

func TestUpdateProfileDistinguishesCategoryIDPresence(t *testing.T) {
	const categoryID = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name           string
		body           string
		wantStatus     int
		wantCategoryID string
	}{
		{name: "omitted", body: `{}`, wantStatus: http.StatusOK},
		{name: "value", body: `{"categoryId":"` + categoryID + `"}`, wantStatus: http.StatusOK, wantCategoryID: categoryID},
		{name: "null", body: `{"categoryId":null}`, wantStatus: http.StatusUnprocessableEntity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profiles := &fakeProfileService{updated: profile.Profile{ID: "profile-id", Name: "Kids", CanManage: true}}
			api := authenticatedProfileAPI(profiles)
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", test.wantStatus, response.Code, response.Body.String())
			}
			if test.name == "null" {
				if profiles.updatedID != "" {
					t.Fatal("explicit null categoryId reached the profile service")
				}
				var body errorEnvelope
				decodeResponse(t, response, &body)
				if body.Error.Code != "invalid_profile" || body.Error.Message != "categoryId cannot be null" {
					t.Fatalf("unexpected error response: %+v", body.Error)
				}
				return
			}
			if profiles.updatedID != "profile-id" {
				t.Fatalf("profile update did not reach the service: %+v", profiles.updateInput)
			}
			if test.wantCategoryID == "" {
				if profiles.updateInput.CategoryID != nil {
					t.Fatalf("omitted categoryId became %+v", profiles.updateInput.CategoryID)
				}
				return
			}
			if profiles.updateInput.CategoryID == nil || *profiles.updateInput.CategoryID != test.wantCategoryID {
				t.Fatalf("unexpected categoryId service input: %+v", profiles.updateInput.CategoryID)
			}
		})
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

func TestUpdateProfileDistinguishesDescriptionClear(t *testing.T) {
	profiles := &fakeProfileService{updated: profile.Profile{ID: "profile-id", Name: "Kids", CanManage: true}}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id", bytes.NewBufferString(`{"description":null}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if profiles.updatedID != "profile-id" || !profiles.updateInput.DescriptionSet || profiles.updateInput.Description != nil {
		t.Fatalf("expected explicit profile description removal, got %+v", profiles.updateInput)
	}
}

func TestUpdateProfileDistinguishesClearedRestrictions(t *testing.T) {
	profiles := &fakeProfileService{updated: profile.Profile{ID: "profile-id", Name: "Kids", Enabled: true, AccessTimezone: "UTC"}}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id", bytes.NewBufferString(`{
		"availableFrom":null,"availableUntil":null,"accessStartTime":null,"accessEndTime":null
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	input := profiles.updateInput
	if !input.AvailableFromSet || input.AvailableFrom != nil || !input.AvailableUntilSet || input.AvailableUntil != nil ||
		!input.AccessStartTimeSet || input.AccessStartTime != nil || !input.AccessEndTimeSet || input.AccessEndTime != nil {
		t.Fatalf("expected explicit restriction clearing, got %+v", input)
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

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", response.Code, response.Body.String())
	}
	if profiles.selectedID != "profile-id" || profiles.selectedPIN == nil || *profiles.selectedPIN != "0000" {
		t.Fatalf("unexpected profile selection input")
	}
}

func TestSelectProfileMapsPINRateLimit(t *testing.T) {
	profiles := &fakeProfileService{selectionErr: profile.ErrPINRateLimited}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile-id/select", bytes.NewBufferString(`{"pin":"0000"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Retry-After") != "300" {
		t.Fatalf("expected Retry-After 300, got %q", response.Header().Get("Retry-After"))
	}
}

func TestSelectProfileMapsUnavailable(t *testing.T) {
	profiles := &fakeProfileService{selectionErr: profile.ErrUnavailable}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile-id/select", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", response.Code, response.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "profile_unavailable" {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

func TestMaintenanceRejectsViewerProfileSelection(t *testing.T) {
	message := "Upgrading the library"
	profiles := &fakeProfileService{selectionErr: profile.ErrManagementRequired}
	api := authenticatedProfileAPI(profiles)
	api.settings = &fakeSettingsService{maintenance: settings.Maintenance{Enabled: true, Message: &message}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile-id/select", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", response.Code, response.Body.String())
	}
	if !profiles.selectionRequireManagement {
		t.Fatal("maintenance selection did not require profile management permission")
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "maintenance_mode" || body.Error.PublicMessage == nil || *body.Error.PublicMessage != message {
		t.Fatalf("unexpected maintenance response %#v", body.Error)
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

func TestProfileAccessChangeProtectsFinalUnrestrictedProfile(t *testing.T) {
	profiles := &fakeProfileService{updateErr: profile.ErrLastUnrestrictedProfile}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/profiles/profile-id", bytes.NewBufferString(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", response.Code, response.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "last_unrestricted_profile" {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

func TestDeleteProtectsFinalUnrestrictedProfile(t *testing.T) {
	profiles := &fakeProfileService{deleteErr: profile.ErrLastUnrestrictedProfile}
	api := authenticatedProfileAPI(profiles)
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/profiles/profile-id", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", response.Code, response.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "last_unrestricted_profile" {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

func authenticatedProfileAPI(profiles profileService) *API {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{UserID: "user-id", Role: "admin", SessionID: "session-id"}}
	api.profiles = profiles
	return api
}
