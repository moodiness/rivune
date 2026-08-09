package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const (
	maximumCompatClientLogBodyBytes = 64 << 10
	maximumCompatSocketMessageBytes = 4 << 10
	maximumCompatSocketMessages     = 1024
	maximumCompatSocketLifetime     = 2 * time.Hour
	compatSocketKeepalivePeriod     = 45 * time.Second
	compatSocketLostTimeoutSeconds  = 60
	compatSocketRevalidatePeriod    = time.Minute
	compatSocketRevalidateTimeout   = 5 * time.Second
	maximumDisplayPreferenceFields  = 64
	maximumDisplayPreferenceValue   = 1024
)

func (handler *Handler) handleUsers(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, []UserDto{handler.userForSession(request.Context(), session)})
}

func (handler *Handler) handleSessions(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The session query is invalid")
		return
	}
	deviceID, deviceFound, deviceErr := queryScalar(request.URL.Query(), "DeviceId")
	activeWithinSeconds, activeErr := boundedInteger(request.URL.Query(), "ActiveWithinSeconds", 1, 2_147_483_647, 0)
	controllableBy, controllableFound, controllableErr := queryScalar(request.URL.Query(), "ControllableByUserId")
	if deviceErr != nil || activeErr != nil || controllableErr != nil || deviceFound && !boundedUTF8(strings.TrimSpace(deviceID), 1, 128) ||
		controllableFound && !validCompatUUID(strings.TrimSpace(controllableBy)) {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The session query is invalid")
		return
	}
	if controllableFound {
		writeJSON(response, http.StatusOK, []SessionInfoDto{})
		return
	}
	visible := []AuthenticatedSession{session}
	if handler.bootstrap != nil {
		visible = handler.bootstrap.visibleSessions(session, strings.TrimSpace(deviceID), time.Duration(activeWithinSeconds)*time.Second)
	}
	result := make([]SessionInfoDto, 0, len(visible))
	for _, candidate := range visible {
		result = append(result, handler.sessionInfo(request.Context(), candidate))
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *Handler) handleActiveEncodings(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The active encoding query is invalid")
		return
	}
	deviceID, deviceFound, deviceErr := queryScalar(request.URL.Query(), "DeviceId")
	playSessionID, playFound, playErr := queryScalar(request.URL.Query(), "PlaySessionId")
	if deviceErr != nil || playErr != nil || !deviceFound || !playFound || !boundedUTF8(strings.TrimSpace(deviceID), 1, 128) ||
		!boundedUTF8(strings.TrimSpace(playSessionID), 1, 128) {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The active encoding query is invalid")
		return
	}
	if strings.TrimSpace(deviceID) != session.Client.DeviceID {
		writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The requested session was not found")
		return
	}
	if handler.playSessions != nil {
		handler.playSessions.closeEncoding(request.Context(), session, strings.TrimSpace(playSessionID))
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) handleClientLog(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	if request.Body == nil || request.ContentLength > maximumCompatClientLogBodyBytes {
		writeCompatError(response, http.StatusRequestEntityTooLarge, "InvalidRequest", "The client log is too large")
		return
	}
	limited := io.LimitReader(request.Body, maximumCompatClientLogBodyBytes+1)
	var buffer [8 << 10]byte
	bytesRead, err := io.CopyBuffer(io.Discard, limited, buffer[:])
	if err != nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The client log could not be read")
		return
	}
	if bytesRead > maximumCompatClientLogBodyBytes {
		writeCompatError(response, http.StatusRequestEntityTooLarge, "InvalidRequest", "The client log is too large")
		return
	}
	if handler.logger != nil {
		handler.logger.LogAttrs(request.Context(), slog.LevelDebug, "jellyfin compatibility client log received",
			slog.String("profile_id", session.ProfileID), slog.String("device_id", session.Client.DeviceID), slog.Int64("bytes", bytesRead))
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) handleSocket(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.authenticateRequest(response, request, true)
	if !ok {
		return
	}
	if !isCompatWebSocketUpgrade(request) {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "A WebSocket upgrade is required")
		return
	}
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The socket query is invalid")
		return
	}
	deviceID, found, err := queryScalar(request.URL.Query(), "DeviceId")
	if err != nil || found && strings.TrimSpace(deviceID) != session.Client.DeviceID {
		writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The requested session was not found")
		return
	}
	if _, ok := response.(http.Hijacker); !ok {
		writeCompatError(response, http.StatusInternalServerError, "InternalError", "The WebSocket upgrade is unavailable")
		return
	}
	if handler.bootstrap == nil {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility socket is unavailable")
		return
	}
	lease, acquired := handler.bootstrap.acquireSocket(session)
	if !acquired {
		writeCompatError(response, http.StatusTooManyRequests, "ResourceLimitExceeded", "The compatibility socket limit has been reached")
		return
	}
	defer handler.bootstrap.releaseSocket(lease)
	requestContext := request.Context()
	server := websocket.Server{
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: func(connection *websocket.Conn) {
			handler.serveCompatSocket(requestContext, connection, session, lease)
		},
	}
	server.ServeHTTP(response, request)
}

func (handler *Handler) serveCompatSocket(ctx context.Context, connection *websocket.Conn, session AuthenticatedSession, lease *compatSocketLease) {
	handler.serveRealtimeSocket(ctx, connection, session, lease)
}

func (handler *Handler) handleDisplayPreferencesUpdate(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	preferenceID, client, ok := displayPreferenceSelector(response, request, session)
	if !ok {
		return
	}
	var input DisplayPreferencesDto
	if err := decodeCompatJSON(response, request, &input); err != nil || !validDisplayPreferences(input, preferenceID, client) {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The display preferences are invalid")
		return
	}
	if handler.displayPreferences == nil {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "Display preferences are unavailable")
		return
	}
	if err := handler.displayPreferences.Update(request.Context(), session, client, preferenceID, input); err != nil {
		switch {
		case errors.Is(err, ErrInvalidDisplayPreferences):
			writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The display preferences are invalid")
		case errors.Is(err, ErrDisplayPreferenceLimit):
			writeCompatError(response, http.StatusTooManyRequests, "ResourceLimitExceeded", "The display preference limit has been reached")
		default:
			writeCompatError(response, http.StatusInternalServerError, "InternalError", "The display preferences could not be stored")
		}
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) handleGroupingOptions(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	writeJSON(response, http.StatusOK, []SpecialViewOptionDto{})
}

func displayPreferenceSelector(response http.ResponseWriter, request *http.Request, session AuthenticatedSession) (string, string, bool) {
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The display preference query is invalid")
		return "", "", false
	}
	userID, userFound, userErr := queryScalar(request.URL.Query(), "UserId")
	if userErr != nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The display preference query is invalid")
		return "", "", false
	}
	if userFound && !sameCompatUUID(userID, session.ProfileID) {
		writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The requested resource was not found")
		return "", "", false
	}
	preferenceID := strings.TrimSpace(request.PathValue("displayPreferencesId"))
	if !validDisplayPreferenceID(preferenceID) {
		writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The requested resource was not found")
		return "", "", false
	}
	client, found, err := queryScalar(request.URL.Query(), "Client")
	if err != nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The display preference query is invalid")
		return "", "", false
	}
	client = strings.TrimSpace(client)
	if !found || client == "" {
		client = strings.TrimSpace(session.Client.Client)
	}
	if client == "" {
		client = "jellyfin"
	}
	if !boundedUTF8(client, 1, 64) {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The display preference query is invalid")
		return "", "", false
	}
	return preferenceID, client, true
}

func validDisplayPreferences(value DisplayPreferencesDto, expectedID, expectedClient string) bool {
	if value.Id != "" && (value.Id != strings.TrimSpace(value.Id) || !strings.EqualFold(value.Id, expectedID)) ||
		value.Client != "" && (value.Client != strings.TrimSpace(value.Client) || !strings.EqualFold(value.Client, expectedClient)) ||
		len(value.CustomPrefs) > maximumDisplayPreferenceFields {
		return false
	}
	for _, field := range []string{value.ViewType, value.SortBy, value.IndexBy, value.ScrollDirection, value.SortOrder} {
		if !boundedUTF8(field, 0, 128) {
			return false
		}
	}
	if !boundedUTF8(value.Client, 0, 64) {
		return false
	}
	for key, preference := range value.CustomPrefs {
		if !boundedUTF8(key, 1, 64) || !boundedUTF8(preference, 0, maximumDisplayPreferenceValue) {
			return false
		}
	}
	return value.PrimaryImageHeight >= 0 && value.PrimaryImageWidth >= 0 && value.PrimaryImageHeight <= 8192 && value.PrimaryImageWidth <= 8192
}

func decodeCapabilitiesReport(response http.ResponseWriter, request *http.Request) (ClientCapabilitiesDto, bool, bool, bool) {
	if request.Body == nil || request.Body == http.NoBody {
		return ClientCapabilitiesDto{}, false, false, true
	}
	if request.ContentLength > maximumCompatCapabilitiesBodyBytes {
		writeCompatError(response, http.StatusRequestEntityTooLarge, "InvalidRequest", "The request body is too large")
		return ClientCapabilitiesDto{}, false, false, false
	}
	payload, err := io.ReadAll(io.LimitReader(request.Body, maximumCompatCapabilitiesBodyBytes+1))
	if err != nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The request body is invalid")
		return ClientCapabilitiesDto{}, false, false, false
	}
	if len(payload) > maximumCompatCapabilitiesBodyBytes {
		writeCompatError(response, http.StatusRequestEntityTooLarge, "InvalidRequest", "The request body is too large")
		return ClientCapabilitiesDto{}, false, false, false
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return ClientCapabilitiesDto{}, false, false, true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(payload, &object) != nil || object == nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The request body is invalid")
		return ClientCapabilitiesDto{}, false, false, false
	}
	wrapper := false
	for _, name := range []string{"DeviceProfile", "PlayableMediaTypes", "SupportedCommands", "SupportsMediaControl", "SupportsPersistentIdentifier"} {
		if _, wrapper = object[name]; wrapper {
			break
		}
	}
	if wrapper {
		var capabilities ClientCapabilitiesDto
		if json.Unmarshal(payload, &capabilities) != nil || !validClientCapabilities(capabilities) {
			writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The client capabilities are invalid")
			return ClientCapabilitiesDto{}, false, false, false
		}
		return capabilities, true, true, true
	}
	direct := false
	for _, name := range []string{"Name", "MaxStreamingBitrate", "DirectPlayProfiles", "TranscodingProfiles", "SubtitleProfiles"} {
		if _, direct = object[name]; direct {
			break
		}
	}
	if !direct {
		return ClientCapabilitiesDto{}, false, false, true
	}
	var profile DeviceProfile
	if json.Unmarshal(payload, &profile) != nil || !validDeviceProfileBounds(profile) {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The device profile is invalid")
		return ClientCapabilitiesDto{}, false, false, false
	}
	return ClientCapabilitiesDto{DeviceProfile: &profile}, false, true, true
}

func (handler *Handler) storeCapabilitiesReport(session AuthenticatedSession, capabilities ClientCapabilitiesDto, replace, present bool) {
	if !present {
		return
	}
	if handler.bootstrap != nil {
		handler.bootstrap.setClientCapabilities(session, capabilities, replace)
	}
	if capabilities.DeviceProfile != nil {
		handler.storeCapabilitiesDeviceProfile(session, capabilities.DeviceProfile)
	}
}

func (handler *Handler) storeCapabilitiesDeviceProfile(session AuthenticatedSession, profile *DeviceProfile) {
	if profile == nil {
		return
	}
	if handler.bootstrap != nil {
		handler.bootstrap.setDeviceProfile(session, *profile)
	}
	if handler.playSessions != nil {
		handler.playSessions.setDeviceProfile(session, *profile)
	}
}

func isCompatWebSocketUpgrade(request *http.Request) bool {
	if request == nil || !strings.EqualFold(strings.TrimSpace(request.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, value := range strings.Split(request.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(value), "upgrade") {
			return true
		}
	}
	return false
}

func (registry *playSessionRegistry) closeEncoding(ctx context.Context, session AuthenticatedSession, playSessionID string) {
	if registry == nil || playSessionID == "" {
		return
	}
	registry.mu.Lock()
	entry := registry.entries[playSessionID]
	if entry != nil && ownerMatches(entry, session) {
		delete(registry.entries, playSessionID)
	} else {
		entry = nil
	}
	registry.mu.Unlock()
	if entry != nil {
		registry.closeEntries(ctx, []*playSessionEntry{entry})
	}
}
