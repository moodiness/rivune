package jellyfin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	nativeauth "github.com/moodiness/rivune/server/internal/auth"
)

const (
	maximumCompatPasswordBytes         = 256
	maximumCompatCapabilitiesBodyBytes = 64 << 10
	maximumCompatBitrateTestBytes      = 4 << 20
	compatLoginRejectedMessage         = "jellyfin compatibility login rejected"
	compatLoginStageRequestBody        = "request_body"
	compatLoginStageUsernameShape      = "username_shape"
	compatLoginStagePasswordShape      = "password_shape"
	compatLoginStageClientMetadata     = "client_metadata"
	compatLoginStageCredentialPolicy   = "credential_or_policy"
	compatLoginStagePostLoginBinding   = "post_login_binding"
	compatSecretFailureAbsent          = "absent"
	compatSecretFailureAliasesDiffer   = "aliases_differ"
	compatSecretFailureInvalidUTF8     = "invalid_utf8"
	compatSecretFailureEmpty           = "empty"
	compatSecretFailureTooLong         = "too_long"
)

type authenticateByNameRequest struct {
	Username *string `json:"Username"`
	UserName *string `json:"UserName"`
	Pw       *string `json:"Pw"`
	Password *string `json:"Password"`
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
		handler.logCompatLoginRejection(compatLoginStageRequestBody)
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The request body is invalid")
		return
	}
	username, ok := compatAlias(payload.Username, payload.UserName)
	if !ok || !validCompatUUID(username) {
		handler.logCompatLoginRejection(compatLoginStageUsernameShape)
		writeCompatLoginFailure(response)
		return
	}
	password, passwordFailure := parseCompatSecret(payload.Pw, payload.Password)
	if passwordFailure != "" {
		handler.logCompatLoginRejection(compatLoginStagePasswordShape + ":" + passwordFailure)
		writeCompatLoginFailure(response)
		return
	}
	client, clientFailure, err := parseClientIdentity(request.Header)
	if err != nil {
		handler.logCompatLoginRejection(compatLoginStageClientMetadata + ":" + clientFailure)
		writeCompatLoginFailure(response)
		return
	}
	result, err := handler.authentication.Login(request.Context(), CompatLoginInput{
		Username: username, Password: password, Client: client,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCompatLogin) || errors.Is(err, ErrInvalidCompatCredential) || errors.Is(err, ErrInvalidCompatAuthorization) {
			handler.logCompatLoginRejection(compatLoginStageCredentialPolicy)
			writeCompatLoginFailure(response)
		} else {
			writeCompatError(response, http.StatusInternalServerError, "InternalError", "The request could not be completed")
		}
		return
	}
	if !validLoginResult(result) {
		handler.logCompatLoginRejection(compatLoginStagePostLoginBinding)
		writeCompatError(response, http.StatusInternalServerError, "InternalError", "The request could not be completed")
		return
	}
	serverID := handler.serverInfo.ID.String()
	writeJSON(response, http.StatusOK, AuthenticationResult{
		User: handler.configuredCompatUser(request.Context(), result.Principal, result.Profile.ID, result.Profile.Name),
		SessionInfo: SessionInfoDto{
			Id: result.Credential.SessionID, UserId: result.Profile.ID, UserName: result.Profile.Name,
			Client: client.Client, DeviceName: client.Device, DeviceId: client.DeviceID, ApplicationVersion: client.Version,
		},
		AccessToken: result.Credential.Token,
		ServerId:    serverID,
	})
}

func (handler *Handler) logCompatLoginRejection(stage string) {
	if handler == nil || handler.logger == nil {
		return
	}
	handler.logger.LogAttrs(
		context.Background(),
		slog.LevelInfo,
		compatLoginRejectedMessage,
		slog.String("stage", stage),
	)
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
	writeJSON(response, http.StatusOK, handler.userForSession(request.Context(), session))
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
	writeJSON(response, http.StatusOK, handler.userForSession(request.Context(), session))
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

func (handler *Handler) handleSessionCapabilitiesFull(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	if request.ContentLength > maximumCompatCapabilitiesBodyBytes {
		writeCompatError(response, http.StatusRequestEntityTooLarge, "InvalidRequest", "The request body is too large")
		return
	}
	if request.Body != nil {
		read, err := io.Copy(io.Discard, io.LimitReader(request.Body, maximumCompatCapabilitiesBodyBytes+1))
		if err != nil {
			writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The request body is invalid")
			return
		}
		if read > maximumCompatCapabilitiesBodyBytes {
			writeCompatError(response, http.StatusRequestEntityTooLarge, "InvalidRequest", "The request body is too large")
			return
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) handleSyncPlayList(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	writeJSON(response, http.StatusOK, []struct{}{})
}

func (handler *Handler) handlePlaybackBitrateTest(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRequest(response, request, false); !ok {
		return
	}
	size, err := boundedInteger(request.URL.Query(), "Size", 1, maximumCompatBitrateTestBytes, 0)
	if err != nil || size == 0 {
		writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The bitrate test size is invalid")
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Content-Length", strconv.Itoa(size))
	response.WriteHeader(http.StatusOK)
	var zeroes [32 << 10]byte
	for remaining := size; remaining > 0; {
		chunk := min(remaining, len(zeroes))
		written, writeErr := response.Write(zeroes[:chunk])
		if writeErr != nil || written == 0 {
			return
		}
		remaining -= written
	}
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

func (handler *Handler) handleBrandingConfiguration(response http.ResponseWriter, _ *http.Request) {
	if _, ok := handler.publicSystemInfo(); !ok {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, struct{}{})
}

func (handler *Handler) handleBrandingSplashscreen(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.publicSystemInfo(); !ok {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
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

func (handler *Handler) userForSession(ctx context.Context, session AuthenticatedSession) UserDto {
	return handler.configuredCompatUser(ctx, session.Principal, session.ProfileID, session.ProfileName)
}

func (handler *Handler) configuredCompatUser(ctx context.Context, principal nativeauth.Principal, profileID, profileName string) UserDto {
	user := handler.newCompatUser(profileID, profileName)
	if handler == nil || handler.collections == nil {
		return user
	}
	views, viewsOK := handler.virtualViews()
	values, err := handler.collections.List(ctx, principal)
	if !viewsOK || err != nil {
		return user
	}
	user.Configuration.OrderedViews = make([]string, 0, len(views)+len(values))
	for _, view := range views {
		user.Configuration.OrderedViews = append(user.Configuration.OrderedViews, view.Id)
	}
	user.Configuration.MyMediaExcludes = make([]string, 0, len(values))
	for _, value := range values {
		user.Configuration.OrderedViews = append(user.Configuration.OrderedViews, value.ID)
		user.Configuration.MyMediaExcludes = append(user.Configuration.MyMediaExcludes, value.ID)
	}
	return user
}

func (handler *Handler) newCompatUser(profileID, profileName string) UserDto {
	playbackEnabled := handler != nil && handler.catalog != nil && handler.playSessions != nil
	return UserDto{
		Name: profileName, ServerId: handler.serverInfo.ID.String(), Id: profileID,
		HasPassword: true, HasConfiguredPassword: true, HasConfiguredEasyPassword: false,
		Policy: UserPolicy{
			EnablePlayback:                 playbackEnabled,
			EnableAudioPlaybackTranscoding: playbackEnabled,
			EnableVideoPlaybackTranscoding: playbackEnabled,
		},
		Configuration: UserConfiguration{
			OrderedViews: []string{}, LatestItemsExcludes: []string{}, MyMediaExcludes: []string{}, GroupedFolders: []string{},
		},
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

func parseCompatSecret(pw, password *string) (string, string) {
	if pw == nil && password == nil {
		return "", compatSecretFailureAbsent
	}
	if pw != nil && password != nil && *pw != *password {
		_, pwCanonical := nativeauth.JellyfinAppPasswordDigest(*pw)
		_, passwordCanonical := nativeauth.JellyfinAppPasswordDigest(*password)
		switch {
		case pwCanonical && !passwordCanonical:
			password = pw
		case passwordCanonical && !pwCanonical:
			pw = password
		case *pw == "":
			pw = password
		case *password == "":
			password = pw
		default:
			return "", compatSecretFailureAliasesDiffer
		}
	}
	secret := password
	if pw != nil {
		secret = pw
	}
	if !utf8.ValidString(*secret) {
		return "", compatSecretFailureInvalidUTF8
	}
	if len(*secret) == 0 {
		return "", compatSecretFailureEmpty
	}
	if len(*secret) > maximumCompatPasswordBytes {
		return "", compatSecretFailureTooLong
	}
	return *secret, ""
}

func writeCompatLoginFailure(response http.ResponseWriter) {
	writeCompatError(response, http.StatusUnauthorized, "InvalidCredentials", "The supplied credentials are invalid")
}
