package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/metadata"
)

func (api *API) listCollections(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	values, err := api.collections.List(r.Context(), principal)
	if err != nil {
		api.writeCollectionError(w, "list collections", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": values})
}

func (api *API) exportCollections(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	document, err := api.collections.Export(r.Context(), principal)
	if err != nil {
		api.writeCollectionError(w, "export collections", err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="rivune-collections.json"`)
	writeJSON(w, http.StatusOK, document)
}

func (api *API) importCollections(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var document collection.ExportDocument
	if err := decodeJSONLimit(w, r, &document, 16*1024*1024); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := api.collections.Import(r.Context(), principal, document)
	if err != nil {
		api.writeCollectionError(w, "import collections", err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (api *API) getCollection(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	value, err := api.collections.Get(r.Context(), principal, r.PathValue("collectionId"))
	if err != nil {
		api.writeCollectionError(w, "get collection", err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) createCollection(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input collection.SaveInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	value, err := api.collections.Create(r.Context(), principal, input)
	if err != nil {
		api.writeCollectionError(w, "create collection", err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (api *API) updateCollection(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input collection.SaveInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	value, err := api.collections.Update(r.Context(), principal, r.PathValue("collectionId"), input)
	if err != nil {
		api.writeCollectionError(w, "update collection", err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) deleteCollection(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if err := api.collections.Delete(r.Context(), principal, r.PathValue("collectionId")); err != nil {
		api.writeCollectionError(w, "delete collection", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) reorderCollections(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input collection.ReorderInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	values, err := api.collections.Reorder(r.Context(), principal, input)
	if err != nil {
		api.writeCollectionError(w, "reorder collections", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": values})
}

func (api *API) resolveCollectionFolder(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	page, limit, ok := collectionPage(w, r)
	if !ok {
		return
	}
	value, err := api.collections.ResolveFolder(
		r.Context(), principal, r.PathValue("collectionId"), r.PathValue("folderId"),
		page, limit, r.URL.Query().Get("language"), r.URL.Query().Get("region"),
	)
	if err != nil {
		api.writeCollectionError(w, "resolve collection folder", err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) lookupCollectionTMDB(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	page, err := integerQuery(r, "page")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_collection_query", "page must be an integer")
		return
	}
	if page == 0 {
		page = 1
	}
	values, err := api.collections.LookupTMDB(
		r.Context(), principal, r.URL.Query().Get("kind"), r.URL.Query().Get("query"),
		r.URL.Query().Get("language"), page,
	)
	if err != nil {
		api.writeCollectionError(w, "lookup TMDB collection source", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": values})
}

func (api *API) collectionTMDBGenres(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	values, err := api.collections.TMDBGenres(
		r.Context(), principal, r.URL.Query().Get("mediaType"), r.URL.Query().Get("language"),
	)
	if err != nil {
		api.writeCollectionError(w, "list TMDB collection genres", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"genres": values})
}

func collectionPage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	page, err := integerQuery(r, "page")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_collection_query", "page must be an integer")
		return 0, 0, false
	}
	limit, err := integerQuery(r, "limit")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_collection_query", "limit must be an integer")
		return 0, 0, false
	}
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = 100
	}
	return page, limit, true
}

func (api *API) writeCollectionError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, collection.ErrInvalidInput):
		message := strings.TrimPrefix(err.Error(), collection.ErrInvalidInput.Error()+": ")
		writeError(w, http.StatusUnprocessableEntity, "invalid_collection", message)
	case errors.Is(err, collection.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select an active profile before managing collections")
	case errors.Is(err, collection.ErrNotFound):
		writeError(w, http.StatusNotFound, "collection_not_found", "The collection or folder does not exist")
	case errors.Is(err, collection.ErrConflict):
		writeError(w, http.StatusConflict, "collection_conflict", "The collection changed on another device; reload it before saving")
	case errors.Is(err, collection.ErrProviderUnavailable),
		errors.Is(err, metadata.ErrProviderUnavailable), errors.Is(err, metadata.ErrProviderUnauthorized),
		errors.Is(err, metadata.ErrProviderRateLimited), errors.Is(err, metadata.ErrProviderFailure):
		writeError(w, http.StatusServiceUnavailable, "collection_provider_unavailable", "The collection source provider is unavailable")
	case errors.Is(err, collection.ErrForbidden):
		writeError(w, http.StatusForbidden, "collection_forbidden", "The active profile cannot manage this collection's profile access")
	default:
		api.internalError(w, operation, err)
	}
}
