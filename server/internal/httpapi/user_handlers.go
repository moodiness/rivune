package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/user"
)

type userResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	users, err := a.users.List(r.Context(), principal)
	if writeUserError(a, w, err, "list users") {
		return
	}
	response := make([]userResponse, 0, len(users))
	for _, item := range users {
		response = append(response, newUserResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": response})
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role,omitempty"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := a.users.Create(r.Context(), principal, user.CreateInput{
		Username: request.Username, Password: request.Password, Role: request.Role,
	})
	if writeUserError(a, w, err, "create user") {
		return
	}
	writeJSON(w, http.StatusCreated, newUserResponse(created))
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Username *string `json:"username,omitempty"`
		Password *string `json:"password,omitempty"`
		Role     *string `json:"role,omitempty"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	updated, err := a.users.Update(r.Context(), principal, r.PathValue("userId"), user.UpdateInput{
		Username: request.Username, Password: request.Password, Role: request.Role,
	})
	if writeUserError(a, w, err, "update user") {
		return
	}
	writeJSON(w, http.StatusOK, newUserResponse(updated))
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if writeUserError(a, w, a.users.Delete(r.Context(), principal, r.PathValue("userId")), "delete user") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) userProfileAccess(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	access, err := a.users.ProfileAccess(r.Context(), principal, r.PathValue("userId"))
	if writeUserError(a, w, err, "list user profile access") {
		return
	}
	response := make([]map[string]any, 0, len(access))
	for _, item := range access {
		response = append(response, profileAccessResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": response})
}

func (a *API) grantUserProfileAccess(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		CanManage nullableBool `json:"canManage"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !request.CanManage.Set || request.CanManage.Value == nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_profile_access", "canManage must be a boolean")
		return
	}
	access, err := a.users.GrantProfileAccess(
		r.Context(), principal, r.PathValue("userId"), r.PathValue("profileId"), *request.CanManage.Value,
	)
	if writeUserError(a, w, err, "grant user profile access") {
		return
	}
	writeJSON(w, http.StatusOK, profileAccessResponse(access))
}

func (a *API) revokeUserProfileAccess(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.users.RevokeProfileAccess(r.Context(), principal, r.PathValue("userId"), r.PathValue("profileId"))
	if writeUserError(a, w, err, "revoke user profile access") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeUserError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, user.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_user", strings.TrimPrefix(err.Error(), user.ErrInvalidInput.Error()+": "))
	case errors.Is(err, user.ErrForbidden):
		writeError(w, http.StatusForbidden, "admin_required", "Administrator access is required")
	case errors.Is(err, user.ErrNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "The user does not exist")
	case errors.Is(err, user.ErrProfileNotFound):
		writeError(w, http.StatusNotFound, "profile_not_found", "The profile does not exist")
	case errors.Is(err, user.ErrAccessNotFound):
		writeError(w, http.StatusNotFound, "profile_access_not_found", "The profile access does not exist")
	case errors.Is(err, user.ErrUsernameConflict):
		writeError(w, http.StatusConflict, "username_conflict", "The username is already in use")
	case errors.Is(err, user.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", "The final administrator cannot be removed")
	case errors.Is(err, user.ErrSelfDeletion):
		writeError(w, http.StatusConflict, "self_deletion", "Administrators cannot delete their own account")
	default:
		a.internalError(w, operation, err)
	}
	return true
}

func newUserResponse(value user.User) userResponse {
	return userResponse{
		ID: value.ID, Username: value.Username, Role: value.Role,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func profileAccessResponse(value user.ProfileAccess) map[string]any {
	return map[string]any{
		"profileId": value.ProfileID, "profileName": value.ProfileName,
		"hasAccess": value.HasAccess, "canManage": value.CanManage,
	}
}
