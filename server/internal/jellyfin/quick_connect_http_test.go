package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

type quickConnectHTTPFake struct {
	*discoveryAuthenticationFake
	status        QuickConnectStatus
	approved      bool
	beginCalls    int
	pollCalls     int
	exchangeCalls int
}

func (fake *quickConnectHTTPFake) BeginQuickConnect(_ context.Context, client ClientIdentity) (QuickConnectStatus, error) {
	fake.beginCalls++
	if client.DeviceID != fake.status.DeviceID {
		return QuickConnectStatus{}, auth.ErrInvalidDeviceCode
	}
	return fake.status, nil
}

func (fake *quickConnectHTTPFake) PollQuickConnect(_ context.Context, secret string, client ClientIdentity) (QuickConnectStatus, error) {
	fake.pollCalls++
	if secret != fake.status.Secret || client.DeviceID != fake.status.DeviceID {
		return QuickConnectStatus{}, auth.ErrInvalidDeviceCode
	}
	status := fake.status
	status.Authenticated = fake.approved
	return status, nil
}

func (fake *quickConnectHTTPFake) LoginQuickConnect(_ context.Context, secret string, client ClientIdentity) (LoginResult, error) {
	fake.exchangeCalls++
	if !fake.approved || secret != fake.status.Secret || client.DeviceID != fake.status.DeviceID {
		return LoginResult{}, auth.ErrInvalidDeviceCode
	}
	result := fake.loginResult
	result.Client = ClientIdentity{
		Client: fake.status.AppName, Device: fake.status.DeviceName,
		DeviceID: fake.status.DeviceID, Version: fake.status.AppVersion,
	}
	return result, nil
}

type quickConnectVersionNativeFake struct {
	authLifecycleNativeFake
	input auth.JellyfinQuickConnectInput
}

func (fake *quickConnectVersionNativeFake) BeginJellyfinQuickConnect(_ context.Context, input auth.JellyfinQuickConnectInput) (auth.DeviceAuthorization, error) {
	fake.input = input
	return auth.DeviceAuthorization{
		DeviceCode: "rivune_dc_version", UserCode: "STUV-WXYZ", CreatedAt: time.Now().UTC(),
	}, nil
}

func (fake *quickConnectVersionNativeFake) PollJellyfinQuickConnect(_ context.Context, secret, deviceID string) (auth.JellyfinQuickConnectStatus, error) {
	if secret != "rivune_dc_version" || deviceID != fake.input.ClientDeviceID {
		return auth.JellyfinQuickConnectStatus{}, auth.ErrInvalidDeviceCode
	}
	return auth.JellyfinQuickConnectStatus{
		Secret: secret, UserCode: "STUV-WXYZ", CreatedAt: time.Now().UTC(),
		DeviceID: fake.input.ClientDeviceID, DeviceName: fake.input.DeviceName,
		AppName: fake.input.AppName, AppVersion: fake.input.AppVersion,
	}, nil
}

func (*quickConnectVersionNativeFake) ExchangeJellyfinQuickConnect(context.Context, string, string) (auth.JellyfinQuickConnectResult, error) {
	return auth.JellyfinQuickConnectResult{}, auth.ErrDeviceAuthorizationPending
}

func TestQuickConnectPollKeepsInitiatingAppVersion(t *testing.T) {
	native := &quickConnectVersionNativeFake{}
	service, err := NewAuthenticationService(
		func(context.Context, auth.JellyfinProfileLoginInput) (auth.JellyfinProfileLoginResult, error) {
			return auth.JellyfinProfileLoginResult{}, errors.New("unexpected profile login")
		},
		native,
		&SessionStore{},
	)
	if err != nil {
		t.Fatal(err)
	}
	initiatingClient := ClientIdentity{
		Client: "ARVIO", Device: "Android", DeviceID: "arvio-android-id", Version: "2.4.0",
	}
	started, err := service.BeginQuickConnect(context.Background(), initiatingClient)
	if err != nil {
		t.Fatal(err)
	}
	pollingClient := ClientIdentity{
		Client: "Jellyfin", Device: "Jellyfin client", DeviceID: "arvio-android-id", Version: "unknown",
	}
	polled, err := service.PollQuickConnect(context.Background(), started.Secret, pollingClient)
	if err != nil {
		t.Fatal(err)
	}
	if native.input.AppVersion != "2.4.0" || polled.AppVersion != "2.4.0" {
		t.Fatalf("initiating input version=%q polled version=%q", native.input.AppVersion, polled.AppVersion)
	}
}

func TestARVIOQuickConnectExactHTTPSequence(t *testing.T) {
	base, _ := newDiscoveryHTTPTestServer(t)
	quick := &quickConnectHTTPFake{
		discoveryAuthenticationFake: base,
		status: QuickConnectStatus{
			Secret: "rivune_dc_quick-connect-secret", Code: "BCDF-GHJK",
			DateAdded: time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC),
			DeviceID:  "arvio-android-id", DeviceName: "Android", AppName: "ARVIO", AppVersion: "2.4.0",
		},
	}
	serverID, err := ParseServerID(discoveryServerID)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Rivune Home", RuntimeVersion: "test"},
		Authentication: quick, QuickConnect: quick,
	})
	if err != nil {
		t.Fatal(err)
	}

	enabled := httptest.NewRecorder()
	handler.ServeHTTP(enabled, httptest.NewRequest(http.MethodGet, "/QuickConnect/Enabled", nil))
	if enabled.Code != http.StatusOK || strings.TrimSpace(enabled.Body.String()) != "true" {
		t.Fatalf("enabled response = %d %q", enabled.Code, enabled.Body.String())
	}

	initiate := arvioQuickConnectRequest(http.MethodPost, "/QuickConnect/Initiate", `{}`)
	initiateResponse := httptest.NewRecorder()
	handler.ServeHTTP(initiateResponse, initiate)
	var initiated QuickConnectResult
	decodeCompatTestResponse(t, initiateResponse, &initiated)
	if initiateResponse.Code != http.StatusOK || initiated.Code != quick.status.Code || initiated.Secret != quick.status.Secret || initiated.Authenticated {
		t.Fatalf("initiate response = %d %+v", initiateResponse.Code, initiated)
	}

	pending := minimalQuickConnectPollRequest("/QuickConnect/Connect?secret=" + quick.status.Secret)
	pendingResponse := httptest.NewRecorder()
	handler.ServeHTTP(pendingResponse, pending)
	var pendingResult QuickConnectResult
	decodeCompatTestResponse(t, pendingResponse, &pendingResult)
	if pendingResponse.Code != http.StatusOK || pendingResult.Authenticated || pendingResult.AppVersion != "2.4.0" || quick.exchangeCalls != 0 {
		t.Fatalf("pending response = %d %+v exchanges=%d", pendingResponse.Code, pendingResult, quick.exchangeCalls)
	}

	quick.approved = true
	approved := minimalQuickConnectPollRequest("/QuickConnect/Connect?secret=" + quick.status.Secret)
	approvedResponse := httptest.NewRecorder()
	handler.ServeHTTP(approvedResponse, approved)
	var approvedResult QuickConnectResult
	decodeCompatTestResponse(t, approvedResponse, &approvedResult)
	if approvedResponse.Code != http.StatusOK || !approvedResult.Authenticated {
		t.Fatalf("approved poll response = %d %+v", approvedResponse.Code, approvedResult)
	}

	complete := arvioQuickConnectRequest(http.MethodPost, "/Users/AuthenticateWithQuickConnect", `{"Secret":"`+quick.status.Secret+`"}`)
	completeResponse := httptest.NewRecorder()
	handler.ServeHTTP(completeResponse, complete)
	var authentication AuthenticationResult
	if err := json.Unmarshal(completeResponse.Body.Bytes(), &authentication); err != nil {
		t.Fatal(err)
	}
	if completeResponse.Code != http.StatusOK || authentication.AccessToken != base.loginResult.Credential.Token || authentication.User.Id != discoveryProfileID {
		t.Fatalf("complete response = %d %+v body=%s", completeResponse.Code, authentication, completeResponse.Body.String())
	}
	if quick.beginCalls != 1 || quick.pollCalls != 2 || quick.exchangeCalls != 1 {
		t.Fatalf("calls begin=%d poll=%d exchange=%d", quick.beginCalls, quick.pollCalls, quick.exchangeCalls)
	}
}

func TestQuickConnectExchangeMetadataCannotReplaceInitiatingIdentity(t *testing.T) {
	base, _ := newDiscoveryHTTPTestServer(t)
	quick := &quickConnectHTTPFake{
		discoveryAuthenticationFake: base,
		approved:                    true,
		status: QuickConnectStatus{
			Secret: "rivune_dc_initiating-identity", Code: "BCDF-GHJK",
			DateAdded: time.Now().UTC(), DeviceID: "arvio-android-id",
			DeviceName: "Initiating Android", AppName: "ARVIO", AppVersion: "2.4.0",
		},
	}
	serverID, _ := ParseServerID(discoveryServerID)
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Rivune Home"},
		Authentication: quick, QuickConnect: quick,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := minimalQuickConnectExchangeRequest(`{"Secret":"` + quick.status.Secret + `"}`)
	request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="`+strings.Repeat("S", 100)+`", Device="Other device", DeviceId="arvio-android-id", Version="99.0"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var authentication AuthenticationResult
	decodeCompatTestResponse(t, response, &authentication)
	if response.Code != http.StatusOK || authentication.SessionInfo.Client != "ARVIO" ||
		authentication.SessionInfo.DeviceName != "Initiating Android" ||
		authentication.SessionInfo.DeviceId != "arvio-android-id" ||
		authentication.SessionInfo.ApplicationVersion != "2.4.0" {
		t.Fatalf("exchange status=%d session=%+v", response.Code, authentication.SessionInfo)
	}
}

func TestQuickConnectInitiateRejectsNullWithoutAllocatingCode(t *testing.T) {
	base, _ := newDiscoveryHTTPTestServer(t)
	quick := &quickConnectHTTPFake{discoveryAuthenticationFake: base, status: QuickConnectStatus{
		Secret: "rivune_dc_should-not-allocate", Code: "JKLM-NPQR",
		DeviceID: "arvio-android-id", DeviceName: "Android", AppName: "ARVIO", AppVersion: "2.4.0",
	}}
	serverID, _ := ParseServerID(discoveryServerID)
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Rivune Home"},
		Authentication: quick, QuickConnect: quick,
	})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, arvioQuickConnectRequest(http.MethodPost, "/QuickConnect/Initiate", `null`))
	if response.Code != http.StatusBadRequest || quick.beginCalls != 0 || strings.Contains(response.Body.String(), quick.status.Code) {
		t.Fatalf("null initiate status=%d calls=%d body=%s", response.Code, quick.beginCalls, response.Body.String())
	}
}

func TestQuickConnectAcceptsLongRawDeviceIDWithStableCanonicalBinding(t *testing.T) {
	base, _ := newDiscoveryHTTPTestServer(t)
	rawDeviceID := strings.Repeat("d", 300)
	canonicalDeviceID, ok := canonicalCompatDeviceID(rawDeviceID)
	if !ok {
		t.Fatal("long DeviceId did not canonicalize")
	}
	quick := &quickConnectHTTPFake{discoveryAuthenticationFake: base, status: QuickConnectStatus{
		Secret: "rivune_dc_long-device", Code: "STUV-WXYZ", DeviceID: canonicalDeviceID,
		DeviceName: "Android", AppName: "ARVIO", AppVersion: "2.4.0",
	}}
	serverID, _ := ParseServerID(discoveryServerID)
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Rivune Home"},
		Authentication: quick, QuickConnect: quick,
	})
	if err != nil {
		t.Fatal(err)
	}

	initiate := arvioQuickConnectRequest(http.MethodPost, "/QuickConnect/Initiate", `{}`)
	initiate.Header.Set("X-Emby-Authorization", `MediaBrowser Client="ARVIO", Device="Android", DeviceId="`+rawDeviceID+`", Version="2.4.0"`)
	initiateResponse := httptest.NewRecorder()
	handler.ServeHTTP(initiateResponse, initiate)
	var initiated QuickConnectResult
	decodeCompatTestResponse(t, initiateResponse, &initiated)
	if initiateResponse.Code != http.StatusOK || initiated.DeviceID != canonicalDeviceID || len(initiated.DeviceID) > 128 {
		t.Fatalf("initiate status=%d device=%q", initiateResponse.Code, initiated.DeviceID)
	}

	poll := minimalQuickConnectPollRequest("/QuickConnect/Connect?secret=" + quick.status.Secret)
	poll.Header.Set("X-Emby-Authorization", `MediaBrowser DeviceId="`+rawDeviceID+`"`)
	pollResponse := httptest.NewRecorder()
	handler.ServeHTTP(pollResponse, poll)
	var polled QuickConnectResult
	decodeCompatTestResponse(t, pollResponse, &polled)
	if pollResponse.Code != http.StatusOK || polled.DeviceID != canonicalDeviceID || quick.beginCalls != 1 || quick.pollCalls != 1 {
		t.Fatalf("poll status=%d result=%+v calls=%d/%d", pollResponse.Code, polled, quick.beginCalls, quick.pollCalls)
	}
}

func TestQuickConnectRejectsWrongClientAndPreApprovalExchange(t *testing.T) {
	base, _ := newDiscoveryHTTPTestServer(t)
	quick := &quickConnectHTTPFake{discoveryAuthenticationFake: base, status: QuickConnectStatus{
		Secret: "rivune_dc_bound-secret", Code: "JKLM-NPQR", DateAdded: time.Now().UTC(),
		DeviceID: "arvio-android-id", DeviceName: "Android", AppName: "ARVIO", AppVersion: "2.4.0",
	}}
	serverID, _ := ParseServerID(discoveryServerID)
	handler, err := New(Dependencies{ServerInfo: ServerInfo{ID: serverID, Name: "Rivune Home"}, Authentication: quick, QuickConnect: quick})
	if err != nil {
		t.Fatal(err)
	}

	wrongClient := arvioQuickConnectRequest(http.MethodGet, "/QuickConnect/Connect?secret="+quick.status.Secret, "")
	wrongClient.Header.Set("X-Emby-Authorization", `MediaBrowser Client="ARVIO", Device="Android", DeviceId="other-device", Version="2.4.0"`)
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrongClient)
	if wrongResponse.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-client status = %d body=%s", wrongResponse.Code, wrongResponse.Body.String())
	}

	premature := arvioQuickConnectRequest(http.MethodPost, "/Users/AuthenticateWithQuickConnect", `{"Secret":"`+quick.status.Secret+`"}`)
	prematureResponse := httptest.NewRecorder()
	handler.ServeHTTP(prematureResponse, premature)
	if prematureResponse.Code != http.StatusUnauthorized || quick.exchangeCalls != 1 {
		t.Fatalf("pre-approval exchange = %d calls=%d body=%s", prematureResponse.Code, quick.exchangeCalls, prematureResponse.Body.String())
	}
}

func TestQuickConnectRequiresOnlyStableDeviceIDMetadata(t *testing.T) {
	base, _ := newDiscoveryHTTPTestServer(t)
	native := &quickConnectVersionNativeFake{}
	quick, err := NewAuthenticationService(
		func(context.Context, auth.JellyfinProfileLoginInput) (auth.JellyfinProfileLoginResult, error) {
			return auth.JellyfinProfileLoginResult{}, errors.New("unexpected profile login")
		},
		native,
		&SessionStore{},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverID, _ := ParseServerID(discoveryServerID)
	handler, err := New(Dependencies{ServerInfo: ServerInfo{ID: serverID, Name: "Rivune Home"}, Authentication: base, QuickConnect: quick})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/QuickConnect/Initiate", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Emby-Authorization", `MediaBrowser DeviceId="arvio-android-id"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var initiated QuickConnectResult
	decodeCompatTestResponse(t, response, &initiated)
	if response.Code != http.StatusOK || initiated.AppVersion != "unknown" || native.input.AppVersion != "unknown" {
		t.Fatalf("minimal initiation status=%d result=%+v stored=%+v", response.Code, initiated, native.input)
	}
	poll := minimalQuickConnectPollRequest("/QuickConnect/Connect?secret=" + initiated.Secret)
	pollResponse := httptest.NewRecorder()
	handler.ServeHTTP(pollResponse, poll)
	var polled QuickConnectResult
	decodeCompatTestResponse(t, pollResponse, &polled)
	if pollResponse.Code != http.StatusOK || polled.AppVersion != "unknown" {
		t.Fatalf("minimal poll status=%d result=%+v", pollResponse.Code, polled)
	}
}

func TestQuickConnectRejectsMalformedSecretsBeforeServiceLookup(t *testing.T) {
	base, _ := newDiscoveryHTTPTestServer(t)
	quick := &quickConnectHTTPFake{discoveryAuthenticationFake: base, status: QuickConnectStatus{
		Secret: "rivune_dc_known", Code: "STUV-WXYZ", DateAdded: time.Now().UTC(),
		DeviceID: "arvio-android-id", DeviceName: "Android", AppName: "ARVIO", AppVersion: "2.4.0",
	}}
	serverID, _ := ParseServerID(discoveryServerID)
	handler, err := New(Dependencies{ServerInfo: ServerInfo{ID: serverID, Name: "Rivune Home"}, Authentication: quick, QuickConnect: quick})
	if err != nil {
		t.Fatal(err)
	}

	malformed := []string{
		" rivune_dc_secret",
		"rivune_dc_secret ",
		"rivune_dc_bad.secret",
		"rivune_dc_",
		"rivune_dc_" + strings.Repeat("a", quickConnectSecretMaxLength-len(quickConnectSecretPrefix)+1),
	}
	for _, secret := range malformed {
		poll := minimalQuickConnectPollRequest("/QuickConnect/Connect?secret=" + url.QueryEscape(secret))
		pollResponse := httptest.NewRecorder()
		handler.ServeHTTP(pollResponse, poll)
		if pollResponse.Code != http.StatusBadRequest {
			t.Errorf("poll secret %q status=%d body=%s", secret, pollResponse.Code, pollResponse.Body.String())
		}

		exchange := minimalQuickConnectExchangeRequest(`{"Secret":` + string(mustJSONMarshal(t, secret)) + `}`)
		exchangeResponse := httptest.NewRecorder()
		handler.ServeHTTP(exchangeResponse, exchange)
		if exchangeResponse.Code != http.StatusBadRequest {
			t.Errorf("exchange secret %q status=%d body=%s", secret, exchangeResponse.Code, exchangeResponse.Body.String())
		}
	}
	if quick.pollCalls != 0 || quick.exchangeCalls != 0 {
		t.Fatalf("malformed secrets reached service: poll=%d exchange=%d", quick.pollCalls, quick.exchangeCalls)
	}

	unknown := "rivune_dc_well-formed_unknown"
	poll := minimalQuickConnectPollRequest("/QuickConnect/Connect?secret=" + unknown)
	pollResponse := httptest.NewRecorder()
	handler.ServeHTTP(pollResponse, poll)
	exchange := minimalQuickConnectExchangeRequest(`{"Secret":"` + unknown + `"}`)
	exchangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(exchangeResponse, exchange)
	assertCompatUnauthorized(t, pollResponse)
	assertCompatUnauthorized(t, exchangeResponse)
	if quick.pollCalls != 1 || quick.exchangeCalls != 1 {
		t.Fatalf("unknown valid secret service calls=%d/%d", quick.pollCalls, quick.exchangeCalls)
	}
}

func minimalQuickConnectPollRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("X-Emby-Authorization", `MediaBrowser DeviceId="arvio-android-id"`)
	return request
}

func minimalQuickConnectExchangeRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateWithQuickConnect", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Emby-Authorization", `MediaBrowser DeviceId="arvio-android-id"`)
	return request
}

func mustJSONMarshal(t *testing.T, value string) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func arvioQuickConnectRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "ARVIO/2.4.0")
	request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="ARVIO", Device="Android", DeviceId="arvio-android-id", Version="2.4.0"`)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

var _ Authentication = (*quickConnectHTTPFake)(nil)
var _ QuickConnectAuthentication = (*quickConnectHTTPFake)(nil)
