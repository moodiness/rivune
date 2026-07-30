package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

type tokenResponse struct {
	TokenType             string    `json:"tokenType"`
	AccessToken           string    `json:"accessToken"`
	AccessTokenExpiresAt  time.Time `json:"accessTokenExpiresAt"`
	RefreshToken          string    `json:"refreshToken"`
	RefreshTokenExpiresAt time.Time `json:"refreshTokenExpiresAt"`
	SessionID             string    `json:"sessionId"`
	DeviceID              string    `json:"deviceId"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Device   struct {
			ID       string `json:"id,omitempty"`
			Name     string `json:"name"`
			Platform string `json:"platform"`
		} `json:"device"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	tokens, err := a.auth.Login(r.Context(), auth.LoginInput{
		Username:   request.Username,
		Password:   request.Password,
		DeviceID:   request.Device.ID,
		DeviceName: request.Device.Name,
		Platform:   request.Device.Platform,
	})
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "The username or password is invalid")
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_login", strings.TrimPrefix(err.Error(), auth.ErrInvalidInput.Error()+": "))
	case err != nil:
		a.internalError(w, "log in user", err)
	default:
		writeJSON(w, http.StatusOK, newTokenResponse(tokens))
	}
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	tokens, err := a.auth.Refresh(r.Context(), strings.TrimSpace(request.RefreshToken))
	switch {
	case errors.Is(err, auth.ErrInvalidToken):
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired")
	case err != nil:
		a.internalError(w, "refresh session", err)
	default:
		writeJSON(w, http.StatusOK, newTokenResponse(tokens))
	}
}

func (a *API) requireAuthentication(next func(http.ResponseWriter, *http.Request, auth.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := a.auth.Authenticate(r.Context(), bearerToken(r.Header.Get("Authorization")))
		switch {
		case errors.Is(err, auth.ErrInvalidToken):
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid_access_token", "A valid access token is required")
		case err != nil:
			a.internalError(w, "authenticate request", err)
		default:
			next(w, r, principal)
		}
	})
}

func (a *API) me(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	account, err := a.auth.Account(r.Context(), principal)
	if err != nil {
		a.internalError(w, "read authenticated account", err)
		return
	}

	profiles := make([]map[string]any, 0, len(account.Profiles))
	for _, profile := range account.Profiles {
		avatar := profileAvatarResponse{
			Kind: profile.AvatarKind,
			URL:  "/api/v1/profiles/" + profile.ID + "/avatar",
		}
		if profile.AvatarKind != "custom" {
			avatar.Kind = "preset"
			avatar.PresetID = profile.AvatarPreset
			avatar.URL = "/api/v1/profile-avatars/" + profile.AvatarPreset
		}
		profiles = append(profiles, map[string]any{
			"id":        profile.ID,
			"name":      profile.Name,
			"isChild":   profile.IsChild,
			"hasPin":    profile.HasPIN,
			"canManage": profile.CanManage,
			"avatar":    avatar,
		})
	}
	var activeProfile any
	if account.Principal.ActiveProfileID != nil && account.Principal.ProfileGrantExpiresAt != nil {
		activeProfile = map[string]any{
			"id":        *account.Principal.ActiveProfileID,
			"expiresAt": *account.Principal.ProfileGrantExpiresAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{
			"id":       account.Principal.UserID,
			"username": account.Principal.Username,
			"role":     account.Principal.Role,
		},
		"session": map[string]any{
			"id":            account.Principal.SessionID,
			"deviceId":      account.Principal.DeviceID,
			"activeProfile": activeProfile,
		},
		"profiles": profiles,
	})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if err := a.auth.Logout(r.Context(), principal); err != nil {
		a.internalError(w, "log out session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) sessions(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	sessions, err := a.auth.Sessions(r.Context(), principal)
	if err != nil {
		a.internalError(w, "list account sessions", err)
		return
	}

	response := make([]map[string]any, 0, len(sessions))
	for _, session := range sessions {
		response = append(response, map[string]any{
			"id":         session.ID,
			"deviceId":   session.DeviceID,
			"deviceName": session.DeviceName,
			"platform":   session.Platform,
			"ipAddress":  nullableSessionIPAddress(session.IPAddress),
			"createdAt":  session.CreatedAt,
			"lastSeenAt": session.LastSeenAt,
			"current":    session.Current,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": response})
}

func (a *API) revokeSession(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.auth.RevokeSession(r.Context(), principal, r.PathValue("sessionId"))
	switch {
	case errors.Is(err, auth.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "session_not_found", "The session does not exist")
	case err != nil:
		a.internalError(w, "revoke account session", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) profileSessions(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	sessions, err := a.auth.ProfileSessions(r.Context(), principal, r.PathValue("profileId"))
	switch {
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, "profile_session_forbidden", "You cannot manage sessions for this profile")
	case err != nil:
		a.internalError(w, "list profile sessions", err)
	default:
		response := make([]map[string]any, 0, len(sessions))
		for _, session := range sessions {
			response = append(response, map[string]any{
				"id":                    session.ID,
				"userId":                session.UserID,
				"username":              session.Username,
				"deviceId":              session.DeviceID,
				"deviceName":            session.DeviceName,
				"platform":              session.Platform,
				"ipAddress":             nullableSessionIPAddress(session.IPAddress),
				"createdAt":             session.CreatedAt,
				"lastSeenAt":            session.LastSeenAt,
				"profileGrantExpiresAt": session.ProfileGrantExpiresAt,
				"current":               session.Current,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": response})
	}
}

func (a *API) revokeProfileSession(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	err := a.auth.RevokeProfileSession(r.Context(), principal, r.PathValue("profileId"), r.PathValue("sessionId"))
	switch {
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, "profile_session_forbidden", "You cannot manage sessions for this profile")
	case errors.Is(err, auth.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "session_not_found", "The active profile session does not exist")
	case err != nil:
		a.internalError(w, "revoke profile session", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func nullableSessionIPAddress(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func newTokenResponse(tokens auth.TokenPair) tokenResponse {
	return tokenResponse{
		TokenType:             "Bearer",
		AccessToken:           tokens.AccessToken,
		AccessTokenExpiresAt:  tokens.AccessExpiresAt,
		RefreshToken:          tokens.RefreshToken,
		RefreshTokenExpiresAt: tokens.RefreshExpiresAt,
		SessionID:             tokens.SessionID,
		DeviceID:              tokens.DeviceID,
	}
}
