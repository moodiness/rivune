package httpapi

import (
	"errors"
	"net/http"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
)

func (a *API) createPlaybackFailover(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input playback.CreateFailoverInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	state, err := a.playback.CreateFailover(r.Context(), principal, input)
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before configuring playback failover")
	case errors.Is(err, playback.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_playback_failover", "The ordered playback failover candidates are invalid")
	case errors.Is(err, playback.ErrSourceReferenceExpired):
		writeError(w, http.StatusGone, "playback_source_expired", "A playback source expired and must be refreshed")
	case errors.Is(err, playback.ErrMediaCapacityReached):
		writeError(w, http.StatusServiceUnavailable, "playback_failover_capacity_reached", "Too many automatic playback failovers are active for this session")
	case err != nil:
		a.internalError(w, "create playback source failover", err)
	default:
		writeJSON(w, http.StatusCreated, state)
	}
}

func (a *API) getPlaybackFailover(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	state, err := a.playback.Failover(r.Context(), principal, r.PathValue("failoverId"))
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before reading playback failover")
	case errors.Is(err, playback.ErrFailoverNotFound):
		writeError(w, http.StatusNotFound, "playback_failover_not_found", "The playback failover is invalid or expired")
	case err != nil:
		a.internalError(w, "read playback source failover", err)
	default:
		writeJSON(w, http.StatusOK, state)
	}
}

func (a *API) advancePlaybackFailover(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input playback.AdvanceFailoverInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	state, err := a.playback.AdvanceFailover(r.Context(), principal, r.PathValue("failoverId"), input)
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before advancing playback failover")
	case errors.Is(err, playback.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_playback_failover", "The playback failover report is invalid")
	case errors.Is(err, playback.ErrFailoverIneligible):
		writeError(w, http.StatusUnprocessableEntity, "playback_failover_ineligible", "This playback error must not trigger an automatic source change")
	case errors.Is(err, playback.ErrFailoverConflict):
		writeError(w, http.StatusConflict, "playback_failover_conflict", "Playback failover changed; reload its current state")
	case errors.Is(err, playback.ErrFailoverNotFound):
		writeError(w, http.StatusNotFound, "playback_failover_not_found", "The playback failover is invalid or expired")
	case err != nil:
		a.internalError(w, "advance playback source failover", err)
	default:
		writeJSON(w, http.StatusOK, state)
	}
}

func (a *API) cancelPlaybackFailover(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.playback.CancelFailover(r.Context(), principal, r.PathValue("failoverId"))
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before cancelling playback failover")
	case errors.Is(err, playback.ErrFailoverNotFound):
		writeError(w, http.StatusNotFound, "playback_failover_not_found", "The playback failover is invalid, expired, or already inactive")
	case err != nil:
		a.internalError(w, "cancel playback source failover", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
