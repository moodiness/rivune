package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
	"golang.org/x/net/websocket"
)

const (
	bootstrapProfileA = "a1000000-0000-4000-8000-000000000001"
	bootstrapProfileB = "b1000000-0000-4000-8000-000000000001"
)

type bootstrapAuthenticationFake struct {
	mu       sync.Mutex
	sessions map[string]AuthenticatedSession
	revoked  map[string]bool
}

func (*bootstrapAuthenticationFake) Login(context.Context, CompatLoginInput) (LoginResult, error) {
	return LoginResult{}, ErrInvalidCompatLogin
}

func (fake *bootstrapAuthenticationFake) Authenticate(_ context.Context, token string) (AuthenticatedSession, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	session, ok := fake.sessions[token]
	if !ok || fake.revoked[session.ID] {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return cloneAuthenticatedSession(session), nil
}

func (fake *bootstrapAuthenticationFake) Revalidate(_ context.Context, expected AuthenticatedSession) (AuthenticatedSession, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.revoked[expected.ID] {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	for _, session := range fake.sessions {
		if sameAuthenticatedSessionOwner(expected, session) {
			return cloneAuthenticatedSession(session), nil
		}
	}
	return AuthenticatedSession{}, ErrInvalidCompatCredential
}

func (fake *bootstrapAuthenticationFake) Logout(_ context.Context, session AuthenticatedSession) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.revoked[session.ID] = true
	return nil
}

type bootstrapCatalogFake struct{}

func (*bootstrapCatalogFake) GetCatalogTitle(context.Context, auth.Principal, string) (watchstate.CatalogTitle, error) {
	return watchstate.CatalogTitle{}, errors.New("not used")
}

func (*bootstrapCatalogFake) ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return watchstate.CatalogPage{}, errors.New("not used")
}

type bootstrapPlaybackFake struct{}

func (*bootstrapPlaybackFake) Sources(context.Context, auth.Principal, playback.SourcesInput) (playback.SourceList, error) {
	return playback.SourceList{}, errors.New("not used")
}

func (*bootstrapPlaybackFake) Open(context.Context, auth.Principal, playback.ResolveInput) (playback.Delivery, error) {
	return playback.Delivery{}, errors.New("not used")
}

func (*bootstrapPlaybackFake) Serve(http.ResponseWriter, *http.Request, playback.DeliveryHandle) error {
	return errors.New("not used")
}

func (*bootstrapPlaybackFake) ServeAsset(http.ResponseWriter, *http.Request, playback.DeliveryHandle, string) error {
	return errors.New("not used")
}

func (*bootstrapPlaybackFake) Close(context.Context, auth.Principal, playback.DeliveryHandle) error {
	return nil
}

func TestUsersAndSessionsExposeOnlyCurrentProfileDeviceWithHonestPolicy(t *testing.T) {
	handler, authentication, token, current := newBootstrapHTTPFixture(t, false)
	peer := bootstrapSession("a2000000-0000-4000-8000-000000000002", bootstrapProfileA, "device-a")
	foreignProfile := bootstrapSession("b2000000-0000-4000-8000-000000000002", bootstrapProfileB, "device-a")
	foreignDevice := bootstrapSession("a3000000-0000-4000-8000-000000000003", bootstrapProfileA, "device-b")
	handler.bootstrap.observe(peer)
	handler.bootstrap.observe(foreignProfile)
	handler.bootstrap.observe(foreignDevice)

	users := bootstrapRequest(t, handler, http.MethodGet, "/Users", "", token, "")
	if users.Code != http.StatusOK {
		t.Fatalf("users status = %d: %s", users.Code, users.Body.String())
	}
	var userList []UserDto
	decodeCompatTestResponse(t, users, &userList)
	if len(userList) != 1 || userList[0].Id != current.ProfileID || userList[0].Policy.EnableMediaPlayback ||
		userList[0].Policy.EnablePlaybackRemuxing || userList[0].Policy.EnableContentDownloading ||
		!userList[0].Policy.EnableRemoteAccess || userList[0].Policy.EnableRemoteControlOfOtherUsers || userList[0].Policy.EnableSharedDeviceControl ||
		userList[0].Configuration.AudioLanguagePreference != nil || userList[0].Configuration.SubtitleLanguagePreference != nil ||
		!userList[0].Configuration.HidePlayedInLatest || !userList[0].Configuration.RememberAudioSelections || !userList[0].Configuration.RememberSubtitleSelections {
		t.Fatalf("unexpected users response: %+v", userList)
	}

	sessions := bootstrapRequest(t, handler, http.MethodGet, "/Sessions", "", token, "")
	if sessions.Code != http.StatusOK {
		t.Fatalf("sessions status = %d: %s", sessions.Code, sessions.Body.String())
	}
	var sessionList []SessionInfoDto
	decodeCompatTestResponse(t, sessions, &sessionList)
	if len(sessionList) != 2 {
		t.Fatalf("visible sessions = %+v", sessionList)
	}
	for _, session := range sessionList {
		if session.UserId != bootstrapProfileA || session.DeviceId != "device-a" || session.SupportsRemoteControl || session.SupportsMediaControl {
			t.Fatalf("foreign or remotely controllable session leaked: %+v", session)
		}
	}

	handler.bootstrap.mu.Lock()
	handler.bootstrap.sessions[peer.ID].lastSeenAt = time.Now().UTC().Add(-2 * time.Minute)
	handler.bootstrap.mu.Unlock()
	recentResponse := bootstrapRequest(t, handler, http.MethodGet, "/Sessions?ActiveWithinSeconds=60", "", token, "")
	var recent []SessionInfoDto
	decodeCompatTestResponse(t, recentResponse, &recent)
	if recentResponse.Code != http.StatusOK || len(recent) != 1 || recent[0].Id != current.ID {
		t.Fatalf("active session filter status=%d sessions=%+v", recentResponse.Code, recent)
	}
	controllableResponse := bootstrapRequest(t, handler, http.MethodGet, "/Sessions?ControllableByUserId="+bootstrapProfileA, "", token, "")
	var controllable []SessionInfoDto
	decodeCompatTestResponse(t, controllableResponse, &controllable)
	if controllableResponse.Code != http.StatusOK || len(controllable) != 0 {
		t.Fatalf("remote-control filter status=%d sessions=%+v", controllableResponse.Code, controllable)
	}
	foreignDeviceQuery := bootstrapRequest(t, handler, http.MethodGet, "/Sessions?DeviceId=device-b", "", token, "")
	var empty []SessionInfoDto
	decodeCompatTestResponse(t, foreignDeviceQuery, &empty)
	if foreignDeviceQuery.Code != http.StatusOK || len(empty) != 0 {
		t.Fatalf("foreign device query status=%d sessions=%+v", foreignDeviceQuery.Code, empty)
	}
	anonymous := bootstrapRequest(t, handler, http.MethodGet, "/Sessions", "", "", "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous sessions status = %d", anonymous.Code)
	}
	_ = authentication
}

func TestCapabilitiesPreferencesLogsEncodingsAndLogoutLifecycleAreBounded(t *testing.T) {
	handler, _, token, current := newBootstrapHTTPFixture(t, true)
	direct := `{"Name":"Direct","DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}`
	directResponse := bootstrapRequest(t, handler, http.MethodPost, "/Sessions/Capabilities?PlayableMediaTypes=Video&SupportedCommands=Play&SupportsMediaControl=true", direct, token, "application/json")
	if directResponse.Code != http.StatusNoContent {
		t.Fatalf("direct capabilities status=%d body=%s", directResponse.Code, directResponse.Body.String())
	}
	profile, ok := handler.playSessions.deviceProfile(current)
	if !ok || profile.Name != "Direct" {
		t.Fatalf("direct device profile not stored: ok=%t profile=%+v", ok, profile)
	}
	wrapped := `{"PlayableMediaTypes":["Video"],"SupportedCommands":["DisplayContent"],"SupportsMediaControl":true,"SupportsPersistentIdentifier":true,"DeviceProfile":{"Name":"Wrapped","DirectPlayProfiles":[{"Container":"mkv","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`
	wrappedResponse := bootstrapRequest(t, handler, http.MethodPost, "/Sessions/Capabilities/Full", wrapped, token, "application/json")
	profile, ok = handler.playSessions.deviceProfile(current)
	if wrappedResponse.Code != http.StatusNoContent || !ok || profile.Name != "Wrapped" {
		t.Fatalf("wrapped capabilities status=%d ok=%t profile=%+v", wrappedResponse.Code, ok, profile)
	}
	sessionsResponse := bootstrapRequest(t, handler, http.MethodGet, "/Sessions", "", token, "")
	var reported []SessionInfoDto
	decodeCompatTestResponse(t, sessionsResponse, &reported)
	if sessionsResponse.Code != http.StatusOK || len(reported) != 1 || len(reported[0].Capabilities.SupportedCommands) != 1 ||
		reported[0].Capabilities.SupportedCommands[0] != "DisplayContent" || !reported[0].Capabilities.SupportsMediaControl ||
		!reported[0].SupportsMediaControl || reported[0].SupportsRemoteControl || reported[0].Capabilities.DeviceProfile == nil {
		t.Fatalf("stored client capabilities status=%d sessions=%+v", sessionsResponse.Code, reported)
	}

	preferenceBody := `{"Id":"home","ViewType":"Poster","CustomPrefs":{"density":"compact"},"ScrollDirection":"Vertical"}`
	preferenceUpdate := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/home?Client=tv", preferenceBody, token, "application/json")
	if preferenceUpdate.Code != http.StatusNoContent {
		t.Fatalf("preference update status=%d body=%s", preferenceUpdate.Code, preferenceUpdate.Body.String())
	}
	preferenceRead := bootstrapRequest(t, handler, http.MethodGet, "/DisplayPreferences/home?Client=tv", "", token, "")
	var preference DisplayPreferencesDto
	decodeCompatTestResponse(t, preferenceRead, &preference)
	if preferenceRead.Code != http.StatusOK || preference.CustomPrefs["density"] != "compact" || preference.Client != "tv" {
		t.Fatalf("stored preference status=%d value=%+v", preferenceRead.Code, preference)
	}
	handler.bootstrap.preferenceLimit = 1
	preferenceOverflow := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/other?Client=tv", `{"CustomPrefs":{}}`, token, "application/json")
	if preferenceOverflow.Code != http.StatusTooManyRequests {
		t.Fatalf("preference overflow status = %d", preferenceOverflow.Code)
	}

	validLog := bootstrapRequest(t, handler, http.MethodPost, "/ClientLog/Document", "client started", token, "text/plain")
	if validLog.Code != http.StatusNoContent {
		t.Fatalf("client log status = %d", validLog.Code)
	}
	largeLog := bootstrapRequest(t, handler, http.MethodPost, "/ClientLog/Document", strings.Repeat("x", maximumCompatClientLogBodyBytes+1), token, "text/plain")
	if largeLog.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large client log status = %d", largeLog.Code)
	}

	foreign := bootstrapSession("b2000000-0000-4000-8000-000000000002", bootstrapProfileB, "device-a")
	handler.playSessions.entries["current-encoding"] = &playSessionEntry{
		compatSessionID: current.ID, nativeSessionID: current.Principal.SessionID, profileID: current.ProfileID,
		deviceID: current.Client.DeviceID, principal: clonePrincipal(current.Principal), playSessionID: "current-encoding", sources: map[string]*playSessionSource{},
	}
	handler.playSessions.entries["foreign-encoding"] = &playSessionEntry{
		compatSessionID: foreign.ID, nativeSessionID: foreign.Principal.SessionID, profileID: foreign.ProfileID,
		deviceID: foreign.Client.DeviceID, principal: clonePrincipal(foreign.Principal), playSessionID: "foreign-encoding", sources: map[string]*playSessionSource{},
	}
	unclear := bootstrapRequest(t, handler, http.MethodDelete, "/Videos/ActiveEncodings", "", token, "")
	if unclear.Code != http.StatusBadRequest || handler.playSessions.entries["current-encoding"] == nil || handler.playSessions.entries["foreign-encoding"] == nil {
		t.Fatalf("unscoped active encoding request status=%d entries=%v", unclear.Code, handler.playSessions.entries)
	}
	encodings := bootstrapRequest(t, handler, http.MethodDelete, "/Videos/ActiveEncodings?deviceId=device-a&playSessionId=current-encoding", "", token, "")
	if encodings.Code != http.StatusNoContent || handler.playSessions.entries["current-encoding"] != nil || handler.playSessions.entries["foreign-encoding"] == nil {
		t.Fatalf("active encoding isolation status=%d entries=%v", encodings.Code, handler.playSessions.entries)
	}

	logout := bootstrapRequest(t, handler, http.MethodPost, "/Sessions/Logout", "", token, "")
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d: %s", logout.Code, logout.Body.String())
	}
	if len(handler.bootstrap.sessions) != 0 || len(handler.bootstrap.preferences) != 0 || len(handler.playSessions.deviceProfiles) != 0 {
		t.Fatalf("logout retained bootstrap state: sessions=%d preferences=%d profiles=%d", len(handler.bootstrap.sessions), len(handler.bootstrap.preferences), len(handler.playSessions.deviceProfiles))
	}
}

func TestSocketRequiresAuthenticationAndLogoutClosesBoundedConnection(t *testing.T) {
	handler, _, token, _ := newBootstrapHTTPFixture(t, false)
	anonymous := bootstrapRequest(t, handler, http.MethodGet, "/socket", "", "", "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous socket status = %d", anonymous.Code)
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	handler.bootstrap.socketSessionLimit = 1
	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/socket?api_key=" + token + "&DeviceId=device-a"
	connection, err := websocket.Dial(socketURL, "", server.URL)
	if err != nil {
		t.Fatalf("open authenticated compatibility socket: %v", err)
	}
	defer connection.Close()
	var keepalive WebSocketMessageDto
	if err := websocket.JSON.Receive(connection, &keepalive); err != nil || keepalive.MessageType != "ForceKeepAlive" || keepalive.Data != float64(compatSocketLostTimeoutSeconds) {
		t.Fatalf("initial socket keepalive = %+v, err=%v", keepalive, err)
	}
	if bootstrapSocketCount(handler.bootstrap) != 1 {
		t.Fatalf("registered sockets = %d", bootstrapSocketCount(handler.bootstrap))
	}
	if second, secondErr := websocket.Dial(socketURL, "", server.URL); secondErr == nil {
		_ = second.Close()
		t.Fatal("second socket exceeded the per-session limit")
	}

	logoutRequest, err := http.NewRequest(http.MethodPost, server.URL+"/Sessions/Logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	logoutRequest.Header.Set("X-Emby-Token", token)
	logoutResponse, err := server.Client().Do(logoutRequest)
	if err != nil {
		t.Fatalf("logout with active socket: %v", err)
	}
	_ = logoutResponse.Body.Close()
	if logoutResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutResponse.StatusCode)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	var message json.RawMessage
	if err := websocket.JSON.Receive(connection, &message); err == nil {
		t.Fatalf("socket remained readable after logout: %s", message)
	}
	if bootstrapSocketCount(handler.bootstrap) != 0 {
		t.Fatalf("logout retained sockets = %d", bootstrapSocketCount(handler.bootstrap))
	}
}

func TestGroupingOptionsAndPreferenceCrossProfileAuthorization(t *testing.T) {
	handler, _, token, _ := newBootstrapHTTPFixture(t, false)
	grouping := bootstrapRequest(t, handler, http.MethodGet, "/UserViews/GroupingOptions?UserId="+bootstrapProfileA, "", token, "")
	var options []SpecialViewOptionDto
	decodeCompatTestResponse(t, grouping, &options)
	if grouping.Code != http.StatusOK || options == nil || len(options) != 0 {
		t.Fatalf("grouping options status=%d options=%+v", grouping.Code, options)
	}
	crossProfile := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/home?UserId="+bootstrapProfileB+"&Client=tv", `{"CustomPrefs":{}}`, token, "application/json")
	if crossProfile.Code != http.StatusNotFound || len(handler.bootstrap.preferences) != 0 {
		t.Fatalf("cross-profile preference status=%d preferences=%d", crossProfile.Code, len(handler.bootstrap.preferences))
	}
}

func newBootstrapHTTPFixture(t *testing.T, playbackEnabled bool) (*Handler, *bootstrapAuthenticationFake, string, AuthenticatedSession) {
	t.Helper()
	serverID, err := ParseServerID("a0000000-0000-4000-8000-000000000000")
	token := compatTestToken(17)
	current := bootstrapSession("a2000000-0000-4000-8000-000000000001", bootstrapProfileA, "device-a")
	authentication := &bootstrapAuthenticationFake{sessions: map[string]AuthenticatedSession{token: current}, revoked: make(map[string]bool)}
	dependencies := Dependencies{ServerInfo: ServerInfo{ID: serverID, Name: "Rivune"}, Authentication: authentication}
	if playbackEnabled {
		dependencies.Catalog = &bootstrapCatalogFake{}
		dependencies.Playback = &bootstrapPlaybackFake{}
	}
	handler, err := New(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	return handler, authentication, token, current
}

func bootstrapSession(id, profileID, deviceID string) AuthenticatedSession {
	activeProfileID := profileID
	return AuthenticatedSession{
		ID: id, ProfileID: profileID, ProfileName: "Profile " + profileID[:1], ExpiresAt: time.Now().UTC().Add(time.Hour),
		Client:    ClientIdentity{Client: "Compatibility Client", Device: "Living Room", DeviceID: deviceID, Version: "1.0"},
		Principal: auth.Principal{SessionID: "native-" + id, UserID: "owner-a", DeviceID: deviceID, ActiveProfileID: &activeProfileID},
	}
}

func bootstrapRequest(t *testing.T, handler http.Handler, method, target, body, token, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		request.Header.Set("X-Emby-Token", token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func bootstrapSocketCount(registry *bootstrapRegistry) int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return len(registry.sockets)
}
