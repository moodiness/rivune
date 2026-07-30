package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
)

func (a *API) beginDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		DeviceName string `json:"deviceName"`
		Platform   string `json:"platform"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	authorization, err := a.auth.BeginDeviceAuthorization(r.Context(), request.DeviceName, request.Platform)
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_device", authInputMessage(err))
	case err != nil:
		a.internalError(w, "begin device authorization", err)
	default:
		verificationURI := "/pair"
		if a.config.PublicURL != "" {
			verificationURI = a.config.PublicURL + verificationURI
		}
		completeURI, parseErr := url.Parse(verificationURI)
		if parseErr != nil {
			a.internalError(w, "build device verification URL", parseErr)
			return
		}
		query := completeURI.Query()
		query.Set("code", authorization.UserCode)
		completeURI.RawQuery = query.Encode()
		writeJSON(w, http.StatusCreated, map[string]any{
			"deviceCode":              authorization.DeviceCode,
			"userCode":                authorization.UserCode,
			"verificationUri":         verificationURI,
			"verificationUriComplete": completeURI.String(),
			"expiresAt":               authorization.ExpiresAt,
			"intervalSeconds":         int64(authorization.Interval.Seconds()),
		})
	}
}

func (a *API) approveDeviceAuthorization(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		UserCode string `json:"userCode"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	err := a.auth.ApproveDeviceAuthorization(r.Context(), principal, request.UserCode)
	switch {
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, "device_approval_forbidden", "Select a profile allowed to manage devices")
	case errors.Is(err, auth.ErrInvalidUserCode):
		writeError(w, http.StatusNotFound, "device_code_not_found", "The device code is invalid or expired")
	case errors.Is(err, auth.ErrDeviceAuthorizationClaimed):
		writeError(w, http.StatusConflict, "device_code_claimed", "The device code was approved by another account")
	case err != nil:
		a.internalError(w, "approve device authorization", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) exchangeDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		DeviceCode string `json:"deviceCode"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	tokens, err := a.auth.ExchangeDeviceAuthorization(r.Context(), request.DeviceCode)
	switch {
	case errors.Is(err, auth.ErrDeviceAuthorizationPending):
		writeError(w, http.StatusBadRequest, "authorization_pending", "The device authorization is still pending")
	case errors.Is(err, auth.ErrDeviceAuthorizationSlowDown):
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, "slow_down", "Wait before polling the device authorization again")
	case errors.Is(err, auth.ErrDeviceAuthorizationExpired):
		writeError(w, http.StatusBadRequest, "expired_device_code", "The device authorization has expired")
	case errors.Is(err, auth.ErrInvalidDeviceCode):
		writeError(w, http.StatusBadRequest, "invalid_device_code", "The device code is invalid")
	case err != nil:
		a.internalError(w, "exchange device authorization", err)
	default:
		writeJSON(w, http.StatusOK, newTokenResponse(tokens))
	}
}

func authInputMessage(err error) string {
	return strings.TrimPrefix(err.Error(), auth.ErrInvalidInput.Error()+": ")
}
