package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

const defaultMaintenanceMessage = "Rivune is temporarily unavailable for maintenance."

type tokenResponse struct {
	TokenType             string    `json:"tokenType"`
	AccessToken           string    `json:"accessToken"`
	AccessTokenExpiresAt  time.Time `json:"accessTokenExpiresAt"`
	RefreshToken          string    `json:"refreshToken"`
	RefreshTokenExpiresAt time.Time `json:"refreshTokenExpiresAt"`
	SessionID             string    `json:"sessionId"`
	DeviceID              string    `json:"deviceId"`
}

type sessionNotificationResponse struct {
	ID             string    `json:"id"`
	Message        string    `json:"message"`
	SenderUsername string    `json:"senderUsername"`
	CreatedAt      time.Time `json:"createdAt"`
}

type notificationBroadcastResponse struct {
	ID             string    `json:"id"`
	Message        string    `json:"message"`
	SenderUsername string    `json:"senderUsername"`
	RecipientCount int64     `json:"recipientCount"`
	CreatedAt      time.Time `json:"createdAt"`
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
			return
		case err != nil:
			a.internalError(w, "authenticate request", err)
			return
		}
		if !principal.ActiveProfileCanManage && !maintenanceExemptRequest(r) && a.rejectMaintenanceRequest(w, r) {
			return
		}
		next(w, r, principal)
	})
}

func maintenanceExemptRequest(r *http.Request) bool {
	path := r.URL.Path
	if path == "/api/v1/auth/logout" || path == "/api/v1/auth/me" || path == "/api/v1/profiles/selection" {
		return true
	}
	if path == "/api/v1/operations" || strings.HasPrefix(path, "/api/v1/operations/") {
		return true
	}
	if r.Method == http.MethodGet && (path == "/api/v1/profiles" || strings.HasSuffix(path, "/avatar") && strings.HasPrefix(path, "/api/v1/profiles/")) {
		return true
	}
	return r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/profiles/") && strings.HasSuffix(path, "/select")
}

func (a *API) maintenanceStatus(w http.ResponseWriter, r *http.Request) (bool, *string, bool) {
	if a.settings == nil {
		return false, nil, true
	}
	state, err := a.settings.Maintenance(r.Context())
	if err != nil {
		a.internalError(w, "read maintenance mode", err)
		return false, nil, false
	}
	return state.Enabled, state.Message, true
}

func writeMaintenanceMode(w http.ResponseWriter, message *string) {
	w.Header().Set("Retry-After", "5")
	writeJSON(w, http.StatusServiceUnavailable, errorEnvelope{Error: apiError{
		Code:          "maintenance_mode",
		Message:       defaultMaintenanceMessage,
		PublicMessage: message,
	}})
}

func (a *API) rejectMaintenanceRequest(w http.ResponseWriter, r *http.Request) bool {
	enabled, message, ok := a.maintenanceStatus(w, r)
	if !ok {
		return true
	}
	if !enabled {
		return false
	}
	writeMaintenanceMode(w, message)
	return true
}

func (a *API) me(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	account, err := a.auth.Account(r.Context(), principal)
	if err != nil {
		a.internalError(w, "read authenticated account", err)
		return
	}

	maintenance := map[string]any{"enabled": false, "message": nil}
	if a.settings != nil {
		state, err := a.settings.Maintenance(r.Context())
		if err != nil {
			a.internalError(w, "read maintenance mode", err)
			return
		}
		maintenance = newMaintenanceSettingsResponse(state)
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
			"id":              profile.ID,
			"name":            profile.Name,
			"isChild":         profile.IsChild,
			"hasPin":          profile.HasPIN,
			"canManage":       profile.CanManage,
			"enabled":         profile.Enabled,
			"availableFrom":   profile.AvailableFrom,
			"availableUntil":  profile.AvailableUntil,
			"accessStartTime": profile.AccessStartTime,
			"accessEndTime":   profile.AccessEndTime,
			"accessTimezone":  profile.AccessTimezone,
			"accessible":      profile.Accessible,
			"avatar":          avatar,
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
		"profiles":    profiles,
		"maintenance": maintenance,
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

func (a *API) sessionNotifications(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	var afterID int64
	if values, exists := r.URL.Query()["after"]; exists {
		if len(values) != 1 || values[0] == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "The notification cursor is invalid")
			return
		}
		raw := values[0]
		for index := range raw {
			if raw[index] < '0' || raw[index] > '9' {
				writeError(w, http.StatusBadRequest, "invalid_request", "The notification cursor is invalid")
				return
			}
		}
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "The notification cursor is invalid")
			return
		}
		afterID = parsed
	}
	notifications, err := a.auth.SessionNotifications(r.Context(), principal, afterID)
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "The notification cursor is invalid")
	case err != nil:
		a.internalError(w, "list session notifications", err)
	default:
		response := make([]sessionNotificationResponse, len(notifications))
		for index, notification := range notifications {
			response[index] = newSessionNotificationResponse(notification)
		}
		writeJSON(w, http.StatusOK, map[string]any{"notifications": response})
	}
}

func (a *API) acknowledgeSessionNotification(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	rawID := r.PathValue("notificationId")
	validID := rawID != ""
	for index := range rawID {
		validID = validID && rawID[index] >= '0' && rawID[index] <= '9'
	}
	notificationID, err := strconv.ParseInt(rawID, 10, 64)
	if !validID || err != nil || notificationID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "The notification identifier is invalid")
		return
	}
	err = a.auth.AcknowledgeSessionNotification(r.Context(), principal, notificationID)
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "The notification identifier is invalid")
	case errors.Is(err, auth.ErrNotificationNotFound):
		writeError(w, http.StatusNotFound, "notification_not_found", "The session notification does not exist")
	case err != nil:
		a.internalError(w, "acknowledge session notification", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) broadcastSessionNotification(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		IDempotencyKey string `json:"idempotencyKey"`
		Message        string `json:"message"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	broadcast, err := a.auth.BroadcastSessionNotification(r.Context(), principal, request.IDempotencyKey, request.Message)
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "The idempotency key must be a UUID and the message must contain between 1 and 500 characters")
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, "admin_required", "Administrator access is required")
	case err != nil:
		a.internalError(w, "broadcast session notification", err)
	default:
		writeJSON(w, http.StatusCreated, notificationBroadcastResponse{
			ID: broadcast.ID, Message: broadcast.Message, SenderUsername: broadcast.SenderUsername,
			RecipientCount: broadcast.RecipientCount, CreatedAt: broadcast.CreatedAt,
		})
	}
}

func (a *API) sendProfileSessionNotification(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Message string `json:"message"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	notification, err := a.auth.SendProfileSessionNotification(
		r.Context(), principal, r.PathValue("profileId"), r.PathValue("sessionId"), request.Message,
	)
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "The message must contain between 1 and 500 characters")
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, "profile_session_forbidden", "You cannot message sessions for this profile")
	case errors.Is(err, auth.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "session_not_found", "The active profile session does not exist")
	case err != nil:
		a.internalError(w, "send profile session notification", err)
	default:
		writeJSON(w, http.StatusCreated, newSessionNotificationResponse(notification))
	}
}

func newSessionNotificationResponse(notification auth.SessionNotification) sessionNotificationResponse {
	return sessionNotificationResponse{
		ID: strconv.FormatInt(notification.ID, 10), Message: notification.Message,
		SenderUsername: notification.SenderUsername, CreatedAt: notification.CreatedAt,
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
