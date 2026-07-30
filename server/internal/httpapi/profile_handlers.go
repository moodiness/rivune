package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/profile"
)

type profileAvatarResponse struct {
	Kind     string `json:"kind"`
	PresetID string `json:"presetId,omitempty"`
	URL      string `json:"url"`
}

type profileResponse struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	IsChild   bool                  `json:"isChild"`
	HasPIN    bool                  `json:"hasPin"`
	CanManage bool                  `json:"canManage"`
	Avatar    profileAvatarResponse `json:"avatar"`
}

type nullableString struct {
	Set   bool
	Value *string
}

func (value *nullableString) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Value = nil
		return nil
	}
	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = &decoded
	return nil
}

func (a *API) listProfiles(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	profiles, err := a.profiles.List(r.Context(), principal)
	if err != nil {
		a.internalError(w, "list profiles", err)
		return
	}
	response := make([]profileResponse, 0, len(profiles))
	for _, item := range profiles {
		response = append(response, newProfileResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": response})
}

func (a *API) createProfile(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Name    string  `json:"name"`
		IsChild bool    `json:"isChild"`
		PIN     *string `json:"pin,omitempty"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	created, err := a.profiles.Create(r.Context(), principal, profile.CreateInput{
		Name: request.Name, IsChild: request.IsChild, PIN: request.PIN,
	})
	switch {
	case errors.Is(err, profile.ErrForbidden):
		writeError(w, http.StatusForbidden, "profile_forbidden", "Only an administrator can create profiles")
	case errors.Is(err, profile.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_profile", profileErrorMessage(err))
	case err != nil:
		a.internalError(w, "create profile", err)
	default:
		writeJSON(w, http.StatusCreated, newProfileResponse(created))
	}
}

func (a *API) updateProfile(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Name    *string        `json:"name,omitempty"`
		IsChild *bool          `json:"isChild,omitempty"`
		PIN     nullableString `json:"pin,omitempty"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	updated, err := a.profiles.Update(r.Context(), principal, r.PathValue("profileId"), profile.UpdateInput{
		Name: request.Name, IsChild: request.IsChild, PINSet: request.PIN.Set, PIN: request.PIN.Value,
	})
	switch {
	case errors.Is(err, profile.ErrNotFound):
		writeError(w, http.StatusNotFound, "profile_not_found", "The profile does not exist")
	case errors.Is(err, profile.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_profile", profileErrorMessage(err))
	case err != nil:
		a.internalError(w, "update profile", err)
	default:
		writeJSON(w, http.StatusOK, newProfileResponse(updated))
	}
}

func (a *API) deleteProfile(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.profiles.Delete(r.Context(), principal, r.PathValue("profileId"))
	switch {
	case errors.Is(err, profile.ErrNotFound):
		writeError(w, http.StatusNotFound, "profile_not_found", "The profile does not exist")
	case errors.Is(err, profile.ErrLastProfile):
		writeError(w, http.StatusConflict, "last_profile", "The final profile cannot be deleted")
	case err != nil:
		a.internalError(w, "delete profile", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) selectProfile(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		PIN *string `json:"pin,omitempty"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	selection, err := a.profiles.Select(r.Context(), principal, r.PathValue("profileId"), request.PIN)
	switch {
	case errors.Is(err, profile.ErrNotFound):
		writeError(w, http.StatusNotFound, "profile_not_found", "The profile does not exist")
	case errors.Is(err, profile.ErrInvalidPIN):
		writeError(w, http.StatusUnauthorized, "invalid_profile_pin", "A valid profile PIN is required")
	case errors.Is(err, profile.ErrForbidden):
		writeError(w, http.StatusUnauthorized, "invalid_access_token", "A valid access token is required")
	case err != nil:
		a.internalError(w, "select profile", err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"profile":   newProfileResponse(selection.Profile),
			"expiresAt": selection.ExpiresAt,
		})
	}
}

func (a *API) clearProfileSelection(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.profiles.ClearSelection(r.Context(), principal)
	switch {
	case errors.Is(err, profile.ErrForbidden):
		writeError(w, http.StatusUnauthorized, "invalid_access_token", "A valid access token is required")
	case err != nil:
		a.internalError(w, "clear profile selection", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func newProfileResponse(value profile.Profile) profileResponse {
	avatar := profileAvatarResponse{
		Kind: value.AvatarKind,
		URL:  "/api/v1/profiles/" + value.ID + "/avatar",
	}
	if value.AvatarKind != "custom" {
		avatar.Kind = "preset"
		avatar.PresetID = value.AvatarPreset
		avatar.URL = "/api/v1/profile-avatars/" + value.AvatarPreset
	}
	return profileResponse{
		ID: value.ID, Name: value.Name, IsChild: value.IsChild, HasPIN: value.HasPIN, CanManage: value.CanManage,
		Avatar: avatar,
	}
}

func profileErrorMessage(err error) string {
	return strings.TrimPrefix(err.Error(), profile.ErrInvalidInput.Error()+": ")
}
