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

type bootstrapCatalogFake struct {
	items map[string]watchstate.CatalogTitle
}

func (catalog *bootstrapCatalogFake) GetCatalogTitle(_ context.Context, _ auth.Principal, itemID string) (watchstate.CatalogTitle, error) {
	item, ok := catalog.items[itemID]
	if !ok {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	return item, nil
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

type bootstrapDisplayPreferencesFake struct {
	values      map[string]DisplayPreferencesDto
	limit       int
	getCalls    int
	updateCalls int
}

func (fake *bootstrapDisplayPreferencesFake) Get(_ context.Context, session AuthenticatedSession, client, id string) (DisplayPreferencesDto, bool, error) {
	fake.getCalls++
	value, ok := fake.values[bootstrapDisplayPreferenceKey(session, client, id)]
	return value, ok, nil
}

func (fake *bootstrapDisplayPreferencesFake) Update(_ context.Context, session AuthenticatedSession, client, id string, value DisplayPreferencesDto) error {
	fake.updateCalls++
	key := bootstrapDisplayPreferenceKey(session, client, id)
	if _, exists := fake.values[key]; !exists && len(fake.values) >= fake.limit {
		return ErrDisplayPreferenceLimit
	}
	value.Id, value.Client = id, client
	if value.CustomPrefs == nil {
		value.CustomPrefs = map[string]string{}
	}
	fake.values[key] = value
	return nil
}

func bootstrapDisplayPreferenceKey(session AuthenticatedSession, client, id string) string {
	return session.Principal.UserID + "\x00" + session.ProfileID + "\x00" + client + "\x00" + id
}

func bootstrapDisplayPreferenceService(handler *Handler) *bootstrapDisplayPreferencesFake {
	return handler.displayPreferences.(*bootstrapDisplayPreferencesFake)
}

func TestUsersAndSessionsExposeOnlyCurrentProfileAcrossAuthorizedDevices(t *testing.T) {
	handler, authentication, token, current := newBootstrapHTTPFixture(t, false)
	peer := bootstrapSession("a2000000-0000-4000-8000-000000000002", bootstrapProfileA, "device-a")
	foreignProfile := bootstrapSession("b2000000-0000-4000-8000-000000000002", bootstrapProfileB, "device-a")
	foreignDevice := bootstrapSession("a3000000-0000-4000-8000-000000000003", bootstrapProfileA, "device-b")
	foreignAccount := bootstrapSession("a4000000-0000-4000-8000-000000000004", bootstrapProfileA, "device-c")
	foreignAccount.Principal.UserID = "owner-b"
	handler.bootstrap.observe(peer)
	handler.bootstrap.observe(foreignProfile)
	handler.bootstrap.observe(foreignDevice)
	handler.bootstrap.observe(foreignAccount)

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
	if len(sessionList) != 3 {
		t.Fatalf("visible sessions = %+v", sessionList)
	}
	for _, session := range sessionList {
		if session.UserId != bootstrapProfileA || session.SupportsRemoteControl || session.SupportsMediaControl {
			t.Fatalf("foreign or remotely controllable session leaked: %+v", session)
		}
	}

	handler.bootstrap.mu.Lock()
	handler.bootstrap.sessions[peer.ID].lastSeenAt = time.Now().UTC().Add(-2 * time.Minute)
	handler.bootstrap.mu.Unlock()
	recentResponse := bootstrapRequest(t, handler, http.MethodGet, "/Sessions?ActiveWithinSeconds=60", "", token, "")
	var recent []SessionInfoDto
	decodeCompatTestResponse(t, recentResponse, &recent)
	if recentResponse.Code != http.StatusOK || len(recent) != 2 || recent[0].Id != current.ID || recent[1].Id != foreignDevice.ID {
		t.Fatalf("active session filter status=%d sessions=%+v", recentResponse.Code, recent)
	}
	controllableResponse := bootstrapRequest(t, handler, http.MethodGet, "/Sessions?ControllableByUserId="+bootstrapProfileA, "", token, "")
	var controllable []SessionInfoDto
	decodeCompatTestResponse(t, controllableResponse, &controllable)
	if controllableResponse.Code != http.StatusOK || len(controllable) != 0 {
		t.Fatalf("remote-control filter status=%d sessions=%+v", controllableResponse.Code, controllable)
	}
	foreignDeviceQuery := bootstrapRequest(t, handler, http.MethodGet, "/Sessions?DeviceId=device-b", "", token, "")
	var deviceSessions []SessionInfoDto
	decodeCompatTestResponse(t, foreignDeviceQuery, &deviceSessions)
	if foreignDeviceQuery.Code != http.StatusOK || len(deviceSessions) != 1 || deviceSessions[0].Id != foreignDevice.ID {
		t.Fatalf("device filter status=%d sessions=%+v", foreignDeviceQuery.Code, deviceSessions)
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
	wrapped := `{"PlayableMediaTypes":["Video"],"SupportedCommands":["DisplayContent"],"SupportsMediaControl":true,"SupportsPersistentIdentifier":true,"DeviceProfile":{"Name":"Wrapped","ContainerProfiles":[{"Type":"Video","Container":"mkv","Conditions":[{"Condition":"LessThanEqual","Property":"Height","Value":"1080","IsRequired":true}]}],"DirectPlayProfiles":[{"Container":"mkv","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`
	wrappedResponse := bootstrapRequest(t, handler, http.MethodPost, "/Sessions/Capabilities/Full", wrapped, token, "application/json")
	profile, ok = handler.playSessions.deviceProfile(current)
	if wrappedResponse.Code != http.StatusNoContent || !ok || profile.Name != "Wrapped" || len(profile.ContainerProfiles) != 1 {
		t.Fatalf("wrapped capabilities status=%d ok=%t profile=%+v", wrappedResponse.Code, ok, profile)
	}
	malformed := `{"DeviceProfile":{"Name":"Malformed","ContainerProfiles":[{"Type":"Video","Container":"mkv","Conditions":[{"Condition":"LessThanEqual","Property":"Height","Value":"unbounded"}]}]}}`
	malformedResponse := bootstrapRequest(t, handler, http.MethodPost, "/Sessions/Capabilities/Full", malformed, token, "application/json")
	profile, ok = handler.playSessions.deviceProfile(current)
	if malformedResponse.Code != http.StatusBadRequest || !ok || profile.Name != "Wrapped" || len(profile.ContainerProfiles) != 1 {
		t.Fatalf("malformed profile status=%d mutated stored profile: ok=%t profile=%+v body=%s", malformedResponse.Code, ok, profile, malformedResponse.Body.String())
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
	bootstrapDisplayPreferenceService(handler).limit = 1
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
	preferences := bootstrapDisplayPreferenceService(handler)
	if len(handler.bootstrap.sessions) != 0 || len(preferences.values) != 1 || len(handler.playSessions.deviceProfiles) != 0 {
		t.Fatalf("logout state: sessions=%d durablePreferences=%d profiles=%d", len(handler.bootstrap.sessions), len(preferences.values), len(handler.playSessions.deviceProfiles))
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
	if err := websocket.JSON.Receive(connection, &keepalive); err != nil || keepalive.MessageType != "ForceKeepAlive" || keepalive.Data != float64(compatSocketLostTimeoutSeconds) || !validCompatUUID(keepalive.MessageId) {
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
func TestViewingReportProjectsAuthorizedItemAndRejectsForeignSession(t *testing.T) {
	handler, _, token, current := newBootstrapHTTPFixture(t, true)
	itemID := "c1000000-0000-4000-8000-000000000001"
	handler.catalog.(*bootstrapCatalogFake).items[itemID] = watchstate.CatalogTitle{ID: itemID, MediaType: "movie", Title: "Viewing"}

	report := bootstrapRequest(t, handler, http.MethodPost, "/Sessions/Viewing?ItemId="+itemID, "", token, "")
	if report.Code != http.StatusNoContent {
		t.Fatalf("viewing status=%d body=%s", report.Code, report.Body.String())
	}
	sessions := bootstrapRequest(t, handler, http.MethodGet, "/Sessions", "", token, "")
	var values []SessionInfoDto
	decodeCompatTestResponse(t, sessions, &values)
	if sessions.Code != http.StatusOK || len(values) != 1 || values[0].NowViewingItem == nil || values[0].NowViewingItem.Id != itemID || values[0].NowViewingItem.Path != "" || len(values[0].NowViewingItem.MediaSources) != 0 {
		t.Fatalf("viewing session status=%d values=%+v", sessions.Code, values)
	}
	foreignSession := bootstrapRequest(t, handler, http.MethodPost, "/Sessions/Viewing?ItemId="+itemID+"&SessionId=a2000000-0000-4000-8000-000000000099", "", token, "")
	if foreignSession.Code != http.StatusNotFound {
		t.Fatalf("foreign viewing session status=%d body=%s", foreignSession.Code, foreignSession.Body.String())
	}
	unknown := bootstrapRequest(t, handler, http.MethodPost, "/Sessions/Viewing?ItemId=c1000000-0000-4000-8000-000000000099&SessionId="+current.ID, "", token, "")
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown viewing item status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func TestSocketSubscriptionsReconnectMalformedAndOversizedFramesFailClosed(t *testing.T) {
	handler, _, token, _ := newBootstrapHTTPFixture(t, false)
	handler.bootstrap.socketSessionLimit = 1
	server := httptest.NewServer(handler)
	defer server.Close()
	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/socket?ApiKey=" + token + "&deviceId=device-a"

	connection, err := websocket.Dial(socketURL, "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var message WebSocketMessageDto
	if err := websocket.JSON.Receive(connection, &message); err != nil || message.MessageType != "ForceKeepAlive" || !validCompatUUID(message.MessageId) {
		t.Fatalf("initial message=%+v err=%v", message, err)
	}
	if err := websocket.JSON.Send(connection, WebSocketMessageDto{MessageType: "KeepAlive"}); err != nil {
		t.Fatal(err)
	}
	message = WebSocketMessageDto{}
	if err := websocket.JSON.Receive(connection, &message); err != nil || message.MessageType != "KeepAlive" || !validCompatUUID(message.MessageId) {
		t.Fatalf("keepalive response=%+v err=%v", message, err)
	}
	if err := websocket.JSON.Send(connection, WebSocketMessageDto{MessageType: "SessionsStart", Data: "0,1000"}); err != nil {
		t.Fatal(err)
	}
	message = WebSocketMessageDto{}
	if err := websocket.JSON.Receive(connection, &message); err != nil || message.MessageType != "Sessions" || !validCompatUUID(message.MessageId) {
		t.Fatalf("sessions message=%+v err=%v", message, err)
	}
	_ = connection.Close()
	waitBootstrapSocketCount(t, handler.bootstrap, 0)

	malformed, err := websocket.Dial(socketURL, "", server.URL)
	if err != nil {
		t.Fatalf("reconnect socket: %v", err)
	}
	if err := websocket.JSON.Receive(malformed, &message); err != nil {
		t.Fatal(err)
	}
	if err := websocket.Message.Send(malformed, []byte("{")); err != nil {
		t.Fatal(err)
	}
	_ = malformed.SetReadDeadline(time.Now().Add(time.Second))
	if err := websocket.JSON.Receive(malformed, &message); err == nil {
		t.Fatal("malformed websocket frame remained connected")
	}
	_ = malformed.Close()
	waitBootstrapSocketCount(t, handler.bootstrap, 0)

	oversized, err := websocket.Dial(socketURL, "", server.URL)
	if err != nil {
		t.Fatalf("reconnect oversized socket: %v", err)
	}
	if err := websocket.JSON.Receive(oversized, &message); err != nil {
		t.Fatal(err)
	}
	if err := websocket.Message.Send(oversized, make([]byte, maximumCompatSocketMessageBytes+1)); err != nil {
		t.Fatal(err)
	}
	_ = oversized.SetReadDeadline(time.Now().Add(time.Second))
	if err := websocket.JSON.Receive(oversized, &message); err == nil {
		t.Fatal("oversized websocket frame remained connected")
	}
	_ = oversized.Close()
}

func TestSocketInputDecoderAcceptsOnlyRequiredBoundedMessageTypes(t *testing.T) {
	for _, payload := range []string{
		`{"MessageType":"KeepAlive"}`,
		`{"MessageType":"SessionsStart","Data":"0,1000"}`,
		`{"MessageType":"SessionsStop"}`,
	} {
		if _, err := decodeCompatSocketInput([]byte(payload)); err != nil {
			t.Errorf("valid payload %s: %v", payload, err)
		}
	}
	for _, payload := range []string{
		`{"MessageType":"LibraryChangedStart"}`,
		`{"MessageType":"KeepAlive","Data":1}`,
		`{"MessageType":"SessionsStop","Data":"0,1000"}`,
		`{"MessageType":"SessionsStart","Data":"0,999"}`,
		`{"MessageType":"SessionsStart","Data":"0,60001"}`,
		`{"MessageType":"SessionsStart","Data":"9223372036854775807,9223372036854775807"}`,
		`{"MessageType":"KeepAlive","Unexpected":true}`,
		`{"MessageType":"KeepAlive"}{"MessageType":"KeepAlive"}`,
	} {
		if _, err := decodeCompatSocketInput([]byte(payload)); err == nil {
			t.Errorf("invalid payload accepted: %s", payload)
		}
	}
}

func TestLogoutClosesOwnerSocketAndPublishesRemainingProfileSessions(t *testing.T) {
	handler, _, token, current := newBootstrapHTTPFixture(t, false)
	peer := bootstrapSession("a3000000-0000-4000-8000-000000000003", bootstrapProfileA, "device-b")
	handler.bootstrap.observe(peer)
	ownerLease, ownerOK := handler.bootstrap.acquireSocket(current)
	peerLease, peerOK := handler.bootstrap.acquireSocket(peer)
	if !ownerOK || !peerOK || !handler.bootstrap.subscribeSessions(peerLease, true) {
		t.Fatal("failed to prepare logout sockets")
	}
	logout := bootstrapRequest(t, handler, http.MethodPost, "/Sessions/Logout", "", token, "")
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}
	select {
	case <-ownerLease.closed:
	default:
		t.Fatal("logout did not close owner socket")
	}
	event := receiveCompatSocketEvent(t, peerLease)
	sessions, ok := event.Data.([]SessionInfoDto)
	if event.MessageType != "Sessions" || !validCompatUUID(event.MessageId) || !ok || len(sessions) != 1 || sessions[0].Id != peer.ID {
		t.Fatalf("logout session event=%+v", event)
	}
	handler.bootstrap.releaseSocket(peerLease)
}

func TestSocketBrokerIsProfileScopedAndDisconnectsBackpressuredConsumer(t *testing.T) {
	registry := newBootstrapRegistry()
	owner := bootstrapSession("a2000000-0000-4000-8000-000000000001", bootstrapProfileA, "device-a")
	foreign := bootstrapSession("b2000000-0000-4000-8000-000000000001", bootstrapProfileB, "device-a")
	ownerLease, ownerOK := registry.acquireSocket(owner)
	foreignLease, foreignOK := registry.acquireSocket(foreign)
	if !ownerOK || !foreignOK {
		t.Fatal("failed to acquire profile-scoped sockets")
	}
	event := WebSocketMessageDto{MessageType: "UserDataChanged", Data: UserDataChangeInfo{UserId: owner.ProfileID, UserDataList: []UserItemDataDto{{ItemId: "item"}}}}
	registry.publish(owner, event, false)
	if received := receiveCompatSocketEvent(t, ownerLease); received.MessageType != "UserDataChanged" {
		t.Fatalf("owner event=%+v", received)
	}
	select {
	case leaked := <-foreignLease.outbound:
		t.Fatalf("cross-profile event leaked: %+v", leaked)
	default:
	}
	for len(ownerLease.outbound) < cap(ownerLease.outbound) {
		ownerLease.outbound <- event
	}
	registry.publish(owner, event, false)
	select {
	case <-ownerLease.closed:
	default:
		t.Fatal("backpressured socket was not disconnected")
	}
	registry.releaseSocket(foreignLease)
}

func waitBootstrapSocketCount(t *testing.T, registry *bootstrapRegistry, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if bootstrapSocketCount(registry) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("socket count=%d, want %d", bootstrapSocketCount(registry), want)
}

func TestGroupingOptionsAndPreferenceCrossProfileAuthorization(t *testing.T) {
	handler, _, token, _ := newBootstrapHTTPFixture(t, false)
	grouping := bootstrapRequest(t, handler, http.MethodGet, "/UserViews/GroupingOptions?UserId="+bootstrapProfileA, "", token, "")
	var options []SpecialViewOptionDto
	decodeCompatTestResponse(t, grouping, &options)
	if grouping.Code != http.StatusOK || options == nil || len(options) != 0 {
		t.Fatalf("grouping options status=%d options=%+v", grouping.Code, options)
	}

	preferences := bootstrapDisplayPreferenceService(handler)
	withoutUser := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/home?Client=tv", `{"CustomPrefs":{"layout":"poster"}}`, token, "application/json")
	if withoutUser.Code != http.StatusNoContent || preferences.updateCalls != 1 {
		t.Fatalf("preference without UserId status=%d updates=%d body=%s", withoutUser.Code, preferences.updateCalls, withoutUser.Body.String())
	}
	boundUser := bootstrapRequest(t, handler, http.MethodGet, "/DisplayPreferences/home?uSeRiD="+strings.ToUpper(strings.ReplaceAll(bootstrapProfileA, "-", ""))+"&Client=tv", "", token, "")
	var stored DisplayPreferencesDto
	decodeCompatTestResponse(t, boundUser, &stored)
	if boundUser.Code != http.StatusOK || preferences.getCalls != 1 || stored.CustomPrefs["layout"] != "poster" {
		t.Fatalf("bound preference status=%d gets=%d value=%+v", boundUser.Code, preferences.getCalls, stored)
	}

	getCalls, updateCalls := preferences.getCalls, preferences.updateCalls
	foreignRead := bootstrapRequest(t, handler, http.MethodGet, "/DisplayPreferences/home?UserId="+bootstrapProfileB+"&Client=tv", "", token, "")
	if foreignRead.Code != http.StatusNotFound || preferences.getCalls != getCalls {
		t.Fatalf("cross-profile read status=%d gets=%d body=%s", foreignRead.Code, preferences.getCalls, foreignRead.Body.String())
	}
	foreignUpdate := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/home?UserId="+bootstrapProfileB+"&Client=tv", `{"CustomPrefs":{"layout":"foreign"}}`, token, "application/json")
	if foreignUpdate.Code != http.StatusNotFound || preferences.updateCalls != updateCalls {
		t.Fatalf("cross-profile update status=%d updates=%d body=%s", foreignUpdate.Code, preferences.updateCalls, foreignUpdate.Body.String())
	}
	duplicateRead := bootstrapRequest(t, handler, http.MethodGet, "/DisplayPreferences/home?UserId="+bootstrapProfileA+"&userid="+bootstrapProfileA+"&Client=tv", "", token, "")
	if duplicateRead.Code != http.StatusBadRequest || preferences.getCalls != getCalls {
		t.Fatalf("duplicate UserId read status=%d gets=%d body=%s", duplicateRead.Code, preferences.getCalls, duplicateRead.Body.String())
	}
	duplicateUpdate := bootstrapRequest(t, handler, http.MethodPost, "/DisplayPreferences/home?UserId="+bootstrapProfileA+"&userid="+bootstrapProfileA+"&Client=tv", `{"CustomPrefs":{}}`, token, "application/json")
	if duplicateUpdate.Code != http.StatusBadRequest || preferences.updateCalls != updateCalls {
		t.Fatalf("duplicate UserId update status=%d updates=%d body=%s", duplicateUpdate.Code, preferences.updateCalls, duplicateUpdate.Body.String())
	}
}

func newBootstrapHTTPFixture(t *testing.T, playbackEnabled bool) (*Handler, *bootstrapAuthenticationFake, string, AuthenticatedSession) {
	t.Helper()
	serverID, err := ParseServerID("a0000000-0000-4000-8000-000000000000")
	token := compatTestToken(17)
	current := bootstrapSession("a2000000-0000-4000-8000-000000000001", bootstrapProfileA, "device-a")
	authentication := &bootstrapAuthenticationFake{sessions: map[string]AuthenticatedSession{token: current}, revoked: make(map[string]bool)}
	displayPreferences := &bootstrapDisplayPreferencesFake{values: make(map[string]DisplayPreferencesDto), limit: maximumDisplayPreferencesPerScope}
	dependencies := Dependencies{ServerInfo: ServerInfo{ID: serverID, Name: "Rivune"}, Authentication: authentication, DisplayPreferences: displayPreferences}
	if playbackEnabled {
		dependencies.Catalog = &bootstrapCatalogFake{items: make(map[string]watchstate.CatalogTitle)}
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

func receiveCompatSocketEvent(t *testing.T, lease *compatSocketLease) WebSocketMessageDto {
	t.Helper()
	select {
	case event := <-lease.outbound:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for compatibility socket event")
		return WebSocketMessageDto{}
	}
}
