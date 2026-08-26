package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/savedsearch"
)

const maximumSavedSearchRequestBytes = 16 << 10

func (api *API) listSavedSearches(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	values, err := api.savedSearches.ListSavedSearches(r.Context(), principal)
	if err != nil { api.writeSavedSearchError(w, "list saved searches", err); return }
	writeJSON(w, http.StatusOK, map[string]any{"savedSearches": values})
}

func (api *API) createSavedSearch(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) { return }
	var input savedsearch.SavedSearchInput
	if err := decodeJSONLimit(w, r, &input, maximumSavedSearchRequestBytes); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", "Saved search creation requires a valid JSON object"); return }
	value, err := api.savedSearches.CreateSavedSearch(r.Context(), principal, input)
	if err != nil { api.writeSavedSearchError(w, "create saved search", err); return }
	writeJSON(w, http.StatusCreated, value)
}

func (api *API) updateSavedSearch(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) { return }
	var input savedsearch.SavedSearchInput
	if err := decodeJSONLimit(w, r, &input, maximumSavedSearchRequestBytes); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", "Saved search update requires a valid JSON object"); return }
	value, err := api.savedSearches.UpdateSavedSearch(r.Context(), principal, r.PathValue("savedSearchId"), input)
	if err != nil { api.writeSavedSearchError(w, "update saved search", err); return }
	writeJSON(w, http.StatusOK, value)
}

func (api *API) deleteSavedSearch(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	revision, ok := expectedSavedSearchRevision(w, r); if !ok { return }
	if err := api.savedSearches.DeleteSavedSearch(r.Context(), principal, r.PathValue("savedSearchId"), revision); err != nil { api.writeSavedSearchError(w, "delete saved search", err); return }
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) listSmartCollections(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	values, err := api.savedSearches.ListSmartCollections(r.Context(), principal)
	if err != nil { api.writeSavedSearchError(w, "list smart collections", err); return }
	writeJSON(w, http.StatusOK, map[string]any{"smartCollections": values})
}

func (api *API) createSmartCollection(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) { return }
	var input savedsearch.SmartCollectionInput
	if err := decodeJSONLimit(w, r, &input, maximumSavedSearchRequestBytes); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", "Smart collection creation requires a valid JSON object"); return }
	value, err := api.savedSearches.CreateSmartCollection(r.Context(), principal, input)
	if err != nil { api.writeSavedSearchError(w, "create smart collection", err); return }
	writeJSON(w, http.StatusCreated, value)
}

func (api *API) updateSmartCollection(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) { return }
	var input savedsearch.SmartCollectionInput
	if err := decodeJSONLimit(w, r, &input, maximumSavedSearchRequestBytes); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", "Smart collection update requires a valid JSON object"); return }
	value, err := api.savedSearches.UpdateSmartCollection(r.Context(), principal, r.PathValue("smartCollectionId"), input)
	if err != nil { api.writeSavedSearchError(w, "update smart collection", err); return }
	writeJSON(w, http.StatusOK, value)
}

func (api *API) deleteSmartCollection(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	revision, ok := expectedSavedSearchRevision(w, r); if !ok { return }
	if err := api.savedSearches.DeleteSmartCollection(r.Context(), principal, r.PathValue("smartCollectionId"), revision); err != nil { api.writeSavedSearchError(w, "delete smart collection", err); return }
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) smartCollectionItems(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	page, err := integerQuery(r, "page"); if err != nil { writeError(w, http.StatusUnprocessableEntity, "invalid_saved_search", "page must be an integer"); return }
	pageSize, err := integerQuery(r, "pageSize"); if err != nil { writeError(w, http.StatusUnprocessableEntity, "invalid_saved_search", "pageSize must be an integer"); return }
	if page == 0 { page = 1 }; if pageSize == 0 { pageSize = 24 }
	value, err := api.savedSearches.EvaluateSmartCollection(r.Context(), principal, r.PathValue("smartCollectionId"), page, pageSize)
	if err != nil { api.writeSavedSearchError(w, "evaluate smart collection", err); return }
	writeJSON(w, http.StatusOK, value)
}

func expectedSavedSearchRevision(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("expectedRevision")); revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision < 1 { writeError(w, http.StatusUnprocessableEntity, "invalid_saved_search", "expectedRevision must be a positive integer"); return 0, false }
	return revision, true
}

func (api *API) writeSavedSearchError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, savedsearch.ErrInvalidInput): writeError(w, http.StatusUnprocessableEntity, "invalid_saved_search", strings.TrimPrefix(err.Error(), savedsearch.ErrInvalidInput.Error()+": "))
	case errors.Is(err, savedsearch.ErrProfileRequired): writeError(w, http.StatusConflict, "profile_selection_required", "Select an active profile before managing saved searches")
	case errors.Is(err, savedsearch.ErrNotFound): writeError(w, http.StatusNotFound, "saved_search_not_found", "The saved search or smart collection does not exist")
	case errors.Is(err, savedsearch.ErrConflict): writeError(w, http.StatusConflict, "saved_search_conflict", "The saved search changed on another device; reload it before saving")
	case errors.Is(err, savedsearch.ErrForbidden): writeError(w, http.StatusForbidden, "saved_search_forbidden", "The active profile cannot manage this saved search")
	default: api.internalError(w, operation, err)
	}
}
