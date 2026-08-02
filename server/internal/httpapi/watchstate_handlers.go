package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

func (a *API) resolveTitle(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		MediaType     string `json:"mediaType"`
		Provider      string `json:"provider"`
		ExternalID    string `json:"externalId"`
		ResourceID    string `json:"resourceId"`
		Title         string `json:"title"`
		PosterURL     string `json:"posterUrl"`
		BackgroundURL string `json:"backgroundUrl"`
		ReleaseInfo   string `json:"releaseInfo"`
		Released      string `json:"released"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := a.watchstate.ResolveTitle(r.Context(), principal, watchstate.ResolveTitleInput{
		MediaType: request.MediaType, Provider: request.Provider, ExternalID: request.ExternalID,
		ResourceID: request.ResourceID, Title: request.Title, PosterURL: request.PosterURL,
		BackgroundURL: request.BackgroundURL, ReleaseInfo: request.ReleaseInfo, Released: request.Released,
	})
	if err != nil {
		a.writeWatchstateError(w, "resolve title", err)
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeTitleReference(r.Context(), &result)
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) library(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	page, err := integerQuery(r, "page")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_watch_state", err.Error())
		return
	}
	pageSize, err := integerQuery(r, "pageSize")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_watch_state", err.Error())
		return
	}
	result, err := a.watchstate.Library(r.Context(), principal, r.URL.Query().Get("mediaType"), page, pageSize)
	if err != nil {
		a.writeWatchstateError(w, "list library", err)
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeLibraryPage(r.Context(), &result)
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) addLibrary(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	item, err := a.watchstate.AddLibrary(r.Context(), principal, r.PathValue("titleId"))
	if err != nil {
		a.writeWatchstateError(w, "add library title", err)
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeLibraryItem(r.Context(), &item)
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) removeLibrary(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if err := a.watchstate.RemoveLibrary(r.Context(), principal, r.PathValue("titleId")); err != nil {
		a.writeWatchstateError(w, "remove library title", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) getProgress(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	progress, err := a.watchstate.GetProgress(r.Context(), principal, r.PathValue("titleId"))
	if errors.Is(err, watchstate.ErrProgressNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		a.writeWatchstateError(w, "read playback progress", err)
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

func (a *API) updateProgress(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		PositionSeconds int   `json:"positionSeconds"`
		DurationSeconds int   `json:"durationSeconds"`
		Completed       bool  `json:"completed"`
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	progress, err := a.watchstate.UpdateProgress(r.Context(), principal, r.PathValue("titleId"), watchstate.UpdateProgressInput{
		PositionSeconds: request.PositionSeconds,
		DurationSeconds: request.DurationSeconds,
		Completed:       request.Completed,
		ExpectedVersion: request.ExpectedVersion,
	})
	if err != nil {
		a.writeWatchstateError(w, "update playback progress", err)
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

func (a *API) clearProgress(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	expectedVersion, err := int64Query(r, "expectedVersion")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_watch_state", err.Error())
		return
	}
	if err := a.watchstate.ClearProgress(r.Context(), principal, r.PathValue("titleId"), expectedVersion); err != nil {
		a.writeWatchstateError(w, "clear playback progress", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) markWatched(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	input, ok := completionInput(w, r)
	if !ok {
		return
	}
	progress, err := a.watchstate.SetWatched(r.Context(), principal, r.PathValue("titleId"), true, input)
	if err != nil {
		a.writeWatchstateError(w, "mark title watched", err)
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

func (a *API) markUnwatched(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	expectedVersion, err := int64Query(r, "expectedVersion")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_watch_state", err.Error())
		return
	}
	progress, err := a.watchstate.SetWatched(r.Context(), principal, r.PathValue("titleId"), false, watchstate.CompletionInput{ExpectedVersion: expectedVersion})
	if err != nil {
		a.writeWatchstateError(w, "mark title unwatched", err)
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

func (a *API) continueWatching(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	limit, err := integerQuery(r, "limit")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_watch_state", err.Error())
		return
	}
	result, err := a.watchstate.ContinueWatching(r.Context(), principal, limit)
	if err != nil {
		a.writeWatchstateError(w, "list continue watching", err)
		return
	}
	if a.artwork != nil {
		a.artwork.LocalizeContinuePage(r.Context(), &result)
	}
	writeJSON(w, http.StatusOK, result)
}

func completionInput(w http.ResponseWriter, r *http.Request) (watchstate.CompletionInput, bool) {
	if !requireJSON(w, r) {
		return watchstate.CompletionInput{}, false
	}
	var request struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return watchstate.CompletionInput{}, false
	}
	return watchstate.CompletionInput{ExpectedVersion: request.ExpectedVersion}, true
}

func integerQuery(r *http.Request, name string) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New(name + " must be an integer")
	}
	return parsed, nil
}

func int64Query(r *http.Request, name string) (int64, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, errors.New(name + " must be an integer")
	}
	return parsed, nil
}

func (a *API) writeWatchstateError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, watchstate.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_watch_state", strings.TrimPrefix(err.Error(), watchstate.ErrInvalidInput.Error()+": "))
	case errors.Is(err, watchstate.ErrProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select an active profile before accessing watch state")
	case errors.Is(err, watchstate.ErrNotFound):
		writeError(w, http.StatusNotFound, "title_not_found", "The title or playback progress does not exist")
	case errors.Is(err, watchstate.ErrConflict):
		writeError(w, http.StatusConflict, "watch_state_conflict", "The watch state changed on another device; reload it before retrying")
	default:
		a.internalError(w, operation, err)
	}
}
