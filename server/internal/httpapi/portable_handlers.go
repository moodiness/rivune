package httpapi

import (
	"errors"
	"net/http"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/portable"
)

func (a *API) exportProfileArchive(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !principal.IsGlobalAdministrator() {
		writeError(w, http.StatusForbidden, "global_admin_required", "Global administrator access is required")
		return
	}
	document, err := a.portable.Export(r.Context(), principal, r.PathValue("profileId"))
	if err != nil {
		a.writePortableError(w, "export profile archive", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="rivune-profile-archive.json"`)
	writeJSON(w, http.StatusOK, document)
}

func (a *API) importProfileArchive(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !principal.IsGlobalAdministrator() {
		writeError(w, http.StatusForbidden, "global_admin_required", "Global administrator access is required")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	var document portable.Document
	if err := decodeJSONLimit(w, r, &document, portable.MaximumDocumentBytes); err != nil {
		var maximumBytesError *http.MaxBytesError
		if errors.As(err, &maximumBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "profile_archive_too_large", "Profile archive exceeds the 16 MiB limit")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_profile_archive", err.Error())
		return
	}
	report, err := a.portable.Import(r.Context(), principal, r.PathValue("profileId"), document)
	if err != nil {
		a.writePortableError(w, "import profile archive", err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (a *API) writePortableError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, portable.ErrForbidden):
		writeError(w, http.StatusForbidden, "global_admin_required", "Global administrator access is required")
	case errors.Is(err, portable.ErrProfileNotFound):
		writeError(w, http.StatusNotFound, "profile_archive_target_not_found", "The target profile does not exist")
	case errors.Is(err, portable.ErrInvalidDocument):
		writeError(w, http.StatusBadRequest, "invalid_profile_archive", "The profile archive is invalid")
	case errors.Is(err, portable.ErrConflict):
		writeError(w, http.StatusConflict, "profile_archive_conflict", "The profile archive conflicts with target data")
	default:
		a.internalError(w, operation, err)
	}
}
