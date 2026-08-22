package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/coordination"
)

func (a *API) playbackDeviceHeartbeat(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input coordination.DeviceHeartbeatInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	device, err := a.coordination.Heartbeat(r.Context(), principal, input)
	if writeCoordinationError(a, w, err, "update playback device") {
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (a *API) playbackDevices(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	devices, err := a.coordination.Devices(r.Context(), principal)
	if writeCoordinationError(a, w, err, "list playback devices") {
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

func (a *API) sendPlaybackCommand(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input coordination.CommandInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	command, err := a.coordination.SendCommand(r.Context(), principal, r.PathValue("sessionId"), input)
	if writeCoordinationError(a, w, err, "send playback command") {
		return
	}
	writeJSON(w, http.StatusCreated, command)
}

func (a *API) playbackCommands(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	afterID := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_playback_coordination", "after must be a non-negative integer")
			return
		}
		afterID = parsed
	}
	commands, err := a.coordination.Commands(r.Context(), principal, afterID)
	if writeCoordinationError(a, w, err, "list playback commands") {
		return
	}
	writeJSON(w, http.StatusOK, commands)
}

func (a *API) acknowledgePlaybackCommand(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	commandID, err := strconv.ParseInt(r.PathValue("commandId"), 10, 64)
	if err != nil || commandID <= 0 {
		writeError(w, http.StatusNotFound, "playback_coordination_not_found", "The playback command does not exist")
		return
	}
	if err := a.coordination.AcknowledgeCommand(r.Context(), principal, commandID); writeCoordinationError(a, w, err, "acknowledge playback command") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) createPlaybackRoom(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input coordination.CreateRoomInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	room, err := a.coordination.CreateRoom(r.Context(), principal, input)
	if writeCoordinationError(a, w, err, "create playback room") {
		return
	}
	writeJSON(w, http.StatusCreated, room)
}

func (a *API) joinPlaybackRoom(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input coordination.JoinRoomInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	room, err := a.coordination.JoinRoom(r.Context(), principal, input.Code)
	if writeCoordinationError(a, w, err, "join playback room") {
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (a *API) playbackRoom(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	room, err := a.coordination.Room(r.Context(), principal, r.PathValue("roomId"))
	if writeCoordinationError(a, w, err, "read playback room") {
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (a *API) updatePlaybackRoom(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input coordination.UpdateRoomInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	room, err := a.coordination.UpdateRoom(r.Context(), principal, r.PathValue("roomId"), input)
	if writeCoordinationError(a, w, err, "update playback room") {
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (a *API) leavePlaybackRoom(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if err := a.coordination.LeaveRoom(r.Context(), principal, r.PathValue("roomId")); writeCoordinationError(a, w, err, "leave playback room") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeCoordinationError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, coordination.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before coordinating playback")
	case errors.Is(err, coordination.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_playback_coordination", "The playback coordination request is invalid")
	case errors.Is(err, coordination.ErrNotFound):
		writeError(w, http.StatusNotFound, "playback_coordination_not_found", "The playback device, command, or room does not exist")
	case errors.Is(err, coordination.ErrForbidden):
		writeError(w, http.StatusForbidden, "playback_coordination_forbidden", "This device cannot modify that playback room")
	case errors.Is(err, coordination.ErrConflict):
		writeError(w, http.StatusConflict, "playback_coordination_conflict", "The playback room changed on another device")
	case errors.Is(err, coordination.ErrCapacity):
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusServiceUnavailable, "playback_coordination_capacity", "The playback coordination limit was reached")
	default:
		a.internalError(w, operation, err)
	}
	return true
}
