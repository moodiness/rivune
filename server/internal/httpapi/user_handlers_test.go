package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/user"
)

type fakeUserService struct {
	users            []user.User
	listErr          error
	createInput      user.CreateInput
	created          user.User
	createErr        error
	updateInput      user.UpdateInput
	updated          user.User
	updateErr        error
	deleteErr        error
	profileAccess    []user.ProfileAccess
	profileAccessErr error
	granted          user.ProfileAccess
	grantCanManage   bool
	grantErr         error
	revokeErr        error
}

func (f *fakeUserService) List(context.Context, auth.Principal) ([]user.User, error) {
	return f.users, f.listErr
}

func (f *fakeUserService) Create(_ context.Context, _ auth.Principal, input user.CreateInput) (user.User, error) {
	f.createInput = input
	return f.created, f.createErr
}

func (f *fakeUserService) Update(_ context.Context, _ auth.Principal, _ string, input user.UpdateInput) (user.User, error) {
	f.updateInput = input
	return f.updated, f.updateErr
}

func (f *fakeUserService) Delete(context.Context, auth.Principal, string) error {
	return f.deleteErr
}

func (f *fakeUserService) ProfileAccess(context.Context, auth.Principal, string) ([]user.ProfileAccess, error) {
	return f.profileAccess, f.profileAccessErr
}

func (f *fakeUserService) GrantProfileAccess(_ context.Context, _ auth.Principal, _, _ string, canManage bool) (user.ProfileAccess, error) {
	f.grantCanManage = canManage
	return f.granted, f.grantErr
}

func (f *fakeUserService) RevokeProfileAccess(context.Context, auth.Principal, string, string) error {
	return f.revokeErr
}

func TestCreateUserDoesNotReturnPassword(t *testing.T) {
	service := &fakeUserService{created: user.User{ID: "user-id", Username: "alice", Role: "member"}}
	api := authenticatedUserAPI(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBufferString(`{"username":"alice","password":"very-secure-password","role":"member"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.createInput.Password != "very-secure-password" || bytes.Contains(response.Body.Bytes(), []byte("password")) {
		t.Fatalf("password handling leaked in response: %s", response.Body.String())
	}
}

func TestGrantProfileAccessPreservesFalse(t *testing.T) {
	service := &fakeUserService{granted: user.ProfileAccess{ProfileID: "profile-id", ProfileName: "Kids", HasAccess: true}}
	api := authenticatedUserAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/users/user-id/profiles/profile-id", bytes.NewBufferString(`{"canManage":false}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || service.grantCanManage {
		t.Fatalf("expected non-managing access, got status %d and canManage=%t", response.Code, service.grantCanManage)
	}
}

func TestDeleteUserProtectsFinalAdmin(t *testing.T) {
	service := &fakeUserService{deleteErr: user.ErrLastAdmin}
	api := authenticatedUserAPI(service)
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/users/admin-id", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", response.Code, response.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "last_admin" {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

func authenticatedUserAPI(service userService) *API {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{UserID: "admin-id", Role: "admin", SessionID: "session-id"}}
	api.users = service
	return api
}
