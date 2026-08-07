package jellyfin

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const maximumCompatPasswordBytes = 256

type authenticateByNameRequest struct {
	Username   *string `json:"Username"`
	UserName   *string `json:"UserName"`
	Pw         *string `json:"Pw"`
	Password   *string `json:"Password"`
	ProfilePin *string `json:"ProfilePin"`
}

func (handler *Handler) handlePublicSystemInfo(response http.ResponseWriter, _ *http.Request) {
	info, ok := handler.publicSystemInfo()
	if !ok {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, info)
}

func (handler *Handler) handleSystemPing(response http.ResponseWriter, _ *http.Request) {
	if _, ok := handler.publicSystemInfo(); !ok {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, CompatibilityProduct)
}

func (handler *Handler) handleSystemEndpoint(response http.ResponseWriter, _ *http.Request) {
	if _, ok := handler.publicSystemInfo(); !ok {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, SystemEndpointInfo{IsLocal: false, IsInNetwork: false})
}

func (handler *Handler) handleQuickConnectEnabled(response http.ResponseWriter, _ *http.Request) {
	if _, ok := handler.publicSystemInfo(); !ok {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, false)
}

func (handler *Handler) handleAuthenticateByName(response http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.authentication == nil {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return
	}
	if _, ok := handler.publicSystemInfo(); !ok {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return
	}
	var payload authenticateByNameRequest
	if err := decodeCompatJSON(response, request, &payload); err != nil {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The request body is invalid")
		return
	}
	username, ok := compatAlias(payload.Username, payload.UserName)
	if !ok || !boundedUTF8(username, 1, 256) {
		writeCompatLoginFailure(response)
		return
	}
	password, ok := compatSecretAlias(payload.Pw, payload.Password)
	if !ok || !utf8.ValidString(password) || len(password) < 1 || len(password) > maximumCompatPasswordBytes {
		writeCompatLoginFailure(response)
		return
	}
	var profilePIN *string
	if payload.ProfilePin != nil {
		pin := strings.TrimSpace(*payload.ProfilePin)
		if !validCompatPIN(pin) {
			writeCompatLoginFailure(response)
			return
		}
		profilePIN = &pin
	}
	client, err := ParseClientIdentity(request.Header)
	if err != nil {
		writeCompatLoginFailure(response)
		return
	}
	result, err := handler.authentication.Login(request.Context(), CompatLoginInput{
		Username: username, Password: password, ProfilePIN: profilePIN, Client: client,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCompatLogin) || errors.Is(err, ErrInvalidCompatCredential) || errors.Is(err, ErrInvalidCompatAuthorization) {
			writeCompatLoginFailure(response)
		} else {
			writeCompatError(response, http.StatusInternalServerError, "InternalError", "The request could not be completed")
		}
		return
	}
	if !validLoginResult(result) {
		writeCompatError(response, http.StatusInternalServerError, "InternalError", "The request could not be completed")
		return
	}
	serverID := handler.serverInfo.ID.String()
	writeJSON(response, http.StatusOK, AuthenticationResult{
		User: handler.newCompatUser(result.Profile.ID, result.Profile.Name, result.Profile.HasPIN),
		SessionInfo: SessionInfoDto{
			Id: result.Credential.SessionID, UserId: result.Profile.ID, UserName: result.Profile.Name,
			Client: client.Client, DeviceName: client.Device, DeviceId: client.DeviceID, ApplicationVersion: client.Version,
		},
		AccessToken: result.Credential.Token,
		ServerId:    serverID,
	})
}

func (handler *Handler) handlePublicUsers(response http.ResponseWriter, _ *http.Request) {
	if _, ok := handler.publicSystemInfo(); !ok {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, []UserDto{})
}

func (handler *Handler) handleSystemInfo(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	info, ok := handler.publicSystemInfo()
	if !ok {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, SystemInfo{PublicSystemInfo: info})
}

func (handler *Handler) handleCurrentUser(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, handler.userForSession(session))
}

func (handler *Handler) handleUser(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(request.PathValue("id")), session.ProfileID) {
		writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The requested resource was not found")
		return
	}
	writeJSON(response, http.StatusOK, handler.userForSession(session))
}

func (handler *Handler) handleLogout(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	if err := handler.authentication.Logout(request.Context(), session); err != nil {
		if errors.Is(err, ErrInvalidCompatCredential) {
			writeCompatError(response, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
		} else {
			writeCompatError(response, http.StatusInternalServerError, "InternalError", "The request could not be completed")
		}
		return
	}
	if handler.playSessions != nil {
		_ = handler.playSessions.closeSession(request.Context(), session)
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) handlePlugins(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	writeJSON(response, http.StatusOK, []struct{}{})
}

func (handler *Handler) handlePackages(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	writeJSON(response, http.StatusOK, []struct{}{})
}

func (handler *Handler) handleBrandingConfiguration(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	writeJSON(response, http.StatusOK, struct{}{})
}

func (handler *Handler) handleBrandingSplashscreen(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) handleDisplayPreferences(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	preferenceID := strings.TrimSpace(request.PathValue("id"))
	if !boundedUTF8(preferenceID, 1, 128) {
		writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The requested resource was not found")
		return
	}
	writeJSON(response, http.StatusOK, DisplayPreferencesDto{Id: preferenceID, CustomPrefs: map[string]string{}})
}

func (handler *Handler) publicSystemInfo() (PublicSystemInfo, bool) {
	if handler == nil || handler.serverInfo.ID.value == (uuidValue{}) || !boundedUTF8(handler.serverInfo.Name, 1, 80) {
		return PublicSystemInfo{}, false
	}
	return PublicSystemInfo{
		Id: handler.serverInfo.ID.String(), ServerName: handler.serverInfo.Name,
		Version: CompatibilityVersion, ProductName: CompatibilityProduct, StartupWizardCompleted: true,
	}, true
}

func (handler *Handler) userForSession(session AuthenticatedSession) UserDto {
	return handler.newCompatUser(session.ProfileID, session.ProfileName, session.ProfileHasPIN)
}

func (handler *Handler) newCompatUser(profileID, profileName string, hasPIN bool) UserDto {
	playbackEnabled := handler != nil && handler.catalog != nil && handler.playSessions != nil
	return UserDto{
		Name: profileName, ServerId: handler.serverInfo.ID.String(), Id: profileID,
		HasPassword: true, HasConfiguredPassword: true, HasConfiguredEasyPassword: hasPIN,
		Policy: UserPolicy{
			EnablePlayback:                 playbackEnabled,
			EnableAudioPlaybackTranscoding: playbackEnabled,
			EnableVideoPlaybackTranscoding: playbackEnabled,
		},
		Configuration: UserConfiguration{},
	}
}

func validLoginResult(result LoginResult) bool {
	if _, ok := compatCredentialDigest(result.Credential.Token); !ok {
		return false
	}
	if !validCompatUUID(result.Credential.SessionID) || !result.Credential.ExpiresAt.After(time.Now().UTC()) ||
		!boundedUTF8(result.Profile.Name, 1, 120) || result.Principal.ActiveProfileID == nil ||
		!strings.EqualFold(*result.Principal.ActiveProfileID, result.Profile.ID) {
		return false
	}
	return validCompatUUID(result.Profile.ID)
}

func compatAlias(first, second *string) (string, bool) {
	if first == nil && second == nil {
		return "", false
	}
	if first != nil && second != nil && *first != *second {
		return "", false
	}
	if first != nil {
		return strings.TrimSpace(*first), true
	}
	return strings.TrimSpace(*second), true
}

func compatSecretAlias(first, second *string) (string, bool) {
	if first == nil && second == nil {
		return "", false
	}
	if first != nil && second != nil && *first != *second {
		return "", false
	}
	if first != nil {
		return *first, true
	}
	return *second, true
}

func validCompatPIN(pin string) bool {
	if len(pin) < 4 || len(pin) > 8 {
		return false
	}
	for _, current := range pin {
		if current < '0' || current > '9' {
			return false
		}
	}
	return true
}

func writeCompatLoginFailure(response http.ResponseWriter) {
	writeCompatError(response, http.StatusUnauthorized, "InvalidCredentials", "The supplied credentials or profile selection are invalid")
}
