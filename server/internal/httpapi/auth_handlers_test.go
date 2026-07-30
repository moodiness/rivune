package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

type fakeAuthService struct {
	loginInput              auth.LoginInput
	loginTokens             auth.TokenPair
	loginErr                error
	refreshToken            string
	refreshTokens           auth.TokenPair
	refreshErr              error
	authenticateToken       string
	principal               auth.Principal
	authenticateErr         error
	account                 auth.Account
	accountErr              error
	logoutPrincipal         auth.Principal
	logoutErr               error
	sessions                []auth.Session
	sessionsErr             error
	revokedSessionID        string
	revokeErr               error
	profileSessions         []auth.Session
	profileSessionsErr      error
	profileSessionsID       string
	revokedProfileID        string
	revokedProfileSessionID string
	revokeProfileSessionErr error
	deviceAuthorization     auth.DeviceAuthorization
	deviceAuthorizationErr  error
	approvedUserCode        string
	approvalErr             error
	exchangedDeviceCode     string
	exchangeTokens          auth.TokenPair
	exchangeErr             error
}

func (f *fakeAuthService) Login(_ context.Context, input auth.LoginInput) (auth.TokenPair, error) {
	f.loginInput = input
	return f.loginTokens, f.loginErr
}

func (f *fakeAuthService) Refresh(_ context.Context, token string) (auth.TokenPair, error) {
	f.refreshToken = token
	return f.refreshTokens, f.refreshErr
}

func (f *fakeAuthService) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	f.authenticateToken = token
	return f.principal, f.authenticateErr
}

func (f *fakeAuthService) Account(_ context.Context, principal auth.Principal) (auth.Account, error) {
	return f.account, f.accountErr
}

func (f *fakeAuthService) Logout(_ context.Context, principal auth.Principal) error {
	f.logoutPrincipal = principal
	return f.logoutErr
}

func (f *fakeAuthService) Sessions(context.Context, auth.Principal) ([]auth.Session, error) {
	return f.sessions, f.sessionsErr
}

func (f *fakeAuthService) RevokeSession(_ context.Context, _ auth.Principal, sessionID string) error {
	f.revokedSessionID = sessionID
	return f.revokeErr
}

func (f *fakeAuthService) ProfileSessions(_ context.Context, _ auth.Principal, profileID string) ([]auth.Session, error) {
	f.profileSessionsID = profileID
	return f.profileSessions, f.profileSessionsErr
}

func (f *fakeAuthService) RevokeProfileSession(_ context.Context, _ auth.Principal, profileID, sessionID string) error {
	f.revokedProfileID = profileID
	f.revokedProfileSessionID = sessionID
	return f.revokeProfileSessionErr
}

func (f *fakeAuthService) BeginDeviceAuthorization(context.Context, string, string) (auth.DeviceAuthorization, error) {
	return f.deviceAuthorization, f.deviceAuthorizationErr
}

func (f *fakeAuthService) ApproveDeviceAuthorization(_ context.Context, _ auth.Principal, userCode string) error {
	f.approvedUserCode = userCode
	return f.approvalErr
}

func (f *fakeAuthService) ExchangeDeviceAuthorization(_ context.Context, deviceCode string) (auth.TokenPair, error) {
	f.exchangedDeviceCode = deviceCode
	return f.exchangeTokens, f.exchangeErr
}

func TestLoginReturnsOpaqueSessionTokens(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	service := &fakeAuthService{loginTokens: auth.TokenPair{
		AccessToken:      "rivune_at_access",
		AccessExpiresAt:  now.Add(15 * time.Minute),
		RefreshToken:     "rivune_rt_refresh",
		RefreshExpiresAt: now.Add(30 * 24 * time.Hour),
		SessionID:        "session-id",
		DeviceID:         "device-id",
	}}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret","device":{"name":"Living Room","platform":"tvos"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.loginInput.Username != "admin" || service.loginInput.DeviceName != "Living Room" || service.loginInput.Platform != "tvos" {
		t.Fatalf("unexpected login input: %+v", service.loginInput)
	}
	var body tokenResponse
	decodeResponse(t, response, &body)
	if body.TokenType != "Bearer" || body.AccessToken != "rivune_at_access" || body.RefreshToken != "rivune_rt_refresh" || body.SessionID != "session-id" {
		t.Fatalf("unexpected token response: %+v", body)
	}
}

func TestLoginUsesGenericCredentialError(t *testing.T) {
	service := &fakeAuthService{loginErr: auth.ErrInvalidCredentials}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"missing","password":"wrong","device":{"name":"Phone","platform":"ios"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("expected bearer 401, got %d and header %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "invalid_credentials" {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

func TestMeReturnsAuthenticatedAccount(t *testing.T) {
	principal := auth.Principal{SessionID: "session-id", UserID: "user-id", DeviceID: "device-id", Username: "admin", Role: "admin"}
	service := &fakeAuthService{
		principal: principal,
		account:   auth.Account{Principal: principal, Profiles: []auth.Profile{{ID: "profile-id", Name: "Admin", HasPIN: true, CanManage: true}}},
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.authenticateToken != "access-token" {
		t.Fatalf("expected bearer token to reach auth service")
	}
	var body struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		Profiles []struct {
			Name      string `json:"name"`
			CanManage bool   `json:"canManage"`
			HasPIN    bool   `json:"hasPin"`
		} `json:"profiles"`
	}
	decodeResponse(t, response, &body)
	if body.User.Username != "admin" || len(body.Profiles) != 1 || body.Profiles[0].Name != "Admin" || !body.Profiles[0].HasPIN || !body.Profiles[0].CanManage {
		t.Fatalf("unexpected account response: %+v", body)
	}
}

func TestProtectedRouteRejectsInvalidAccessToken(t *testing.T) {
	service := &fakeAuthService{authenticateErr: auth.ErrInvalidToken}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sessions", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("expected bearer 401, got %d and header %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}

func TestRevokeSessionUsesOwnedPathSession(t *testing.T) {
	service := &fakeAuthService{principal: auth.Principal{UserID: "user-id", SessionID: "current-id"}}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions/target-id", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d: %s", response.Code, response.Body.String())
	}
	if service.revokedSessionID != "target-id" {
		t.Fatalf("expected target-id to be revoked, got %q", service.revokedSessionID)
	}
}

func TestListProfileSessionsIncludesDeviceAndUser(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	service := &fakeAuthService{
		principal: auth.Principal{UserID: "manager-id", SessionID: "current-id"},
		profileSessions: []auth.Session{{
			ID: "session-id", UserID: "user-id", Username: "alex", DeviceID: "device-id",
			DeviceName: "Living Room", Platform: "tvos", IPAddress: "203.0.113.42",
			ProfileGrantExpiresAt: &expiresAt,
		}},
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles/profile-id/sessions", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Sessions []struct {
			Username   string  `json:"username"`
			DeviceName string  `json:"deviceName"`
			IPAddress  *string `json:"ipAddress"`
		} `json:"sessions"`
	}
	decodeResponse(t, response, &body)
	if service.profileSessionsID != "profile-id" || len(body.Sessions) != 1 ||
		body.Sessions[0].Username != "alex" || body.Sessions[0].DeviceName != "Living Room" ||
		body.Sessions[0].IPAddress == nil || *body.Sessions[0].IPAddress != "203.0.113.42" {
		t.Fatalf("unexpected profile sessions response: %+v", body)
	}
}

func TestRevokeProfileSessionUsesBothPathIdentifiers(t *testing.T) {
	service := &fakeAuthService{principal: auth.Principal{UserID: "manager-id", SessionID: "current-id"}}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/profiles/profile-id/sessions/session-id", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d: %s", response.Code, response.Body.String())
	}
	if service.revokedProfileID != "profile-id" || service.revokedProfileSessionID != "session-id" {
		t.Fatalf("unexpected revoked profile session: profile=%q session=%q", service.revokedProfileID, service.revokedProfileSessionID)
	}
}

func TestApproveDeviceAuthorizationRequiresManagerProfile(t *testing.T) {
	service := &fakeAuthService{
		principal:   auth.Principal{UserID: "user-id", SessionID: "session-id"},
		approvalErr: auth.ErrForbidden,
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-code/approve", bytes.NewBufferString(`{"userCode":"ABCD-EFGH"}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", response.Code, response.Body.String())
	}
}

func TestRefreshRejectsInvalidToken(t *testing.T) {
	service := &fakeAuthService{refreshErr: errors.Join(errors.New("wrapped"), auth.ErrInvalidToken)}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewBufferString(`{"refreshToken":"invalid"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s", response.Code, response.Body.String())
	}
}
