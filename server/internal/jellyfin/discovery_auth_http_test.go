package jellyfin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
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
func (fake *discoveryAuthenticationFake) Revalidate(_ context.Context, expected AuthenticatedSession) (AuthenticatedSession, error) {
	if fake.authenticateErr != nil || fake.revoked || !sameAuthenticatedSessionOwner(expected, fake.session) {
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

func (*logoutPlaybackFake) ServeAsset(http.ResponseWriter, *http.Request, playback.DeliveryHandle, string) error {
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
	login := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(`{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"correct horse"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Device="Living Room", DeviceId="generic-client-device", Version="8.2"`)
	loginResponse := httptest.NewRecorder()
	mux.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginResponse.Code, loginResponse.Body.String())
	}
	var result AuthenticationResult
	decodeCompatTestResponse(t, loginResponse, &result)
	if result.AccessToken != fake.token || result.User.Id != discoveryProfileID || result.User.Name != "Kids" || result.User.HasConfiguredEasyPassword || result.ServerId != discoveryServerID ||
		result.SessionInfo.ServerId != discoveryServerID || !result.SessionInfo.IsActive ||
		result.User.Configuration.AudioLanguagePreference != nil || result.User.Configuration.SubtitleLanguagePreference != nil ||
		!result.User.Configuration.HidePlayedInLatest || !result.User.Configuration.RememberAudioSelections || !result.User.Configuration.RememberSubtitleSelections {
		t.Fatalf("unexpected compatibility identity: user=%+v serverID=%q session=%+v tokenMatches=%t", result.User, result.ServerId, result.SessionInfo, result.AccessToken == fake.token)
	}
	var loginJSON map[string]json.RawMessage
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &loginJSON); err != nil {
		t.Fatalf("decode login JSON object: %v", err)
	}
	var sessionJSON map[string]json.RawMessage
	if err := json.Unmarshal(loginJSON["SessionInfo"], &sessionJSON); err != nil {
		t.Fatalf("decode SessionInfo JSON object: %v", err)
	}
	if _, ok := sessionJSON["ServerId"]; !ok {
		t.Fatalf("SessionInfo JSON omitted ServerId: %s", loginResponse.Body.String())
	}
	if active, ok := sessionJSON["IsActive"]; !ok || string(active) != "true" {
		t.Fatalf("SessionInfo JSON omitted active state: %s", loginResponse.Body.String())
	}
	if strings.Contains(loginResponse.Body.String(), fake.session.Principal.SessionID) || strings.Contains(loginResponse.Body.String(), "rivune_at_") {
		t.Fatal("login disclosed native session material")
	}
	if fake.lastLogin.Username != "c3000000-0000-4000-8000-000000000003" || fake.lastLogin.Client.Client != "Generic Client" {
		t.Fatalf("login fields were not parsed as expected: username=%q client=%q", fake.lastLogin.Username, fake.lastLogin.Client.Client)
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
	if user.Id != discoveryProfileID || user.Name != "Kids" || user.ServerId != discoveryServerID || user.HasConfiguredEasyPassword {
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
		{name: "Username and Pw", body: `{"Username":"c3000000-0000-4000-8000-000000000003","Pw":" secret "}`, password: " secret "},
		{name: "UserName and Password", body: `{"UserName":"c3000000-0000-4000-8000-000000000003","Password":"other secret"}`, password: "other secret"},
		{name: "empty Pw and Password", body: `{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"","Password":"alias secret"}`, password: "alias secret"},
		{name: "Pw and empty Password", body: `{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"other alias secret","Password":""}`, password: "other alias secret"},
		{name: "canonical Pw wins conflict", body: `{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"rivune_jfa_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","Password":"legacy-hash"}`, password: "rivune_jfa_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{name: "canonical Password wins conflict", body: `{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"legacy-hash","Password":"rivune_jfa_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`, password: "rivune_jfa_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, mux := newDiscoveryHTTPTestServer(t)
			request := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
			request.Header.Set("Authorization", `MediaBrowser Client="Compatibility Client", Device="Tablet", DeviceId="compatibility-client-device", Version="2.4", Token=""`)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK || fake.lastLogin.Password != test.password || fake.lastLogin.Client.Client != "Compatibility Client" {
				t.Fatalf("login status=%d client=%q passwordPreserved=%t", response.Code, fake.lastLogin.Client.Client, fake.lastLogin.Password == test.password)
			}
		})
	}
}

func TestAuthenticateByNameLogsSafeClientMetadataFailureStage(t *testing.T) {
	fake, _ := newDiscoveryHTTPTestServer(t)
	serverID, err := ParseServerID(discoveryServerID)
	if err != nil {
		t.Fatalf("parse server ID: %v", err)
	}
	var logs bytes.Buffer
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Rivune Home", RuntimeVersion: "test"},
		Authentication: fake,
		Logger:         slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	secret := "never-log-this-app-password"
	device := "never-log-this-device"
	request := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(
		`{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"`+secret+`"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Device="`+device+`"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertCompatUnauthorized(t, response)

	logged := logs.String()
	if !strings.Contains(logged, `"msg":"`+compatLoginRejectedMessage+`"`) ||
		!strings.Contains(logged, `"stage":"`+compatLoginStageClientMetadata+`:`+clientIdentityFailureDeviceIDMissing+`"`) {
		t.Fatalf("missing sanitized failure stage: %s", logged)
	}
	for _, forbidden := range []string{secret, device, discoveryProfileID} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("login diagnostics disclosed %q: %s", forbidden, logged)
		}
	}
	if fake.loginCalls != 0 {
		t.Fatalf("invalid client metadata reached authentication: calls=%d", fake.loginCalls)
	}
}

func TestAuthenticateByNameLogsSafePasswordFailureDetail(t *testing.T) {
	fake, _ := newDiscoveryHTTPTestServer(t)
	serverID, err := ParseServerID(discoveryServerID)
	if err != nil {
		t.Fatalf("parse server ID: %v", err)
	}
	var logs bytes.Buffer
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Rivune Home", RuntimeVersion: "test"},
		Authentication: fake,
		Logger:         slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	secret := "never-log-this-password-alias"
	differentSecret := "different-" + secret
	request := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(
		`{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"`+secret+`","Password":"`+differentSecret+`"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Device="TV", DeviceId="generic-client-device", Version="8"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertCompatUnauthorized(t, response)

	logged := logs.String()
	wantStage := compatLoginStagePasswordShape + ":" + compatSecretFailureAliasesDiffer
	if !strings.Contains(logged, `"stage":"`+wantStage+`"`) || strings.Contains(logged, secret) || strings.Contains(logged, differentSecret) {
		t.Fatalf("unsafe or missing password failure detail: %s", logged)
	}
	if fake.loginCalls != 0 {
		t.Fatalf("conflicting secret aliases reached authentication: calls=%d", fake.loginCalls)
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
			body := `{"Username":"c3000000-0000-4000-8000-000000000003",` + credentialFields + `}`
			request := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Device="TV", DeviceId="dev", Version="8"`)
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

func TestAuthenticateByNameRejectsNativeAccountUsernameBeforeAuthentication(t *testing.T) {
	fake, mux := newDiscoveryHTTPTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(`{"Username":"owner","Pw":"native-account-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Device="TV", DeviceId="dev", Version="8"`)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	assertCompatUnauthorized(t, response)
	if fake.loginCalls != 0 || strings.Contains(response.Body.String(), "owner") || strings.Contains(response.Body.String(), "native-account-password") {
		t.Fatalf("native account credential reached compatibility authentication: calls=%d body=%s", fake.loginCalls, response.Body.String())
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
		{name: "bad password", body: `{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"wrong"}`, header: `MediaBrowser Client="Generic Client", Device="TV", DeviceId="dev", Version="8"`},
		{name: "native account name", body: `{"Username":"owner","Pw":"correct"}`, header: `MediaBrowser Client="Generic Client", Device="TV", DeviceId="dev", Version="8"`},
		{name: "unknown credential", body: `{"Username":"c4000000-0000-4000-8000-000000000004","Pw":"correct"}`, header: `MediaBrowser Client="Generic Client", Device="TV", DeviceId="dev", Version="8"`},
		{name: "missing auth metadata", body: `{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"correct"}`},
		{name: "conflicting aliases", body: `{"Username":"c3000000-0000-4000-8000-000000000003","UserName":"c4000000-0000-4000-8000-000000000004","Pw":"correct"}`, header: `MediaBrowser Client="Generic Client", Device="TV", DeviceId="dev", Version="8"`},
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
			if strings.Contains(response.Body.String(), "c3000000") || strings.Contains(response.Body.String(), "c4000000") || strings.Contains(response.Body.String(), "correct") {
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

func TestCompatHTTPRejectsOversizeAndCrossProfileAndHonorsCredentialPrecedence(t *testing.T) {
	fake, mux := newDiscoveryHTTPTestServer(t)
	oversize := `{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"` + strings.Repeat("secret", 3000) + `"}`
	oversizeRequest := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(oversize))
	oversizeRequest.Header.Set("Content-Type", "application/json")
	oversizeRequest.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Device="TV", DeviceId="dev", Version="8"`)
	oversizeResponse := httptest.NewRecorder()
	mux.ServeHTTP(oversizeResponse, oversizeRequest)
	if oversizeResponse.Code != http.StatusBadRequest || fake.loginCalls != 0 || strings.Contains(oversizeResponse.Body.String(), "secret") {
		t.Fatalf("oversize request status=%d loginCalls=%d body=%s", oversizeResponse.Code, fake.loginCalls, oversizeResponse.Body.String())
	}
	for _, malformedBody := range []string{
		`{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"correct","ProfilePin":"1234"}`,
		`{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"correct","Unexpected":"value"}`,
		`{"Username":"c3000000-0000-4000-8000-000000000003","Pw":"correct"} {"Username":"c3000000-0000-4000-8000-000000000003","Pw":"correct"}`,
	} {
		malformedRequest := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(malformedBody))
		malformedRequest.Header.Set("Content-Type", "application/json")
		malformedRequest.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Device="TV", DeviceId="dev", Version="8"`)
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
	if ambiguousResponse.Code != http.StatusOK {
		t.Fatalf("credential precedence status=%d body=%s", ambiguousResponse.Code, ambiguousResponse.Body.String())
	}

	queryToken := httptest.NewRequest(http.MethodGet, "/Users/Me?ApiKey="+fake.token, nil)
	queryTokenResponse := httptest.NewRecorder()
	mux.ServeHTTP(queryTokenResponse, queryToken)
	if queryTokenResponse.Code != http.StatusOK {
		t.Fatalf("global ApiKey query status=%d body=%s", queryTokenResponse.Code, queryTokenResponse.Body.String())
	}

	bearer := httptest.NewRequest(http.MethodGet, "/Users/Me", nil)
	bearer.Header.Set("Authorization", "Bearer "+fake.token)
	bearerResponse := httptest.NewRecorder()
	mux.ServeHTTP(bearerResponse, bearer)
	if bearerResponse.Code != http.StatusOK {
		t.Fatalf("bearer authorization status=%d body=%s", bearerResponse.Code, bearerResponse.Body.String())
	}

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

	unschemedMismatch := httptest.NewRequest(http.MethodGet, "/Users/Me", nil)
	unschemedMismatch.Header.Set("X-MediaBrowser-Authorization", `Token="`+fake.token+`", UserId="c3000000-0000-4000-8000-000000000003"`)
	unschemedMismatchResponse := httptest.NewRecorder()
	mux.ServeHTTP(unschemedMismatchResponse, unschemedMismatch)
	if unschemedMismatchResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-profile unschemed auth header status = %d, want 404", unschemedMismatchResponse.Code)
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
	if public.Id != discoveryServerID || public.ServerName != "Rivune Home" || public.Version != CompatibilityVersion || public.ProductName != "Jellyfin Server" ||
		public.LocalAddress != "" || public.OperatingSystem != "" || !public.StartupWizardCompleted {
		t.Fatalf("unexpected public identity: %+v", public)
	}
	if strings.Contains(publicResponse.Body.String(), "test") {
		t.Fatalf("public discovery disclosed runtime version: %s", publicResponse.Body.String())
	}
	var publicJSON map[string]json.RawMessage
	if err := json.Unmarshal(publicResponse.Body.Bytes(), &publicJSON); err != nil {
		t.Fatalf("decode public JSON object: %v", err)
	}
	for _, key := range []string{"Id", "LocalAddress", "ServerName", "Version", "ProductName", "StartupWizardCompleted", "OperatingSystem"} {
		if _, ok := publicJSON[key]; !ok {
			t.Fatalf("public JSON omitted %q: %s", key, publicResponse.Body.String())
		}
	}
	if len(publicJSON) != 7 || strings.Count(publicResponse.Body.String(), `"OperatingSystem"`) != 1 {
		t.Fatalf("public JSON fields are not exact: %s", publicResponse.Body.String())
	}

	systemRequest := httptest.NewRequest(http.MethodGet, "/System/Info", nil)
	systemRequest.Header.Set("X-Emby-Token", fake.token)
	systemResponse := httptest.NewRecorder()
	mux.ServeHTTP(systemResponse, systemRequest)
	if systemResponse.Code != http.StatusOK || strings.Count(systemResponse.Body.String(), `"OperatingSystem"`) != 1 {
		t.Fatalf("system info status=%d body=%s", systemResponse.Code, systemResponse.Body.String())
	}

	quickResponse := httptest.NewRecorder()
	mux.ServeHTTP(quickResponse, httptest.NewRequest(http.MethodGet, "/QuickConnect/Enabled", nil))
	if quickResponse.Code != http.StatusOK || strings.TrimSpace(quickResponse.Body.String()) != "false" {
		t.Fatalf("quick connect response = %d %q", quickResponse.Code, quickResponse.Body.String())
	}

	brandingResponse := httptest.NewRecorder()
	mux.ServeHTTP(brandingResponse, httptest.NewRequest(http.MethodGet, "/branding/configuration", nil))
	if brandingResponse.Code != http.StatusOK || strings.TrimSpace(brandingResponse.Body.String()) != "{}" {
		t.Fatalf("public branding response = %d %q", brandingResponse.Code, brandingResponse.Body.String())
	}

	capabilities := httptest.NewRequest(http.MethodPost, "/Sessions/Capabilities/Full", strings.NewReader(`{"PlayableMediaTypes":["Video"]}`))
	capabilities.Header.Set("X-Emby-Token", fake.token)
	capabilitiesResponse := httptest.NewRecorder()
	mux.ServeHTTP(capabilitiesResponse, capabilities)
	if capabilitiesResponse.Code != http.StatusNoContent || capabilitiesResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("session capabilities response = %d headers=%v", capabilitiesResponse.Code, capabilitiesResponse.Header())
	}

	simpleCapabilities := httptest.NewRequest(http.MethodPost, "/Sessions/Capabilities?Id="+fake.session.ID+"&PlayableMediaTypes=Video&SupportedCommands=DisplayContent%2CPlay&SupportsMediaControl=true&SupportsPersistentIdentifier=true", nil)
	simpleCapabilities.Header.Set("X-Emby-Token", fake.token)
	simpleCapabilitiesResponse := httptest.NewRecorder()
	mux.ServeHTTP(simpleCapabilitiesResponse, simpleCapabilities)
	if simpleCapabilitiesResponse.Code != http.StatusNoContent || simpleCapabilitiesResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("simple session capabilities response = %d headers=%v", simpleCapabilitiesResponse.Code, simpleCapabilitiesResponse.Header())
	}

	largePlayableParts := make([]string, 15)
	largeSupportedParts := make([]string, 17)
	for index := range largePlayableParts {
		largePlayableParts[index] = strings.Repeat("a", 64)
	}
	for index := range largeSupportedParts {
		largeSupportedParts[index] = strings.Repeat("b", 64)
	}
	largePlayableList := strings.Join(largePlayableParts, ",")
	largeSupportedList := strings.Join(largeSupportedParts, ",")
	largeCapabilities := httptest.NewRequest(http.MethodPost, "/Sessions/Capabilities?PlayableMediaTypes="+largePlayableList+"&SupportedCommands="+largeSupportedList, nil)
	largeCapabilities.Header.Set("X-Emby-Token", fake.token)
	largeCapabilitiesResponse := httptest.NewRecorder()
	mux.ServeHTTP(largeCapabilitiesResponse, largeCapabilities)
	if largeCapabilitiesResponse.Code != http.StatusNoContent {
		t.Fatalf("large valid session capabilities status = %d, want 204", largeCapabilitiesResponse.Code)
	}

	foreignCapabilities := httptest.NewRequest(http.MethodPost, "/Sessions/Capabilities?Id=c6000000-0000-4000-8000-000000000006", nil)
	foreignCapabilities.Header.Set("X-Emby-Token", fake.token)
	foreignCapabilitiesResponse := httptest.NewRecorder()
	mux.ServeHTTP(foreignCapabilitiesResponse, foreignCapabilities)
	if foreignCapabilitiesResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign session capabilities status = %d, want 404", foreignCapabilitiesResponse.Code)
	}

	invalidCapabilities := httptest.NewRequest(http.MethodPost, "/Sessions/Capabilities?SupportsMediaControl=maybe", nil)
	invalidCapabilities.Header.Set("X-Emby-Token", fake.token)
	invalidCapabilitiesResponse := httptest.NewRecorder()
	mux.ServeHTTP(invalidCapabilitiesResponse, invalidCapabilities)
	if invalidCapabilitiesResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid session capabilities status = %d, want 400", invalidCapabilitiesResponse.Code)
	}

	syncPlay := httptest.NewRequest(http.MethodGet, "/SyncPlay/List", nil)
	syncPlay.Header.Set("X-Emby-Token", fake.token)
	syncPlayResponse := httptest.NewRecorder()
	mux.ServeHTTP(syncPlayResponse, syncPlay)
	if syncPlayResponse.Code != http.StatusOK || strings.TrimSpace(syncPlayResponse.Body.String()) != "[]" {
		t.Fatalf("sync play response = %d %q", syncPlayResponse.Code, syncPlayResponse.Body.String())
	}

	bitrate := httptest.NewRequest(http.MethodGet, "/Playback/BitrateTest?Size=7", nil)
	bitrate.Header.Set("X-Emby-Token", fake.token)
	bitrateResponse := httptest.NewRecorder()
	mux.ServeHTTP(bitrateResponse, bitrate)
	if bitrateResponse.Code != http.StatusOK || bitrateResponse.Body.Len() != 7 || bitrateResponse.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("bitrate response = %d bytes=%d headers=%v", bitrateResponse.Code, bitrateResponse.Body.Len(), bitrateResponse.Header())
	}

	oversizeBitrate := httptest.NewRequest(http.MethodGet, "/Playback/BitrateTest?Size="+strconv.Itoa(maximumCompatBitrateTestBytes+1), nil)
	oversizeBitrate.Header.Set("X-Emby-Token", fake.token)
	oversizeBitrateResponse := httptest.NewRecorder()
	mux.ServeHTTP(oversizeBitrateResponse, oversizeBitrate)
	if oversizeBitrateResponse.Code != http.StatusBadRequest || oversizeBitrateResponse.Body.Len() == 0 {
		t.Fatalf("oversize bitrate response = %d bytes=%d", oversizeBitrateResponse.Code, oversizeBitrateResponse.Body.Len())
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
			ProfileName: "Kids", ExpiresAt: time.Now().UTC().Add(time.Hour), Principal: principal,
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
