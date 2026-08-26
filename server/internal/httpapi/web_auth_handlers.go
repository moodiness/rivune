package httpapi

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	webRefreshCookieName = "rivune_web_refresh"
	webRefreshCookiePath = "/api/v1/auth/web/refresh"
	webCSRFHeader         = "X-Rivune-CSRF"
)

type webTokenResponse struct {
	TokenType            string                  `json:"tokenType"`
	AccessToken          string                  `json:"accessToken"`
	AccessTokenExpiresAt time.Time               `json:"accessTokenExpiresAt"`
	SessionID            string                  `json:"sessionId"`
	DeviceID             string                  `json:"deviceId"`
	AuthorizationScope   auth.AuthorizationScope `json:"authorizationScope"`
	Category             any                     `json:"category"`
}

func (a *API) webLogin(w http.ResponseWriter, r *http.Request) {
	secure, ok := a.requireWebAuthRequest(w, r)
	if !ok {
		return
	}
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

	tokens, err := a.loginCredentialsWeb(r, auth.LoginInput{
		Username: request.Username, Password: request.Password, DeviceID: request.Device.ID,
		DeviceName: request.Device.Name, Platform: request.Device.Platform,
	})
	var admissionErr *credentialLoginAdmissionError
	switch {
	case errors.As(err, &admissionErr):
		writeAdmissionDenied(w, admissionErr.retryAfter)
	case errors.Is(err, auth.ErrInvalidCredentials):
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "The username or password is invalid")
	case errors.Is(err, auth.ErrDeviceQuotaReached):
		writeError(w, http.StatusConflict, "device_quota_reached", "The account has reached its device limit")
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_login", strings.TrimPrefix(err.Error(), auth.ErrInvalidInput.Error()+": "))
	case err != nil:
		a.internalError(w, "log in web user", err)
	default:
		setWebRefreshCookie(w, tokens, secure)
		writeJSON(w, http.StatusOK, newWebTokenResponse(tokens))
	}
}

func (a *API) webRefresh(w http.ResponseWriter, r *http.Request) {
	secure, ok := a.requireWebAuthRequest(w, r)
	if !ok {
		return
	}
	refreshToken, ok := uniqueWebRefreshCookie(r)
	if !ok {
		clearWebRefreshCookie(w, secure)
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired")
		return
	}
	tokens, err := a.auth.RefreshWeb(r.Context(), refreshToken)
	switch {
	case errors.Is(err, auth.ErrInvalidToken):
		clearWebRefreshCookie(w, secure)
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired")
	case err != nil:
		a.internalError(w, "refresh web session", err)
	default:
		setWebRefreshCookie(w, tokens, secure)
		writeJSON(w, http.StatusOK, newWebTokenResponse(tokens))
	}
}

func (a *API) webLogout(w http.ResponseWriter, r *http.Request) {
	secure, ok := a.requireWebAuthRequest(w, r)
	if !ok {
		return
	}
	clearWebRefreshCookie(w, secure)
	refreshToken, found := uniqueWebRefreshCookie(r)
	if !found {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := a.auth.LogoutWeb(r.Context(), refreshToken); err != nil && !errors.Is(err, auth.ErrInvalidToken) {
		a.internalError(w, "log out web session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) webExchangeDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	secure, ok := a.requireWebAuthRequest(w, r)
	if !ok {
		return
	}
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
	tokens, err := a.auth.ExchangeWebDeviceAuthorization(r.Context(), request.DeviceCode)
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
	case errors.Is(err, auth.ErrDeviceQuotaReached):
		writeError(w, http.StatusConflict, "device_quota_reached", "The account has reached its device limit")
	case err != nil:
		a.internalError(w, "exchange web device authorization", err)
	default:
		setWebRefreshCookie(w, tokens, secure)
		writeJSON(w, http.StatusOK, newWebTokenResponse(tokens))
	}
}

func (a *API) loginCredentialsWeb(r *http.Request, input auth.LoginInput) (auth.TokenPair, error) {
	release, retryAfter, admitted := a.credentialAdmission.acquire(requestClientIP(r, a.config.TrustedProxies))
	if !admitted {
		return auth.TokenPair{}, &credentialLoginAdmissionError{retryAfter: retryAfter}
	}
	defer release()
	return a.auth.LoginWeb(r.Context(), input)
}

func (a *API) requireWebAuthRequest(w http.ResponseWriter, r *http.Request) (bool, bool) {
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" || r.Header.Get(webCSRFHeader) != "1" {
		writeError(w, http.StatusForbidden, "invalid_csrf", "The browser authentication request is not same-origin")
		return false, false
	}
	scheme, host, ok := effectiveWebOrigin(r, a.config.TrustedProxies)
	if !ok || r.Header.Get("Origin") != scheme+"://"+host {
		writeError(w, http.StatusForbidden, "invalid_origin", "The browser authentication origin is invalid")
		return false, false
	}
	secure := scheme == "https"
	if !secure && !strictLoopbackHost(host) {
		writeError(w, http.StatusForbidden, "insecure_origin", "Browser authentication requires HTTPS outside loopback")
		return false, false
	}
	return secure, true
}

func effectiveWebOrigin(r *http.Request, trustedProxies []netip.Prefix) (string, string, bool) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	remote, remoteOK := parseIPAddress(r.RemoteAddr)
	if remoteOK && isTrustedProxy(remote, trustedProxies) {
		if forwarded := r.Header.Values("X-Forwarded-Proto"); len(forwarded) != 0 {
			value, ok := singleForwardedValue(forwarded)
			if !ok {
				return "", "", false
			}
			scheme = strings.ToLower(value)
		}
		if forwarded := r.Header.Values("X-Forwarded-Host"); len(forwarded) != 0 {
			value, ok := singleForwardedValue(forwarded)
			if !ok {
				return "", "", false
			}
			host = value
		}
	}
	if scheme != "http" && scheme != "https" {
		return "", "", false
	}
	parsed, err := url.Parse(scheme + "://" + host)
	if err != nil || parsed.Host != host || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" {
		return "", "", false
	}
	return scheme, host, true
}

func singleForwardedValue(values []string) (string, bool) {
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	return value, value != ""
}

func strictLoopbackHost(host string) bool {
	hostname := host
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		hostname = parsedHost
	}
	hostname = strings.Trim(hostname, "[]")
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(hostname)
	return err == nil && address.IsLoopback()
}

func uniqueWebRefreshCookie(r *http.Request) (string, bool) {
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == webRefreshCookieName {
			count++
			value = strings.TrimSpace(cookie.Value)
		}
	}
	return value, count == 1 && value != ""
}

func setWebRefreshCookie(w http.ResponseWriter, tokens auth.TokenPair, secure bool) {
	maxAge := int(time.Until(tokens.RefreshExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name: webRefreshCookieName, Value: tokens.RefreshToken, Path: webRefreshCookiePath,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
		Expires: tokens.RefreshExpiresAt.UTC(), MaxAge: maxAge,
	})
}

func clearWebRefreshCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: webRefreshCookieName, Value: "", Path: webRefreshCookiePath,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
		Expires: time.Unix(1, 0).UTC(), MaxAge: -1,
	})
}

func newWebTokenResponse(tokens auth.TokenPair) webTokenResponse {
	return webTokenResponse{
		TokenType: "Bearer", AccessToken: tokens.AccessToken, AccessTokenExpiresAt: tokens.AccessExpiresAt.UTC(),
		SessionID: tokens.SessionID, DeviceID: tokens.DeviceID, AuthorizationScope: tokens.AuthorizationScope,
		Category: authCategoryRefResponse(tokens.Category),
	}
}
