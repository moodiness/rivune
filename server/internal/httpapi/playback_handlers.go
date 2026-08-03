package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
)

func (a *API) playbackSources(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input playback.SourcesInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if principal.ActiveProfileID != nil {
		effective, err := a.settings.Effective(r.Context(), principal, *principal.ActiveProfileID)
		if err != nil {
			a.internalError(w, "resolve playback source settings", err)
			return
		}
		input.PreferredAudioLanguage = effective.Values.AudioLanguage
		input.PreferredSubtitleLanguage = effective.Values.SubtitleLanguage
		input.PreferredForcedSubtitleLanguage = effective.Values.ForcedSubtitleLanguage
		preferDirectPlay := effective.Values.PreferDirectPlay
		input.Capabilities.PreferDirectPlay = &preferDirectPlay
	}
	sources, err := a.playback.Sources(r.Context(), principal, input)
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before loading playback sources")
	case errors.Is(err, playback.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_playback_request", "The playback source request is invalid")
	case errors.Is(err, playback.ErrProviderUnavailable):
		writeError(w, http.StatusBadGateway, "playback_provider_unavailable", "Playback providers are unavailable")
	case err != nil:
		a.internalError(w, "load playback sources", err)
	default:
		writeJSON(w, http.StatusOK, sources)
	}
}
func (a *API) playbackMarkers(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	season, seasonErr := strconv.Atoi(r.URL.Query().Get("season"))
	episode, episodeErr := strconv.Atoi(r.URL.Query().Get("episode"))
	if seasonErr != nil || episodeErr != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_playback_request", "The playback marker request is invalid")
		return
	}
	if principal.ActiveProfileID == nil {
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before loading playback markers")
		return
	}
	effective, err := a.settings.Effective(r.Context(), principal, *principal.ActiveProfileID)
	if err != nil {
		a.internalError(w, "resolve playback marker settings", err)
		return
	}
	markers, err := a.playback.Markers(r.Context(), principal, playback.MarkerInput{
		IMDBID: r.URL.Query().Get("imdbId"), Season: season, Episode: episode,
		IncludeIntro: effective.Values.SkipIntroEnabled,
		IncludeRecap: effective.Values.SkipRecapEnabled,
		IncludeOutro: effective.Values.SkipOutroEnabled,
	})
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before loading playback markers")
	case errors.Is(err, playback.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_playback_request", "The playback marker request is invalid")
	case err != nil:
		a.internalError(w, "load playback markers", err)
	default:
		writeJSON(w, http.StatusOK, markers)
	}
}

func (a *API) preparePlayback(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input playback.PrepareInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if principal.ActiveProfileID != nil {
		effective, err := a.settings.Effective(r.Context(), principal, *principal.ActiveProfileID)
		if err != nil {
			a.internalError(w, "resolve playback preparation settings", err)
			return
		}
		input.AllowTranscoding = effective.Values.AllowTranscoding
		input.MaximumHeight = playbackMaximumHeight(effective.Values.MaximumResolution)
	}
	preparation, err := a.playback.Prepare(r.Context(), principal, input)
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before preparing playback")
	case errors.Is(err, playback.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_playback_request", "The playback preparation request is invalid")
	case errors.Is(err, playback.ErrSourceReferenceExpired):
		writeError(w, http.StatusGone, "playback_source_expired", "The selected source expired and must be refreshed")
	case errors.Is(err, playback.ErrTranscodingDisabled):
		writeError(w, http.StatusUnprocessableEntity, "playback_transcoding_disabled", "This source requires server transcoding, but transcoding is disabled for this profile")
	case errors.Is(err, playback.ErrClientCapabilityMissing):
		writeError(w, http.StatusUnprocessableEntity, "playback_client_capability_missing", "This source requires a server output mode that this client did not announce")
	case errors.Is(err, playback.ErrUnsupportedSource):
		writeError(w, http.StatusUnprocessableEntity, "playback_source_unsupported", "This source needs video or audio conversion that browser playback does not permit; choose another source or an external player")
	case errors.Is(err, playback.ErrNoPlayableSource):
		writeError(w, http.StatusNotFound, "playback_source_not_found", "The selected source is not compatible with this device")
	case errors.Is(err, playback.ErrMediaSourceFailed):
		writeError(w, http.StatusBadGateway, "playback_source_failed", "The selected media source stopped responding")
	case errors.Is(err, playback.ErrMediaCapacityReached):
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusServiceUnavailable, "playback_capacity_reached", "All media processing slots are currently in use")
	case errors.Is(err, playback.ErrMediaStorageLimit):
		writeError(w, http.StatusInsufficientStorage, "playback_storage_limit", "The media workspace storage limit was reached")
	case errors.Is(err, playback.ErrMediaProcessingFailed):
		writeError(w, http.StatusBadGateway, "playback_processing_failed", "The server could not prepare this source for playback")
	case err != nil:
		a.internalError(w, "prepare playback", err)
	default:
		writeJSON(w, http.StatusOK, preparation)
	}
}

func (a *API) resolvePlayback(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input playback.ResolveInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if principal.ActiveProfileID != nil {
		effective, err := a.settings.Effective(r.Context(), principal, *principal.ActiveProfileID)
		if err != nil {
			a.internalError(w, "resolve playback session settings", err)
			return
		}
		input.AllowTranscoding = effective.Values.AllowTranscoding
		input.MaximumHeight = playbackMaximumHeight(effective.Values.MaximumResolution)
	}
	session, err := a.playback.Resolve(r.Context(), principal, input)
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before starting playback")
	case errors.Is(err, playback.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_playback_request", "The playback request is invalid")
	case errors.Is(err, playback.ErrSourceReferenceExpired):
		writeError(w, http.StatusGone, "playback_source_expired", "The selected source expired and must be refreshed")
	case errors.Is(err, playback.ErrTranscodingDisabled):
		writeError(w, http.StatusUnprocessableEntity, "playback_transcoding_disabled", "This source requires server transcoding, but transcoding is disabled for this profile")
	case errors.Is(err, playback.ErrClientCapabilityMissing):
		writeError(w, http.StatusUnprocessableEntity, "playback_client_capability_missing", "This source requires a server output mode that this client did not announce")
	case errors.Is(err, playback.ErrUnsupportedSource):
		writeError(w, http.StatusUnprocessableEntity, "playback_source_unsupported", "This source needs video or audio conversion that browser playback does not permit; choose another source or an external player")
	case errors.Is(err, playback.ErrNoPlayableSource):
		writeError(w, http.StatusNotFound, "playback_source_not_found", "The selected source is not compatible with this device")
	case errors.Is(err, playback.ErrProviderUnavailable):
		writeError(w, http.StatusBadGateway, "playback_provider_unavailable", "Playback providers are unavailable")
	case errors.Is(err, playback.ErrMediaSourceFailed):
		writeError(w, http.StatusBadGateway, "playback_source_failed", "The selected media source stopped responding")
	case errors.Is(err, playback.ErrMediaCapacityReached):
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusServiceUnavailable, "playback_capacity_reached", "All media processing slots are currently in use")
	case errors.Is(err, playback.ErrMediaStorageLimit):
		writeError(w, http.StatusInsufficientStorage, "playback_storage_limit", "The media workspace storage limit was reached")
	case errors.Is(err, playback.ErrMediaProcessingFailed):
		writeError(w, http.StatusBadGateway, "playback_processing_failed", "The server could not prepare this source for playback")
	case err != nil:
		a.internalError(w, "resolve playback", err)
	default:
		writeJSON(w, http.StatusCreated, session)
	}
}

func (a *API) stopPlayback(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.playback.Stop(r.Context(), principal, r.PathValue("sessionId"))
	switch {
	case errors.Is(err, playback.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "playback_session_not_found", "The playback session is invalid or expired")
	case err != nil:
		a.internalError(w, "stop playback", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func playbackMaximumHeight(value string) int {
	switch value {
	case "2160p":
		return 2160
	case "1080p":
		return 1080
	case "720p":
		return 720
	case "480p":
		return 480
	default:
		return 0
	}
}

func (a *API) playbackAsset(w http.ResponseWriter, r *http.Request) {
	if a.rejectMaintenanceRequest(w, r) {
		return
	}
	err := a.playback.ProxyAsset(
		w, r, r.PathValue("sessionId"), r.PathValue("assetId"),
		r.URL.Query().Get("token"), r.URL.Query().Get("target"), r.URL.Query().Get("signature"),
	)
	switch {
	case errors.Is(err, playback.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "playback_session_not_found", "The playback session is invalid or expired")
	case errors.Is(err, playback.ErrClientCapabilityMissing):
		writeError(w, http.StatusUnprocessableEntity, "playback_client_capability_missing", "This source requires a server output mode that this client did not announce")
	case errors.Is(err, playback.ErrMediaSourceFailed):
		writeError(w, http.StatusBadGateway, "playback_source_failed", "The selected media source stopped responding")
	case errors.Is(err, playback.ErrMediaCapacityReached):
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusServiceUnavailable, "playback_capacity_reached", "All media processing slots are currently in use")
	case errors.Is(err, playback.ErrMediaStorageLimit):
		writeError(w, http.StatusInsufficientStorage, "playback_storage_limit", "The media workspace storage limit was reached")
	case errors.Is(err, playback.ErrMediaProcessingFailed):
		writeError(w, http.StatusBadGateway, "playback_processing_failed", "The server could not prepare this source for playback")
	case err != nil:
		a.internalError(w, "proxy playback asset", err)
	}
}
