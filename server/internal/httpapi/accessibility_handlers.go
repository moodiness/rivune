package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/moodiness/rivune/server/internal/accessibility"
	"github.com/moodiness/rivune/server/internal/auth"
)

const accessibilityRequestMaximumBytes = 4 << 10

func (a *API) accessibilityPreferences(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	document, err := a.accessibility.Get(r.Context(), principal, r.PathValue("profileId"))
	if writeAccessibilityError(a, w, err, "read accessibility preferences") {
		return
	}
	writeAccessibilityDocument(w, document)
}

func (a *API) updateAccessibilityPreferences(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var input accessibility.UpdateInput
	if err := decodeJSONLimit(w, r, &input, accessibilityRequestMaximumBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Accessibility preferences must be one bounded JSON object")
		return
	}
	document, err := a.accessibility.Update(r.Context(), principal, r.PathValue("profileId"), input)
	if writeAccessibilityError(a, w, err, "update accessibility preferences") {
		return
	}
	writeAccessibilityDocument(w, document)
}

func writeAccessibilityDocument(w http.ResponseWriter, document accessibility.Document) {
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, document.Revision))
	writeJSON(w, http.StatusOK, document)
}

func writeAccessibilityError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, accessibility.ErrActiveProfileRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select the requested profile before accessing its accessibility preferences")
	case errors.Is(err, accessibility.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_accessibility_preferences", "Accessibility preferences contain an unsupported value")
	case errors.Is(err, accessibility.ErrConflict):
		writeError(w, http.StatusConflict, "accessibility_preferences_conflict", "Accessibility preferences changed on another device; reload them before retrying")
	default:
		a.internalError(w, operation, err)
	}
	return true
}
