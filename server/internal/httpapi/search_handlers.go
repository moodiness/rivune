package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/collection"
)

const maximumSemanticSearchRequestBytes = 16 << 10

type semanticSearchRequest struct {
	Query             string   `json:"query"`
	MediaType         string   `json:"mediaType,omitempty"`
	Language          string   `json:"language,omitempty"`
	Region            string   `json:"region,omitempty"`
	Page              int      `json:"page,omitempty"`
	Limit             int      `json:"limit,omitempty"`
	ExcludedIntentIDs []string `json:"excludedIntentIds,omitempty"`
}

func (api *API) semanticSearch(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request semanticSearchRequest
	if err := decodeJSONLimit(w, r, &request, maximumSemanticSearchRequestBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Semantic search requires a valid JSON object")
		return
	}
	if request.Page == 0 {
		request.Page = 1
	}
	if request.Limit == 0 {
		request.Limit = 24
	}
	for index := range request.ExcludedIntentIDs {
		request.ExcludedIntentIDs[index] = strings.TrimSpace(request.ExcludedIntentIDs[index])
	}
	page, err := api.collections.SemanticSearch(r.Context(), principal, collection.SemanticSearchInput{
		Query: request.Query, MediaType: request.MediaType, Language: request.Language, Region: request.Region,
		Page: request.Page, Limit: request.Limit, ExcludedIntentIDs: request.ExcludedIntentIDs,
	})
	if err != nil {
		if r.Context().Err() != nil || errors.Is(err, context.Canceled) {
			return
		}
		api.writeCollectionError(w, "semantic search", err)
		return
	}
	if api.collectionArtwork != nil {
		api.collectionArtwork.PresentSemanticSearchPage(r.Context(), &page)
	}
	writeJSON(w, http.StatusOK, page)
}
