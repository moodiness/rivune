package httpapi

import (
	"errors"
	"net/http"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
)

func (a *API) playbackActivity(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	activity, err := a.playback.Activity(r.Context(), principal)
	switch {
	case errors.Is(err, playback.ErrForbidden):
		writeError(w, http.StatusForbidden, "admin_required", "Administrator access is required")
	case err != nil:
		a.internalError(w, "load playback activity", err)
	default:
		writeJSON(w, http.StatusOK, activity)
	}
}

func (a *API) stopPlaybackActivitySession(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.playback.StopActivitySession(r.Context(), principal, r.PathValue("sessionId"))
	switch {
	case errors.Is(err, playback.ErrForbidden):
		writeError(w, http.StatusForbidden, "admin_required", "Administrator access is required")
	case errors.Is(err, playback.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "playback_session_not_found", "The playback session is invalid or expired")
	case err != nil:
		a.internalError(w, "stop managed playback session", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) purgePlaybackActivity(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	result, err := a.playback.PurgeActivity(r.Context(), principal)
	switch {
	case errors.Is(err, playback.ErrForbidden):
		writeError(w, http.StatusForbidden, "admin_required", "Administrator access is required")
	case err != nil:
		a.internalError(w, "purge playback activity", err)
	default:
		writeJSON(w, http.StatusOK, result)
	}
}
