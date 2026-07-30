package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/profile"
)

func (a *API) listProfileAvatarPresets(w http.ResponseWriter, _ *http.Request, _ auth.Principal) {
	presets := profile.AvatarPresets()
	response := make([]map[string]string, 0, len(presets))
	for _, preset := range presets {
		response = append(response, map[string]string{
			"id": preset.ID, "name": preset.Name, "url": "/api/v1/profile-avatars/" + preset.ID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"presets": response})
}

func (a *API) profileAvatarPresetImage(w http.ResponseWriter, r *http.Request) {
	image, found := profile.AvatarPresetSVG(r.PathValue("presetId"))
	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(image)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image)
}

func (a *API) setProfileAvatarPreset(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		PresetID string `json:"presetId"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	updated, err := a.profiles.SetAvatarPreset(r.Context(), principal, r.PathValue("profileId"), request.PresetID)
	if err != nil {
		a.writeProfileAvatarError(w, "set profile avatar preset", err)
		return
	}
	writeJSON(w, http.StatusOK, newProfileResponse(updated))
}

func (a *API) uploadProfileAvatar(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if r.ContentLength > 3<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "avatar_too_large", "The avatar upload must not exceed 2 MiB")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_avatar_upload", "A multipart image field is required")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_avatar_upload", "A multipart image field is required")
		return
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil {
		a.internalError(w, "read profile avatar upload", err)
		return
	}
	if len(contents) > 2<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "avatar_too_large", "The avatar upload must not exceed 2 MiB")
		return
	}
	updated, err := a.profiles.SetAvatarImage(r.Context(), principal, r.PathValue("profileId"), contents)
	if err != nil {
		a.writeProfileAvatarError(w, "store profile avatar", err)
		return
	}
	writeJSON(w, http.StatusOK, newProfileResponse(updated))
}

func (a *API) customProfileAvatar(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	image, err := a.profiles.AvatarImage(r.Context(), principal, r.PathValue("profileId"))
	if err != nil {
		a.writeProfileAvatarError(w, "read profile avatar", err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Content-Type", image.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(image.Data)))
	w.Header().Set("Last-Modified", image.UpdatedAt.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image.Data)
}

func (a *API) writeProfileAvatarError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, profile.ErrNotFound):
		writeError(w, http.StatusNotFound, "profile_avatar_not_found", "The profile or custom avatar does not exist")
	case errors.Is(err, profile.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_profile_avatar", profileErrorMessage(err))
	case errors.Is(err, profile.ErrForbidden):
		writeError(w, http.StatusForbidden, "profile_forbidden", "This user cannot manage the profile")
	default:
		a.internalError(w, operation, err)
	}
}
