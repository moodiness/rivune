package httpapi

import (
	"errors"
	"net/http"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/tracking"
)

func (a *API) trackingStatuses(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	statuses, err := a.tracking.Statuses(r.Context(), principal, r.PathValue("profileId"))
	if writeTrackingError(a, w, err, "read tracking integrations") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": statuses})
}

func (a *API) beginTrackingAuthorization(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	authorization, err := a.tracking.BeginDeviceAuthorization(r.Context(), principal, r.PathValue("profileId"), r.PathValue("provider"))
	if writeTrackingError(a, w, err, "begin tracking authorization") {
		return
	}
	writeJSON(w, http.StatusCreated, authorization)
}

func (a *API) completeTrackingAuthorization(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	status, err := a.tracking.CompleteDeviceAuthorization(r.Context(), principal, r.PathValue("profileId"), r.PathValue("provider"), r.PathValue("authorizationId"))
	if errors.Is(err, tracking.ErrAuthorizationWait) {
		writeJSON(w, http.StatusAccepted, map[string]bool{"pending": true})
		return
	}
	if writeTrackingError(a, w, err, "complete tracking authorization") {
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) updateTrackingPreferences(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input tracking.PreferencesInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	status, err := a.tracking.UpdatePreferences(r.Context(), principal, r.PathValue("profileId"), r.PathValue("provider"), input)
	if writeTrackingError(a, w, err, "update tracking preferences") {
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) disconnectTracking(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.tracking.Disconnect(r.Context(), principal, r.PathValue("profileId"), r.PathValue("provider"))
	if writeTrackingError(a, w, err, "disconnect tracking integration") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeTrackingError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, tracking.ErrForbidden):
		writeError(w, http.StatusForbidden, "tracking_forbidden", "This account cannot manage tracking for the requested profile")
	case errors.Is(err, tracking.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_tracking_request", err.Error())
	case errors.Is(err, tracking.ErrNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "tracking_not_configured", "The server administrator has not configured this tracking provider")
	case errors.Is(err, tracking.ErrNotConnected):
		writeError(w, http.StatusConflict, "tracking_not_connected", "This profile is not connected to the tracking provider")
	case errors.Is(err, tracking.ErrAuthorizationGone):
		writeError(w, http.StatusGone, "tracking_authorization_expired", "The device authorization expired; start a new connection")
	case errors.Is(err, tracking.ErrAuthorizationDenied):
		writeError(w, http.StatusForbidden, "tracking_authorization_denied", "The tracking provider authorization was denied")
	case errors.Is(err, tracking.ErrAuthorizationSlow):
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, "tracking_authorization_slow_down", "Wait before checking the authorization again")
	case errors.Is(err, tracking.ErrOutboxCapacity):
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "tracking_sync_capacity", "Tracking synchronization is temporarily at capacity; retry the mutation")
	case errors.Is(err, tracking.ErrProviderUnavailable):
		writeError(w, http.StatusServiceUnavailable, "tracking_provider_unavailable", "The tracking provider is temporarily unavailable")
	default:
		a.internalError(w, operation, err)
	}
	return true
}
