package httpapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/jellyfin"
)

type jellyfinCredentialResponse struct {
	Username   *string    `json:"username,omitempty"`
	Password   string     `json:"password,omitempty"`
	Active     bool       `json:"active"`
	CanIssue   bool       `json:"canIssue"`
	Generation int64      `json:"generation"`
	CreatedAt  *time.Time `json:"createdAt,omitempty"`
	RotatedAt  *time.Time `json:"rotatedAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

func (a *API) jellyfinCredentialStatus(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	status, err := a.jellyfinCredentials.Status(r.Context(), principal, r.PathValue("profileId"))
	if writeJellyfinCredentialError(a, w, err, "read Jellyfin profile credential") {
		return
	}
	writeJSON(w, http.StatusOK, newJellyfinCredentialResponse(status, ""))
}

func (a *API) createJellyfinCredential(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireEmptyJellyfinCredentialBody(w, r) {
		return
	}
	credential, err := a.jellyfinCredentials.Create(r.Context(), principal, r.PathValue("profileId"))
	if writeJellyfinCredentialError(a, w, err, "create Jellyfin profile credential") {
		return
	}
	writeJellyfinCredentialSecret(w, http.StatusCreated, credential)
}

func (a *API) rotateJellyfinCredential(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireEmptyJellyfinCredentialBody(w, r) {
		return
	}
	credential, err := a.jellyfinCredentials.Rotate(r.Context(), principal, r.PathValue("profileId"))
	if writeJellyfinCredentialError(a, w, err, "rotate Jellyfin profile credential") {
		return
	}
	writeJellyfinCredentialSecret(w, http.StatusOK, credential)
}

func (a *API) revokeJellyfinCredential(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.jellyfinCredentials.Revoke(r.Context(), principal, r.PathValue("profileId"))
	if writeJellyfinCredentialError(a, w, err, "revoke Jellyfin profile credential") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJellyfinCredentialSecret(w http.ResponseWriter, statusCode int, credential jellyfin.ProfileCredential) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, statusCode, newJellyfinCredentialResponse(credential.CredentialStatus, credential.Password))
}

func newJellyfinCredentialResponse(status jellyfin.CredentialStatus, password string) jellyfinCredentialResponse {
	response := jellyfinCredentialResponse{
		Password: password, Active: status.Active, CanIssue: status.CanIssue, Generation: status.Generation,
		RotatedAt: status.RotatedAt, LastUsedAt: status.LastUsedAt, RevokedAt: status.RevokedAt,
	}
	if status.Username != "" {
		username := status.Username
		createdAt := status.CreatedAt
		response.Username = &username
		response.CreatedAt = &createdAt
	}
	return response
}

func writeJellyfinCredentialError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, jellyfin.ErrCredentialForbidden):
		writeError(w, http.StatusForbidden, "jellyfin_credential_forbidden", "The profile credential cannot be managed by this session")
	case errors.Is(err, jellyfin.ErrCredentialNotFound):
		writeError(w, http.StatusNotFound, "jellyfin_credential_not_found", "The profile does not have an active Jellyfin credential")
	case errors.Is(err, jellyfin.ErrCredentialExists):
		writeError(w, http.StatusConflict, "jellyfin_credential_exists", "The profile already has an active Jellyfin credential")
	default:
		a.internalError(w, operation, err)
	}
	return true
}

func requireEmptyJellyfinCredentialBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	var buffer [1]byte
	read, err := r.Body.Read(buffer[:])
	if read != 0 || err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request body must be empty")
		return false
	}
	return true
}
