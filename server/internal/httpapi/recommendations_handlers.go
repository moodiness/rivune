package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

func (a *API) recommendations(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_recommendation_request", "limit must be an integer")
			return
		}
		limit = parsed
	}
	artworkShape := watchstate.RecommendationArtworkShape(r.URL.Query().Get("artworkShape"))
	if !artworkShape.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "invalid_recommendation_request", "artworkShape must be poster or landscape")
		return
	}
	result, err := a.watchstate.Recommendations(r.Context(), principal, limit, artworkShape)
	switch {
	case errors.Is(err, watchstate.ErrProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select a profile before loading recommendations")
	case errors.Is(err, watchstate.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_recommendation_request", "The recommendation request is invalid")
	case err != nil:
		a.internalError(w, "load local recommendations", err)
	default:
		if a.artwork != nil {
			a.artwork.LocalizeRecommendationPage(r.Context(), &result)
		}
		writeJSON(w, http.StatusOK, result)
	}
}
