package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maximumCompatJSONBodyBytes     int64 = 16 << 10
	maximumCompatJSONResponseBytes       = 4 << 20
	maximumCompatErrorCodeRunes          = 64
	maximumCompatErrorMessageRunes       = 256
)

var (
	ErrInvalidCompatRequest           = errors.New("invalid compatibility request")
	ErrCompatMaintenanceMode          = errors.New("compatibility request denied by maintenance mode")
	ErrCompatRequestPolicyUnavailable = errors.New("compatibility request policy unavailable")
)

const defaultCompatMaintenanceMessage = "Rivune is temporarily unavailable for maintenance."

type authenticatedPolicyExemptionKey struct{}

type policyAuthentication struct {
	next   Authentication
	policy AuthenticatedRequestPolicy
}

type compatMaintenanceModeError struct {
	publicMessage string
}

func (err *compatMaintenanceModeError) Error() string {
	return ErrCompatMaintenanceMode.Error()
}

func (err *compatMaintenanceModeError) Unwrap() error {
	return ErrCompatMaintenanceMode
}

func newPolicyAuthentication(next Authentication, policy AuthenticatedRequestPolicy) Authentication {
	if next == nil || policy == nil {
		return next
	}
	return &policyAuthentication{next: next, policy: policy}
}

func (authentication *policyAuthentication) Login(ctx context.Context, input CompatLoginInput) (LoginResult, error) {
	return authentication.next.Login(ctx, input)
}

func (authentication *policyAuthentication) Authenticate(ctx context.Context, token string) (AuthenticatedSession, error) {
	session, err := authentication.next.Authenticate(ctx, token)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	if err := authentication.authorize(ctx, session); err != nil {
		return AuthenticatedSession{}, err
	}
	return session, nil
}

func (authentication *policyAuthentication) Revalidate(ctx context.Context, expected AuthenticatedSession) (AuthenticatedSession, error) {
	session, err := authentication.next.Revalidate(ctx, expected)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	if err := authentication.authorize(ctx, session); err != nil {
		return AuthenticatedSession{}, err
	}
	return session, nil
}

func (authentication *policyAuthentication) Logout(ctx context.Context, session AuthenticatedSession) error {
	return authentication.next.Logout(ctx, session)
}

func (authentication *policyAuthentication) authorize(ctx context.Context, session AuthenticatedSession) error {
	if exempt, _ := ctx.Value(authenticatedPolicyExemptionKey{}).(bool); exempt {
		return nil
	}
	result, err := authentication.policy.Authorize(ctx, session.Principal)
	if err != nil {
		return errors.Join(ErrCompatRequestPolicyUnavailable, err)
	}
	if result.Allowed {
		return nil
	}
	message := ""
	if result.PublicMessage != nil {
		message = *result.PublicMessage
	}
	return &compatMaintenanceModeError{publicMessage: message}
}

func withAuthenticatedPolicyExemption(request *http.Request) *http.Request {
	if request == nil {
		return nil
	}
	return request.WithContext(context.WithValue(request.Context(), authenticatedPolicyExemptionKey{}, true))
}

func compatMaintenancePublicMessage(err error) string {
	var maintenanceErr *compatMaintenanceModeError
	if !errors.As(err, &maintenanceErr) {
		return defaultCompatMaintenanceMessage
	}
	return publicCompatErrorValue(maintenanceErr.publicMessage, defaultCompatMaintenanceMessage, maximumCompatErrorMessageRunes)
}

func compatRequestPolicyResponse(err error) (string, string, bool) {
	switch {
	case errors.Is(err, ErrCompatMaintenanceMode):
		return "MaintenanceMode", compatMaintenancePublicMessage(err), true
	case errors.Is(err, ErrCompatRequestPolicyUnavailable):
		return "ServiceUnavailable", "The compatibility authorization service is unavailable", true
	default:
		return "", "", false
	}
}

func writeCompatRequestPolicyError(response http.ResponseWriter, err error) bool {
	code, message, ok := compatRequestPolicyResponse(err)
	if !ok {
		return false
	}
	response.Header().Set("Retry-After", "5")
	response.Header().Set("Cache-Control", "no-store")
	writeCompatError(response, http.StatusServiceUnavailable, code, message)
	return true
}

func (handler *Handler) writeCompatStreamRequestPolicyError(response http.ResponseWriter, request *http.Request, err error) bool {
	code, message, ok := compatRequestPolicyResponse(err)
	if !ok {
		return false
	}
	response.Header().Set("Retry-After", "5")
	response.Header().Set("Cache-Control", "no-store")
	handler.writeStreamError(response, request, http.StatusServiceUnavailable, code, message)
	return true
}

type CompatErrorResponse struct {
	ResponseStatus CompatErrorStatus `json:"ResponseStatus"`
}

type CompatErrorStatus struct {
	ErrorCode string `json:"ErrorCode"`
	Message   string `json:"Message"`
}

func decodeCompatJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	if response == nil || request == nil || request.Body == nil || destination == nil {
		return ErrInvalidCompatRequest
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return ErrInvalidCompatRequest
	}
	if request.ContentLength > maximumCompatJSONBodyBytes {
		return ErrInvalidCompatRequest
	}
	limited := http.MaxBytesReader(response, request.Body, maximumCompatJSONBodyBytes)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalidCompatRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidCompatRequest
	}
	return nil
}

func (handler *Handler) authenticateRequest(response http.ResponseWriter, request *http.Request, allowQuery bool) (AuthenticatedSession, bool) {
	if handler == nil || handler.authentication == nil || request == nil {
		writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility service is unavailable")
		return AuthenticatedSession{}, false
	}
	token, err := ParseCompatToken(request, allowQuery)
	if err != nil {
		writeCompatError(response, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
		return AuthenticatedSession{}, false
	}
	session, err := handler.authentication.Authenticate(request.Context(), token)
	if err != nil {
		if request.Context().Err() != nil {
			return AuthenticatedSession{}, false
		}
		if writeCompatRequestPolicyError(response, err) {
			return AuthenticatedSession{}, false
		}
		if errors.Is(err, ErrCompatAuthenticationSaturated) {
			writeCompatError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility authentication service is busy")
		} else if errors.Is(err, ErrInvalidCompatCredential) || errors.Is(err, ErrInvalidCompatAuthorization) {
			writeCompatError(response, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
		} else {
			writeCompatError(response, http.StatusInternalServerError, "InternalError", "The request could not be completed")
		}
		return AuthenticatedSession{}, false
	}
	if !validAuthenticatedSession(session) {
		writeCompatError(response, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
		return AuthenticatedSession{}, false
	}
	if !requestUserMatchesSession(request, session.ProfileID) {
		writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "The requested resource was not found")
		return AuthenticatedSession{}, false
	}
	if handler.bootstrap != nil {
		handler.bootstrap.observe(session)
	}
	return session, true
}

func validAuthenticatedSession(session AuthenticatedSession) bool {
	if !boundedUTF8(session.ID, 1, 64) || !boundedUTF8(session.Principal.SessionID, 1, 64) ||
		!boundedUTF8(session.Principal.UserID, 1, 64) || session.Principal.ActiveProfileID == nil ||
		!strings.EqualFold(*session.Principal.ActiveProfileID, session.ProfileID) || !boundedUTF8(session.ProfileName, 1, 120) {
		return false
	}
	_, err := parseUUID(session.ProfileID)
	return err == nil
}

func validCompatUUID(value string) bool {
	_, err := parseUUID(value)
	return err == nil
}

func requestUserMatchesSession(request *http.Request, profileID string) bool {
	parameters, found, err := firstAuthorizationParameters(request.Header)
	if err != nil {
		return false
	}
	if found {
		if headerUserID, exists := parameters["userid"]; exists && !sameCompatUUID(headerUserID, profileID) {
			return false
		}
	}
	query := request.URL.Query()
	for key, values := range query {
		if !strings.EqualFold(key, "UserId") {
			continue
		}
		if len(values) != 1 || !sameCompatUUID(values[0], profileID) {
			return false
		}
	}
	return true
}

func sameCompatUUID(left, right string) bool {
	leftID, leftErr := parseUUID(strings.TrimSpace(left))
	rightID, rightErr := parseUUID(strings.TrimSpace(right))
	return leftErr == nil && rightErr == nil && leftID == rightID
}

func canonicalCompatUUID(value string) (string, bool) {
	parsed, err := parseUUID(strings.TrimSpace(value))
	if err != nil {
		return "", false
	}
	return formatUUID(parsed), true
}

func writeCompatError(response http.ResponseWriter, status int, code, message string) {
	if response == nil {
		return
	}
	if status == http.StatusUnauthorized {
		response.Header().Set("WWW-Authenticate", "MediaBrowser")
	}
	code = publicCompatErrorValue(code, "Error", maximumCompatErrorCodeRunes)
	message = publicCompatErrorValue(message, "The request could not be completed", maximumCompatErrorMessageRunes)
	writeJSON(response, status, CompatErrorResponse{ResponseStatus: CompatErrorStatus{ErrorCode: code, Message: message}})
}

func (handler *Handler) writeCompatError(response http.ResponseWriter, status int, code, message string) {
	writeCompatError(response, status, code, message)
}

func (handler *Handler) writeJSON(response http.ResponseWriter, status int, value any) {
	writeJSON(response, status, value)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	if response == nil {
		return
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > maximumCompatJSONResponseBytes {
		status = http.StatusInternalServerError
		payload = []byte(`{"ResponseStatus":{"ErrorCode":"InternalError","Message":"The request could not be completed"}}`)
	}
	payload = append(payload, '\n')
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = response.Write(payload)
}

func publicCompatErrorValue(value, fallback string, maximumRunes int) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > maximumRunes ||
		strings.Contains(lower, "rivune_") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "api_key") || strings.Contains(lower, "token=") || strings.Contains(lower, "cookie") ||
		strings.Contains(lower, "http://") || strings.Contains(lower, "https://") {
		return fallback
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return fallback
		}
	}
	return value
}
