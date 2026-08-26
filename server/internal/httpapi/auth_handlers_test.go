package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/category"
)

type fakeAuthService struct {
	loginInput                        auth.LoginInput
	loginCalls                        int
	loginTokens                       auth.TokenPair
	loginErr                          error
	jellyfinLoginInput                auth.JellyfinProfileLoginInput
	jellyfinLoginResult               auth.JellyfinProfileLoginResult
	jellyfinLoginErr                  error
	jellyfinLoginCalls                int
	refreshToken                      string
	refreshTokens                     auth.TokenPair
	refreshErr                        error
	authenticateToken                 string
	principal                         auth.Principal
	authenticateErr                   error
	account                           auth.Account
	accountErr                        error
	logoutPrincipal                   auth.Principal
	logoutErr                         error
	sessions                          []auth.Session
	sessionsErr                       error
	revokedSessionID                  string
	revokeErr                         error
	profileSessions                   []auth.Session
	profileSessionsErr                error
	profileSessionsID                 string
	revokedProfileID                  string
	revokedProfileSessionID           string
	revokeProfileSessionErr           error
	notifications                     []auth.SessionNotification
	notificationsAfterID              int64
	notificationsCalls                int
	notificationsErr                  error
	acknowledgedNotificationID        int64
	acknowledgeCalls                  int
	acknowledgeErr                    error
	broadcastID                       string
	broadcastMessage                  string
	broadcastPrincipal                auth.Principal
	broadcastResult                   auth.NotificationBroadcast
	broadcastCalls                    int
	broadcastErr                      error
	notifiedProfileID                 string
	notifiedSessionID                 string
	notifiedMessage                   string
	sentNotification                  auth.SessionNotification
	sendNotificationCalls             int
	sendNotificationErr               error
	deviceAuthorizationInstallationID string
	deviceAuthorizationDeviceName     string
	deviceAuthorizationPlatform       string
	deviceAuthorization               auth.DeviceAuthorization
	deviceAuthorizationErr            error
	approvalInput                     auth.DeviceAuthorizationApproval
	approvalErr                       error
	exchangedDeviceCode               string
	exchangeTokens                    auth.TokenPair
	exchangeErr                       error
}

func (f *fakeAuthService) Login(_ context.Context, input auth.LoginInput) (auth.TokenPair, error) {
	f.loginCalls++
	f.loginInput = input
	return f.loginTokens, f.loginErr
}
func (f *fakeAuthService) LoginWeb(_ context.Context, input auth.LoginInput) (auth.TokenPair, error) {
	f.loginCalls++
	f.loginInput = input
	return f.loginTokens, f.loginErr
}


func (f *fakeAuthService) LoginJellyfinProfile(_ context.Context, input auth.JellyfinProfileLoginInput) (auth.JellyfinProfileLoginResult, error) {
	f.jellyfinLoginCalls++
	f.jellyfinLoginInput = input
	return f.jellyfinLoginResult, f.jellyfinLoginErr
}

func (f *fakeAuthService) Refresh(_ context.Context, token string) (auth.TokenPair, error) {
	f.refreshToken = token
	return f.refreshTokens, f.refreshErr
}
func (f *fakeAuthService) RefreshWeb(_ context.Context, token string) (auth.TokenPair, error) {
	f.refreshToken = token
	return f.refreshTokens, f.refreshErr
}

func (f *fakeAuthService) ExchangeWebDeviceAuthorization(_ context.Context, code string) (auth.TokenPair, error) {
	f.exchangedDeviceCode = code
	return f.exchangeTokens, f.exchangeErr
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
func (f *fakeAuthService) LogoutWeb(_ context.Context, token string) error {
	f.refreshToken = token
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

func (f *fakeAuthService) SessionNotifications(_ context.Context, _ auth.Principal, afterID int64) ([]auth.SessionNotification, error) {
	f.notificationsCalls++
	f.notificationsAfterID = afterID
	return f.notifications, f.notificationsErr
}

func (f *fakeAuthService) AcknowledgeSessionNotification(_ context.Context, _ auth.Principal, notificationID int64) error {
	f.acknowledgeCalls++
	f.acknowledgedNotificationID = notificationID
	return f.acknowledgeErr
}

func (f *fakeAuthService) BroadcastSessionNotification(_ context.Context, principal auth.Principal, broadcastID, message string) (auth.NotificationBroadcast, error) {
	f.broadcastCalls++
	f.broadcastPrincipal = principal
	f.broadcastID = broadcastID
	f.broadcastMessage = message
	return f.broadcastResult, f.broadcastErr
}

func (f *fakeAuthService) SendProfileSessionNotification(_ context.Context, _ auth.Principal, profileID, sessionID, message string) (auth.SessionNotification, error) {
	f.sendNotificationCalls++
	f.notifiedProfileID = profileID
	f.notifiedSessionID = sessionID
	f.notifiedMessage = message
	return f.sentNotification, f.sendNotificationErr
}

func (f *fakeAuthService) BeginDeviceAuthorization(_ context.Context, installationID, deviceName, platform string) (auth.DeviceAuthorization, error) {
	f.deviceAuthorizationInstallationID = installationID
	f.deviceAuthorizationDeviceName = deviceName
	f.deviceAuthorizationPlatform = platform
	return f.deviceAuthorization, f.deviceAuthorizationErr
}

func (f *fakeAuthService) ApproveDeviceAuthorization(_ context.Context, _ auth.Principal, input auth.DeviceAuthorizationApproval) error {
	f.approvalInput = input
	return f.approvalErr
}

func (f *fakeAuthService) ExchangeDeviceAuthorization(_ context.Context, deviceCode string) (auth.TokenPair, error) {
	f.exchangedDeviceCode = deviceCode
	return f.exchangeTokens, f.exchangeErr
}

func TestAuthenticationRejectsMissingOrStaleWebProfileCapability(t *testing.T) {
	activeProfileID := "11111111-1111-4111-8111-111111111111"
	profileContext, profileContextHash, err := auth.NewProfileContext()
	if err != nil {
		t.Fatalf("issue profile context: %v", err)
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{principal: auth.Principal{
		Role: "admin", Platform: "web", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: &activeProfileID, ProfileContextHash: profileContextHash,
	}}
	called := false
	handler := api.requireAuthentication(func(w http.ResponseWriter, _ *http.Request, _ auth.Principal) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	stale := httptest.NewRequest(http.MethodGet, "/api/v1/collections", nil)
	stale.Header.Set("Authorization", "Bearer access-token")
	stale.Header.Set(profileContextHeader, "rivune_pc_stale")
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict || called {
		t.Fatalf("stale profile context status=%d called=%v", staleResponse.Code, called)
	}
	var body errorEnvelope
	decodeResponse(t, staleResponse, &body)
	if body.Error.Code != "profile_selection_required" {
		t.Fatalf("stale profile context error = %q", body.Error.Code)
	}
	missing := httptest.NewRequest(http.MethodGet, "/api/v1/collections", nil)
	missing.Header.Set("Authorization", "Bearer access-token")
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusConflict || called {
		t.Fatalf("missing web profile context status=%d called=%v", missingResponse.Code, called)
	}

	current := httptest.NewRequest(http.MethodGet, "/api/v1/collections", nil)
	current.Header.Set("Authorization", "Bearer access-token")
	current.Header.Set(profileContextHeader, profileContext)
	currentResponse := httptest.NewRecorder()
	handler.ServeHTTP(currentResponse, current)
	if currentResponse.Code != http.StatusNoContent || !called {
		t.Fatalf("current profile context status=%d called=%v", currentResponse.Code, called)
	}

	called = false
	clearWithoutContext := httptest.NewRequest(http.MethodDelete, "/api/v1/profiles/selection", nil)
	clearWithoutContext.Header.Set("Authorization", "Bearer access-token")
	clearResponse := httptest.NewRecorder()
	handler.ServeHTTP(clearResponse, clearWithoutContext)
	if clearResponse.Code != http.StatusNoContent || !called {
		t.Fatalf("profile clear without current context status=%d called=%v", clearResponse.Code, called)
	}

	selectWithoutContext := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/11111111-1111-4111-8111-111111111111/select", nil)
	selectWithoutContext.Header.Set("Authorization", "Bearer access-token")
	selectResponse := httptest.NewRecorder()
	handler.ServeHTTP(selectResponse, selectWithoutContext)
	if selectResponse.Code != http.StatusNoContent || !called {
		t.Fatalf("profile selection without prior context status=%d called=%v", selectResponse.Code, called)
	}
}

func TestLoginReturnsOpaqueSessionTokens(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	service := &fakeAuthService{loginTokens: auth.TokenPair{
		AccessToken:        "rivune_at_access",
		AccessExpiresAt:    now.Add(15 * time.Minute),
		RefreshToken:       "rivune_rt_refresh",
		RefreshExpiresAt:   now.Add(30 * 24 * time.Hour),
		SessionID:          "session-id",
		DeviceID:           "device-id",
		AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
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
	if body.TokenType != "Bearer" || body.AccessToken != "rivune_at_access" || body.RefreshToken != "rivune_rt_refresh" ||
		body.SessionID != "session-id" || body.AuthorizationScope != auth.AuthorizationScopeGlobalAdministrator || body.Category != nil {
		t.Fatalf("unexpected token response: %+v", body)
	}
}

func TestRefreshReturnsPairedDeviceTokenBody(t *testing.T) {
	pairedExpiryInLocalTime := time.Date(10000, time.January, 1, 0, 59, 59, 0, time.FixedZone("CET", 60*60))
	now := time.Now().UTC().Truncate(time.Second)
	categoryName := "Uncategorized"
	service := &fakeAuthService{refreshTokens: auth.TokenPair{
		AccessToken:        "rivune_at_rotated",
		AccessExpiresAt:    now.Add(15 * time.Minute),
		RefreshToken:       "rivune_rt_rotated",
		RefreshExpiresAt:   pairedExpiryInLocalTime,
		SessionID:          "629903e5-a234-420a-9037-9c40c3d8308b",
		DeviceID:           "fc15aeab-88f9-4928-a7d2-d9199ba9c70d",
		AuthorizationScope: auth.AuthorizationScopeCategory,
		Category: &category.CategoryRef{
			ID:   "4cf5ee5b-25f4-41c1-8b60-4c1d6a073c4d",
			Name: categoryName,
		},
	}}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewBufferString(`{"refreshToken":"rivune_rt_existing"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body tokenResponse
	decodeResponse(t, response, &body)
	if body.AccessToken != "rivune_at_rotated" || body.RefreshToken != "rivune_rt_rotated" || body.AuthorizationScope != auth.AuthorizationScopeCategory || body.Category == nil {
		t.Fatalf("unexpected paired refresh response: %+v", body)
	}
	if !body.RefreshTokenExpiresAt.Equal(time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)) {
		t.Fatalf("paired refresh expiry = %s, want canonical UTC maximum", body.RefreshTokenExpiresAt)
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
func TestLoginDeviceQuotaUsesStableConflict(t *testing.T) {
	api := testAPI(&fakeInstanceService{})
	api.auth = &fakeAuthService{loginErr: auth.ErrDeviceQuotaReached}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"member","password":"correct","device":{"name":"Phone","platform":"ios"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", response.Code, response.Body.String())
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "device_quota_reached" {
		t.Fatalf("error code = %q, want device_quota_reached", body.Error.Code)
	}
}

func TestLoginFailuresCannotLockOutValidCredentialsOrFillSubjectTable(t *testing.T) {
	service := &fakeAuthService{loginErr: auth.ErrInvalidCredentials}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	api.usernameAdmission = newUsernameAdmission(credentialUsernameAttempts, 2, time.Minute)

	serveLogin := func(username, password, source string) *httptest.ResponseRecorder {
		t.Helper()
		body := `{"username":"` + username + `","password":"` + password + `","device":{"name":"Phone","platform":"ios"}}`
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = source + ":4000"
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		return response
	}

	for attempt := range credentialUsernameAttempts + 1 {
		response := serveLogin("target", "wrong", "198.51.100."+strconv.Itoa(attempt+1))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("invalid target attempt %d = %d, want opaque 401: %s", attempt+1, response.Code, response.Body.String())
		}
		var body errorEnvelope
		decodeResponse(t, response, &body)
		if body.Error.Code != "invalid_credentials" {
			t.Fatalf("invalid target attempt %d code = %q, want invalid_credentials", attempt+1, body.Error.Code)
		}
	}
	targetDigest := sha256.Sum256([]byte("target"))
	if state := api.usernameAdmission.subjects[targetDigest]; state.attempts != credentialUsernameAttempts {
		t.Fatalf("target failures = %d, want capped %d", state.attempts, credentialUsernameAttempts)
	}

	service.loginErr = nil
	service.loginTokens = auth.TokenPair{AccessToken: "rivune_at_valid", RefreshToken: "rivune_rt_valid"}
	valid := serveLogin("target", "correct", "203.0.113.20")
	if valid.Code != http.StatusOK {
		t.Fatalf("valid target after rotated-source failures = %d, want 200: %s", valid.Code, valid.Body.String())
	}
	if _, exists := api.usernameAdmission.subjects[targetDigest]; exists {
		t.Fatal("successful target login did not reset its failure state")
	}

	service.loginErr = auth.ErrInvalidCredentials
	for index, username := range []string{"first", "second", "third"} {
		response := serveLogin(username, "wrong", "203.0.113."+strconv.Itoa(index+21))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("invalid %s login = %d, want opaque 401: %s", username, response.Code, response.Body.String())
		}
	}
	thirdDigest := sha256.Sum256([]byte("third"))
	if _, exists := api.usernameAdmission.subjects[thirdDigest]; !exists {
		t.Fatal("full subject table rejected a new failed username instead of evicting")
	}
	if got := len(api.usernameAdmission.subjects); got != 2 {
		t.Fatalf("tracked failure subjects = %d, want bounded cardinality 2", got)
	}

	service.loginErr = nil
	newValid := serveLogin("new-valid", "correct", "203.0.113.24")
	if newValid.Code != http.StatusOK {
		t.Fatalf("new valid login with full subject table = %d, want 200: %s", newValid.Code, newValid.Body.String())
	}
}

func TestMeReturnsAuthenticatedAccount(t *testing.T) {
	categoryID := "category-id"
	reference := category.CategoryRef{ID: categoryID, Name: "Kids"}
	principal := auth.Principal{
		SessionID: "session-id", UserID: "user-id", DeviceID: "device-id", Username: "admin", Role: "admin",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID, Category: &reference,
	}
	service := &fakeAuthService{
		principal: principal,
		account: auth.Account{Principal: principal, Profiles: []auth.Profile{{
			ID: "profile-id", Name: "Admin", HasPIN: true, CanManage: true, Enabled: true,
			AvailableUntil: new("2026-08-31"), AccessTimezone: "UTC", Accessible: true, Category: reference,
		}}},
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
		Session struct {
			AuthorizationScope auth.AuthorizationScope `json:"authorizationScope"`
			Category           category.CategoryRef    `json:"category"`
		} `json:"session"`
		Profiles []struct {
			CategoryID     string               `json:"categoryId"`
			Name           string               `json:"name"`
			CanManage      bool                 `json:"canManage"`
			HasPIN         bool                 `json:"hasPin"`
			Enabled        bool                 `json:"enabled"`
			AvailableUntil *string              `json:"availableUntil"`
			AccessTimezone string               `json:"accessTimezone"`
			Accessible     bool                 `json:"accessible"`
			Category       category.CategoryRef `json:"category"`
		} `json:"profiles"`
	}
	decodeResponse(t, response, &body)
	if body.User.Username != "admin" || body.Session.AuthorizationScope != auth.AuthorizationScopeCategory ||
		body.Session.Category.ID != categoryID || len(body.Profiles) != 1 ||
		body.Profiles[0].Name != "Admin" || body.Profiles[0].CategoryID != categoryID ||
		body.Profiles[0].Category.ID != categoryID ||
		!body.Profiles[0].HasPIN || !body.Profiles[0].CanManage || !body.Profiles[0].Enabled ||
		!body.Profiles[0].Accessible || body.Profiles[0].AvailableUntil == nil ||
		*body.Profiles[0].AvailableUntil != "2026-08-31" || body.Profiles[0].AccessTimezone != "UTC" {
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
	reference := category.CategoryRef{ID: "category-id", Name: "Living Room"}
	service := &fakeAuthService{
		principal: auth.Principal{UserID: "manager-id", SessionID: "current-id"},
		profileSessions: []auth.Session{{
			ID: "session-id", UserID: "user-id", Username: "alex", DeviceID: "device-id",
			DeviceName: "Living Room", Platform: "tvos", IPAddress: "203.0.113.42",
			AuthorizationScope: auth.AuthorizationScopeCategory, Category: &reference,
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
			Username           string                  `json:"username"`
			DeviceName         string                  `json:"deviceName"`
			IPAddress          *string                 `json:"ipAddress"`
			AuthorizationScope auth.AuthorizationScope `json:"authorizationScope"`
			Category           category.CategoryRef    `json:"category"`
		} `json:"sessions"`
	}
	decodeResponse(t, response, &body)
	if service.profileSessionsID != "profile-id" || len(body.Sessions) != 1 ||
		body.Sessions[0].Username != "alex" || body.Sessions[0].DeviceName != "Living Room" ||
		body.Sessions[0].AuthorizationScope != auth.AuthorizationScopeCategory ||
		body.Sessions[0].Category.ID != reference.ID ||
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

func TestSendProfileSessionNotificationTargetsActiveSession(t *testing.T) {
	createdAt := time.Now().UTC().Truncate(time.Second)
	service := &fakeAuthService{
		principal: auth.Principal{UserID: "manager-id", Username: "alex", SessionID: "current-id"},
		sentNotification: auth.SessionNotification{
			ID: 42, Message: "Dinner is ready", SenderUsername: "alex", CreatedAt: createdAt,
		},
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile-id/sessions/session-id/notifications", bytes.NewBufferString(`{"message":"Dinner is ready"}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.notifiedProfileID != "profile-id" || service.notifiedSessionID != "session-id" || service.notifiedMessage != "Dinner is ready" {
		t.Fatalf("unexpected notification target: profile=%q session=%q message=%q", service.notifiedProfileID, service.notifiedSessionID, service.notifiedMessage)
	}
	var body sessionNotificationResponse
	decodeResponse(t, response, &body)
	if body.ID != "42" || body.Message != "Dinner is ready" || body.SenderUsername != "alex" || !body.CreatedAt.Equal(createdAt) {
		t.Fatalf("unexpected notification response: %+v", body)
	}
}

func TestSendProfileSessionNotificationRejectsMalformedUTF8(t *testing.T) {
	service := &fakeAuthService{
		principal: auth.Principal{UserID: "manager-id", Username: "alex", SessionID: "current-id"},
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	requestBody := append([]byte(`{"message":"`), 0xff)
	requestBody = append(requestBody, []byte(`"}`)...)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/profile-id/sessions/session-id/notifications", bytes.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", response.Code, response.Body.String())
	}
	if service.sendNotificationCalls != 0 {
		t.Fatalf("expected malformed UTF-8 to be rejected before send, got %d send calls", service.sendNotificationCalls)
	}
}

func TestListSessionNotificationsRoundTripsLosslessDecimalIDs(t *testing.T) {
	createdAt := time.Now().UTC().Truncate(time.Second)
	service := &fakeAuthService{
		principal: auth.Principal{SessionID: "current-id"},
		notifications: []auth.SessionNotification{{
			ID: 9007199254740993, Message: "Playback starts soon", SenderUsername: "admin", CreatedAt: createdAt,
		}},
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/notifications?after=9007199254740992", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.notificationsAfterID != 9007199254740992 {
		t.Fatalf("expected cursor 9007199254740992, got %d", service.notificationsAfterID)
	}
	var body struct {
		Notifications []sessionNotificationResponse `json:"notifications"`
	}
	decodeResponse(t, response, &body)
	if len(body.Notifications) != 1 || body.Notifications[0].ID != "9007199254740993" || body.Notifications[0].Message != "Playback starts soon" {
		t.Fatalf("unexpected notifications response: %+v", body)
	}
}

func TestBroadcastSessionNotificationReturnsStableAudience(t *testing.T) {
	createdAt := time.Now().UTC().Truncate(time.Second)
	const broadcastID = "a2cf8952-1250-4caf-94de-909f58bdc35e"
	service := &fakeAuthService{
		principal: auth.Principal{UserID: "admin-id", Username: "alex", Role: "admin", SessionID: "current-id"},
		broadcastResult: auth.NotificationBroadcast{
			ID: broadcastID, Message: "<b>Dinner is ready</b>", SenderUsername: "alex", RecipientCount: 3, CreatedAt: createdAt,
		},
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/notifications/broadcast", bytes.NewBufferString(
		`{"idempotencyKey":"`+broadcastID+`","message":"<b>Dinner is ready</b>"}`,
	))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.broadcastCalls != 1 || service.broadcastID != broadcastID || service.broadcastMessage != "<b>Dinner is ready</b>" ||
		service.broadcastPrincipal.Role != "admin" {
		t.Fatalf("unexpected broadcast call: id=%q message=%q principal=%+v calls=%d", service.broadcastID, service.broadcastMessage, service.broadcastPrincipal, service.broadcastCalls)
	}
	var body notificationBroadcastResponse
	decodeResponse(t, response, &body)
	if body.ID != broadcastID || body.Message != "<b>Dinner is ready</b>" || body.SenderUsername != "alex" ||
		body.RecipientCount != 3 || !body.CreatedAt.Equal(createdAt) {
		t.Fatalf("unexpected broadcast response: %+v", body)
	}
}

func TestBroadcastSessionNotificationRequiresAdmin(t *testing.T) {
	service := &fakeAuthService{
		principal:    auth.Principal{UserID: "member-id", Role: "member", SessionID: "current-id"},
		broadcastErr: auth.ErrForbidden,
	}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/notifications/broadcast", bytes.NewBufferString(
		`{"idempotencyKey":"a2cf8952-1250-4caf-94de-909f58bdc35e","message":"Dinner is ready"}`,
	))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAcknowledgeSessionNotificationTargetsAuthenticatedSessionDelivery(t *testing.T) {
	service := &fakeAuthService{principal: auth.Principal{SessionID: "current-id"}}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/notifications/42", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d: %s", response.Code, response.Body.String())
	}
	if service.acknowledgeCalls != 1 || service.acknowledgedNotificationID != 42 {
		t.Fatalf("unexpected acknowledgement: id=%d calls=%d", service.acknowledgedNotificationID, service.acknowledgeCalls)
	}
}

func TestListSessionNotificationsRejectsInvalidCursors(t *testing.T) {
	for _, query := range []string{
		"after=",
		"after=-1",
		"after=%2B1",
		"after=1.5",
		"after=9223372036854775808",
		"after=1&after=2",
	} {
		t.Run(query, func(t *testing.T) {
			service := &fakeAuthService{principal: auth.Principal{SessionID: "current-id"}}
			api := testAPI(&fakeInstanceService{})
			api.auth = service
			request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/notifications?"+query, nil)
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d: %s", response.Code, response.Body.String())
			}
			if service.notificationsCalls != 0 {
				t.Fatalf("expected invalid cursor to be rejected before listing, got %d list calls", service.notificationsCalls)
			}
		})
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
