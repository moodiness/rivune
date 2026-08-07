package jellyfin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/profile"
)

const (
	discoveryServerID  = "c1000000-0000-4000-8000-000000000001"
	discoveryProfileID = "c2000000-0000-4000-8000-000000000002"
)

type discoveryAuthenticationFake struct {
	token           string
	loginResult     LoginResult
	session         AuthenticatedSession
	loginErr        error
	authenticateErr error
	logoutErr       error
	loginCalls      int
	authCalls       int
	logoutCalls     int
	revoked         bool
	lastLogin       CompatLoginInput
}

func (fake *discoveryAuthenticationFake) Login(_ context.Context, input CompatLoginInput) (LoginResult, error) {
	fake.loginCalls++
	fake.lastLogin = input
	if fake.loginErr != nil {
		return LoginResult{}, fake.loginErr
	}
	return fake.loginResult, nil
}

func (fake *discoveryAuthenticationFake) Authenticate(_ context.Context, token string) (AuthenticatedSession, error) {
	fake.authCalls++
	if fake.authenticateErr != nil || fake.revoked || token != fake.token {
		if fake.authenticateErr != nil {
			return AuthenticatedSession{}, fake.authenticateErr
		}
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return fake.session, nil
}

func (fake *discoveryAuthenticationFake) Logout(_ context.Context, _ AuthenticatedSession) error {
	fake.logoutCalls++
	if fake.logoutErr != nil {
		return fake.logoutErr
	}
	fake.revoked = true
	return nil
}

type logoutPlaybackFake struct {
	authentication *discoveryAuthenticationFake
	closeCalls     int
	closeErrors    []error
	closedRevoked  bool
}

func (*logoutPlaybackFake) Sources(context.Context, auth.Principal, playback.SourcesInput) (playback.SourceList, error) {
	return playback.SourceList{}, errors.New("not used")
}

func (*logoutPlaybackFake) Open(context.Context, auth.Principal, playback.ResolveInput) (playback.Delivery, error) {
	return playback.Delivery{}, errors.New("not used")
}

func (*logoutPlaybackFake) Serve(http.ResponseWriter, *http.Request, playback.DeliveryHandle) error {
	return errors.New("not used")
}

func (fake *logoutPlaybackFake) Close(context.Context, auth.Principal, playback.DeliveryHandle) error {
	call := fake.closeCalls
	fake.closeCalls++
	fake.closedRevoked = fake.authentication.revoked
	if call < len(fake.closeErrors) {
		return fake.closeErrors[call]
	}
	return nil
}

func TestLogoutFailurePreservesPlaybackAndRetryRevokesBeforeCleanup(t *testing.T) {
	authentication, _ := newDiscoveryHTTPTestServer(t)
	delivery := &logoutPlaybackFake{authentication: authentication}
	registry := newPlaySessionRegistry(delivery)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	registry.now = func() time.Time { return now }
	registry.cleanupRetryBase = time.Second
	registry.cleanupRetryMax = time.Second
	playID := "c7000000-0000-4000-8000-000000000007"
	mediaID := "c8000000-0000-4000-8000-000000000008"
	registry.entries[playID] = &playSessionEntry{
		compatSessionID: authentication.session.ID, nativeSessionID: authentication.session.Principal.SessionID,
		profileID: authentication.session.ProfileID, deviceID: authentication.session.Client.DeviceID,
		playSessionID: playID, principal: clonePrincipal(authentication.session.Principal),
		sourceOrder: []string{mediaID}, sources: map[string]*playSessionSource{
			mediaID: {descriptor: playSourceDescriptor{ID: mediaID}, handle: opaquePlaybackHandleNamed(t, "logout")},
		},
	}
	handler := &Handler{authentication: authentication, playback: delivery, playSessions: registry}

	authentication.logoutErr = errors.New("database unavailable")
	failed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/Sessions/Logout", nil)
	request.Header.Set("X-Emby-Token", authentication.token)
	handler.handleLogout(failed, request)
	if failed.Code != http.StatusInternalServerError || authentication.revoked || delivery.closeCalls != 0 || len(registry.entries) != 1 {
		t.Fatalf("failed logout status=%d revoked=%t closes=%d entries=%d", failed.Code, authentication.revoked, delivery.closeCalls, len(registry.entries))
	}

	authentication.logoutErr = nil
	delivery.closeErrors = []error{errors.New("native close unavailable"), nil}
	retried := httptest.NewRecorder()
	retry := httptest.NewRequest(http.MethodPost, "/Sessions/Logout", nil)
	retry.Header.Set("X-Emby-Token", authentication.token)
	handler.handleLogout(retried, retry)
	if retried.Code != http.StatusNoContent || !authentication.revoked || authentication.logoutCalls != 2 {
		t.Fatalf("retry status=%d revoked=%t logout calls=%d", retried.Code, authentication.revoked, authentication.logoutCalls)
	}
	if delivery.closeCalls != 1 || !delivery.closedRevoked || len(registry.entries) != 0 || len(registry.cleanupPending) != 1 {
		t.Fatalf("cleanup calls=%d observed revocation=%t entries=%d pending=%d", delivery.closeCalls, delivery.closedRevoked, len(registry.entries), len(registry.cleanupPending))
	}
	now = now.Add(time.Second)
	registry.reap(context.Background())
	if delivery.closeCalls != 2 || len(registry.cleanupPending) != 0 {
		t.Fatalf("logout cleanup retry calls=%d pending=%d", delivery.closeCalls, len(registry.cleanupPending))
	}
}

func TestDiscoveryLoginMeLogoutSequenceUsesOnlyCompatIdentity(t *testing.T) {
	fake, mux := newDiscoveryHTTPTestServer(t)
	login := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(`{"Username":"owner/Kids","Pw":"correct horse","ProfilePin":"1234"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Infuse", Device="Living Room", DeviceId="infuse-device", Version="8.2"`)
	loginResponse := httptest.NewRecorder()
	mux.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginResponse.Code, loginResponse.Body.String())
	}
	var result AuthenticationResult
	decodeCompatTestResponse(t, loginResponse, &result)
	if result.AccessToken != fake.token || result.User.Id != discoveryProfileID || result.User.Name != "Kids" || result.ServerId != discoveryServerID {
		t.Fatalf("unexpected compatibility identity: userID=%q userName=%q serverID=%q tokenMatches=%t", result.User.Id, result.User.Name, result.ServerId, result.AccessToken == fake.token)
	}
	if strings.Contains(loginResponse.Body.String(), fake.session.Principal.SessionID) || strings.Contains(loginResponse.Body.String(), "rivune_at_") {
		t.Fatal("login disclosed native session material")
	}
	if fake.lastLogin.Username != "owner/Kids" || fake.lastLogin.ProfilePIN == nil || *fake.lastLogin.ProfilePIN != "1234" || fake.lastLogin.Client.Client != "Infuse" {
		t.Fatalf("login fields were not parsed as expected: username=%q pinPresent=%t client=%q", fake.lastLogin.Username, fake.lastLogin.ProfilePIN != nil, fake.lastLogin.Client.Client)
	}

	me := httptest.NewRequest(http.MethodGet, "/emby/Users/Me", nil)
	me.Header.Set("X-MediaBrowser-Token", fake.token)
	meResponse := httptest.NewRecorder()
	mux.ServeHTTP(meResponse, me)
	if meResponse.Code != http.StatusOK {
		t.Fatalf("Users/Me status = %d, body = %s", meResponse.Code, meResponse.Body.String())
	}
	var user UserDto
	decodeCompatTestResponse(t, meResponse, &user)
	if user.Id != discoveryProfileID || user.Name != "Kids" || user.ServerId != discoveryServerID {
		t.Fatalf("Users/Me returned wrong bound profile: %+v", user)
	}

	logout := httptest.NewRequest(http.MethodPost, "/Sessions/Logout", nil)
	logout.Header.Set("X-Emby-Token", fake.token)
	logoutResponse := httptest.NewRecorder()
	mux.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusNoContent || fake.logoutCalls != 1 {
		t.Fatalf("logout status = %d, calls = %d", logoutResponse.Code, fake.logoutCalls)
	}

	reuse := httptest.NewRequest(http.MethodGet, "/Users/Me", nil)
	reuse.Header.Set("X-Emby-Token", fake.token)
	reuseResponse := httptest.NewRecorder()
	mux.ServeHTTP(reuseResponse, reuse)
	assertCompatUnauthorized(t, reuseResponse)
	if strings.Contains(reuseResponse.Body.String(), fake.token) {
		t.Fatal("revoked-token response disclosed the credential")
	}
}

func TestAuthenticateByNameAcceptsObservedFieldAliasesWithoutChangingPassword(t *testing.T) {
	for _, test := range []struct {
		name     string
		body     string
		password string
	}{
		{name: "Username and Pw", body: `{"Username":"owner/Kids","Pw":" secret "}`, password: " secret "},
		{name: "UserName and Password", body: `{"UserName":"owner/Kids","Password":"other secret"}`, password: "other secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, mux := newDiscoveryHTTPTestServer(t)
			request := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
			request.Header.Set("Authorization", `MediaBrowser Client="VidHub", Device="Tablet", DeviceId="vidhub-device", Version="2.4", Token=""`)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK || fake.lastLogin.Password != test.password || fake.lastLogin.Client.Client != "VidHub" {
				t.Fatalf("login status=%d client=%q passwordPreserved=%t", response.Code, fake.lastLogin.Client.Client, fake.lastLogin.Password == test.password)
			}
		})
	}
}

func TestAuthenticateByNameEnforcesUTF8PasswordByteLimitForBothAliases(t *testing.T) {
	for _, test := range []struct {
		name       string
		fields     string
		password   string
		wantStatus int
	}{
		{name: "Pw accepts 256 UTF-8 bytes", fields: `"Pw":%q`, password: strings.Repeat("é", 128), wantStatus: http.StatusOK},
		{name: "Password accepts 256 UTF-8 bytes", fields: `"Password":%q`, password: strings.Repeat("é", 128), wantStatus: http.StatusOK},
		{name: "identical aliases accept 256 UTF-8 bytes", fields: `"Pw":%q,"Password":%q`, password: strings.Repeat("é", 128), wantStatus: http.StatusOK},
		{name: "Pw rejects 258 UTF-8 bytes", fields: `"Pw":%q`, password: strings.Repeat("é", 129), wantStatus: http.StatusUnauthorized},
		{name: "Password rejects 258 UTF-8 bytes", fields: `"Password":%q`, password: strings.Repeat("é", 129), wantStatus: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, mux := newDiscoveryHTTPTestServer(t)
			var credentialFields string
			if strings.Count(test.fields, "%q") == 2 {
				credentialFields = fmt.Sprintf(test.fields, test.password, test.password)
			} else {
				credentialFields = fmt.Sprintf(test.fields, test.password)
			}
			body := `{"Username":"owner/Kids",` + credentialFields + `}`
			request := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Infuse", Device="TV", DeviceId="dev", Version="8"`)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("login status=%d, want %d", response.Code, test.wantStatus)
			}
			if test.wantStatus == http.StatusOK {
				if fake.loginCalls != 1 || fake.lastLogin.Password != test.password {
					t.Fatalf("accepted password was not preserved: calls=%d bytes=%d", fake.loginCalls, len(fake.lastLogin.Password))
				}
			} else if fake.loginCalls != 0 {
				t.Fatalf("oversize password reached authentication: calls=%d", fake.loginCalls)
			}
		})
	}
}

func TestAuthenticateByNameFailsClosedWithoutCredentialEnumeration(t *testing.T) {
	fake, mux := newDiscoveryHTTPTestServer(t)
	fake.loginErr = ErrInvalidCompatLogin
	requests := []struct {
		name   string
		body   string
		header string
	}{
		{name: "bad password", body: `{"Username":"owner/Kids","Pw":"wrong"}`, header: `MediaBrowser Client="Infuse", Device="TV", DeviceId="dev", Version="8"`},
		{name: "bad pin", body: `{"Username":"owner/Kids","Pw":"correct","ProfilePin":"9999"}`, header: `MediaBrowser Client="Infuse", Device="TV", DeviceId="dev", Version="8"`},
		{name: "ambiguous bare account", body: `{"Username":"owner","Pw":"correct"}`, header: `MediaBrowser Client="Infuse", Device="TV", DeviceId="dev", Version="8"`},
		{name: "missing auth metadata", body: `{"Username":"owner/Kids","Pw":"correct"}`},
		{name: "conflicting aliases", body: `{"Username":"owner/Kids","UserName":"owner/Adults","Pw":"correct"}`, header: `MediaBrowser Client="Infuse", Device="TV", DeviceId="dev", Version="8"`},
	}
	var canonicalBody string
	for _, test := range requests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.header != "" {
				request.Header.Set("X-Emby-Authorization", test.header)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			assertCompatUnauthorized(t, response)
			if strings.Contains(response.Body.String(), "owner") || strings.Contains(response.Body.String(), "Kids") || strings.Contains(response.Body.String(), "Adults") || strings.Contains(response.Body.String(), "correct") {
				t.Fatalf("login failure disclosed credential or profile data: %s", response.Body.String())
			}
			if canonicalBody == "" {
				canonicalBody = response.Body.String()
			} else if response.Body.String() != canonicalBody {
				t.Fatalf("login failure body differs: got %q, want %q", response.Body.String(), canonicalBody)
			}
		})
	}
}

func TestCompatHTTPRejectsOversizeAmbiguousAndCrossProfileRequests(t *testing.T) {
	fake, mux := newDiscoveryHTTPTestServer(t)
	oversize := `{"Username":"owner/Kids","Pw":"` + strings.Repeat("secret", 3000) + `"}`
	oversizeRequest := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(oversize))
	oversizeRequest.Header.Set("Content-Type", "application/json")
	oversizeRequest.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Infuse", Device="TV", DeviceId="dev", Version="8"`)
	oversizeResponse := httptest.NewRecorder()
	mux.ServeHTTP(oversizeResponse, oversizeRequest)
	if oversizeResponse.Code != http.StatusBadRequest || fake.loginCalls != 0 || strings.Contains(oversizeResponse.Body.String(), "secret") {
		t.Fatalf("oversize request status=%d loginCalls=%d body=%s", oversizeResponse.Code, fake.loginCalls, oversizeResponse.Body.String())
	}
	for _, malformedBody := range []string{
		`{"Username":"owner/Kids","Pw":"correct","Unexpected":"value"}`,
		`{"Username":"owner/Kids","Pw":"correct"} {"Username":"owner/Kids","Pw":"correct"}`,
	} {
		malformedRequest := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(malformedBody))
		malformedRequest.Header.Set("Content-Type", "application/json")
		malformedRequest.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Infuse", Device="TV", DeviceId="dev", Version="8"`)
		malformedResponse := httptest.NewRecorder()
		mux.ServeHTTP(malformedResponse, malformedRequest)
		if malformedResponse.Code != http.StatusBadRequest || fake.loginCalls != 0 {
			t.Fatalf("malformed JSON status=%d loginCalls=%d", malformedResponse.Code, fake.loginCalls)
		}
	}

	otherToken := compatTestToken(7)
	ambiguous := httptest.NewRequest(http.MethodGet, "/Users/Me", nil)
	ambiguous.Header.Set("X-Emby-Token", fake.token)
	ambiguous.Header.Set("X-MediaBrowser-Token", otherToken)
	ambiguousResponse := httptest.NewRecorder()
	mux.ServeHTTP(ambiguousResponse, ambiguous)
	assertCompatUnauthorized(t, ambiguousResponse)

	queryToken := httptest.NewRequest(http.MethodGet, "/Users/Me?api_key="+fake.token, nil)
	queryToken.Header.Set("X-Emby-Token", fake.token)
	queryTokenResponse := httptest.NewRecorder()
	mux.ServeHTTP(queryTokenResponse, queryToken)
	assertCompatUnauthorized(t, queryTokenResponse)

	crossProfile := httptest.NewRequest(http.MethodGet, "/Users/c3000000-0000-4000-8000-000000000003", nil)
	crossProfile.Header.Set("X-Emby-Token", fake.token)
	crossProfileResponse := httptest.NewRecorder()
	mux.ServeHTTP(crossProfileResponse, crossProfile)
	if crossProfileResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-profile path status = %d, want 404", crossProfileResponse.Code)
	}

	headerMismatch := httptest.NewRequest(http.MethodGet, "/Users/Me", nil)
	headerMismatch.Header.Set("X-Emby-Authorization", `MediaBrowser Token="`+fake.token+`", UserId="c3000000-0000-4000-8000-000000000003"`)
	headerMismatchResponse := httptest.NewRecorder()
	mux.ServeHTTP(headerMismatchResponse, headerMismatch)
	if headerMismatchResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-profile auth header status = %d, want 404", headerMismatchResponse.Code)
	}

	fake.authenticateErr = ErrInvalidCompatCredential
	expired := httptest.NewRequest(http.MethodGet, "/Users/Me", nil)
	expired.Header.Set("X-Emby-Token", fake.token)
	expiredResponse := httptest.NewRecorder()
	mux.ServeHTTP(expiredResponse, expired)
	assertCompatUnauthorized(t, expiredResponse)
}

func TestDiscoveryAndShimsExposeOnlyDeterministicCompatibilityData(t *testing.T) {
	fake, mux := newDiscoveryHTTPTestServer(t)
	publicResponse := httptest.NewRecorder()
	mux.ServeHTTP(publicResponse, httptest.NewRequest(http.MethodGet, "/System/Info/Public", nil))
	if publicResponse.Code != http.StatusOK {
		t.Fatalf("public info status = %d", publicResponse.Code)
	}
	var public PublicSystemInfo
	decodeCompatTestResponse(t, publicResponse, &public)
	if public.Id != discoveryServerID || public.ServerName != "Rivune Home" || public.Version != CompatibilityVersion || public.ProductName != CompatibilityProduct {
		t.Fatalf("unexpected public identity: %+v", public)
	}
	if strings.Contains(publicResponse.Body.String(), "test") {
		t.Fatalf("public discovery disclosed runtime version: %s", publicResponse.Body.String())
	}

	quickResponse := httptest.NewRecorder()
	mux.ServeHTTP(quickResponse, httptest.NewRequest(http.MethodGet, "/QuickConnect/Enabled", nil))
	if quickResponse.Code != http.StatusOK || strings.TrimSpace(quickResponse.Body.String()) != "false" {
		t.Fatalf("quick connect response = %d %q", quickResponse.Code, quickResponse.Body.String())
	}

	plugins := httptest.NewRequest(http.MethodGet, "/Plugins", nil)
	plugins.Header.Set("X-Emby-Token", fake.token)
	pluginsResponse := httptest.NewRecorder()
	mux.ServeHTTP(pluginsResponse, plugins)
	if pluginsResponse.Code != http.StatusOK || strings.TrimSpace(pluginsResponse.Body.String()) != "[]" {
		t.Fatalf("plugins shim response = %d %q", pluginsResponse.Code, pluginsResponse.Body.String())
	}
}

func TestCompatErrorsRedactCredentialsAndProviderLocations(t *testing.T) {
	response := httptest.NewRecorder()
	writeCompatError(response, http.StatusInternalServerError, "provider_error", "https://upstream.invalid/video?token=rivune_jf_secret")
	body := response.Body.String()
	for _, forbidden := range []string{"https://", "upstream.invalid", "rivune_jf_", "token="} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("error response disclosed %q: %s", forbidden, body)
		}
	}

	encodeFailure := httptest.NewRecorder()
	writeJSON(encodeFailure, http.StatusOK, make(chan struct{}))
	if encodeFailure.Code != http.StatusInternalServerError || !strings.Contains(encodeFailure.Body.String(), "InternalError") {
		t.Fatalf("encode failure response = %d %q", encodeFailure.Code, encodeFailure.Body.String())
	}
}

func newDiscoveryHTTPTestServer(t *testing.T) (*discoveryAuthenticationFake, http.Handler) {
	t.Helper()
	serverID, err := ParseServerID(discoveryServerID)
	if err != nil {
		t.Fatalf("parse server ID: %v", err)
	}
	token := compatTestToken(3)
	profileID := discoveryProfileID
	principal := auth.Principal{
		SessionID: "c6000000-0000-4000-8000-000000000006", UserID: "c4000000-0000-4000-8000-000000000004",
		Username: "owner", ActiveProfileID: &profileID,
	}
	fake := &discoveryAuthenticationFake{
		token: token,
		loginResult: LoginResult{
			Credential: CompatCredential{Token: token, SessionID: "c5000000-0000-4000-8000-000000000005", ExpiresAt: time.Now().UTC().Add(time.Hour)},
			Profile:    profile.Profile{ID: profileID, Name: "Kids", HasPIN: true, Accessible: true}, Principal: principal,
		},
		session: AuthenticatedSession{
			ID: "c5000000-0000-4000-8000-000000000005", ProfileID: profileID,
			ProfileName: "Kids", ProfileHasPIN: true, ExpiresAt: time.Now().UTC().Add(time.Hour), Principal: principal,
		},
	}
	handler, err := New(Dependencies{ServerInfo: ServerInfo{ID: serverID, Name: "Rivune Home", RuntimeVersion: "test"}, Authentication: fake})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fake, handler
}

func compatTestToken(fill byte) string {
	entropy := make([]byte, compatCredentialBytes)
	for index := range entropy {
		entropy[index] = fill
	}
	return compatCredentialPrefix + base64.RawURLEncoding.EncodeToString(entropy)
}

func assertCompatUnauthorized(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "MediaBrowser" {
		t.Fatalf("status = %d, challenge = %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
	var body CompatErrorResponse
	decodeCompatTestResponse(t, response, &body)
	if body.ResponseStatus.ErrorCode == "" || body.ResponseStatus.Message == "" {
		t.Fatalf("invalid compatibility error: %+v", body)
	}
}

func decodeCompatTestResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

var _ Authentication = (*discoveryAuthenticationFake)(nil)
