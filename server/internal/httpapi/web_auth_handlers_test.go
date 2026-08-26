package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

func webAuthRequest(method, target string, body []byte) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Origin", "https://rivune.test")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set(webCSRFHeader, "1")
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestWebLoginSetsHostOnlyStrictCookieWithoutRefreshInBody(t *testing.T) {
	now := time.Now().UTC()
	service := &fakeAuthService{loginTokens: auth.TokenPair{
		AccessToken: "rivune_at_access", AccessExpiresAt: now.Add(time.Minute),
		RefreshToken: "rivune_rt_refresh", RefreshExpiresAt: now.Add(time.Hour),
		SessionID: "session-id", DeviceID: "device-id", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := webAuthRequest(http.MethodPost, "https://rivune.test/api/v1/auth/web/login", []byte(`{"username":"admin","password":"secret","device":{"name":"Browser","platform":"web"}}`))
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	if cookie.Name != webRefreshCookieName || cookie.Value != "rivune_rt_refresh" || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Path != webRefreshCookiePath || cookie.Domain != "" {
		t.Fatalf("unexpected refresh cookie: %+v", cookie)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, exposed := body["refreshToken"]; exposed {
		t.Fatalf("web response exposed refresh token: %s", response.Body.String())
	}
	if service.loginInput.Platform != "web" {
		t.Fatalf("web platform = %q", service.loginInput.Platform)
	}
}

func TestWebAuthRequestMatrix(t *testing.T) {
	tests := []struct {
		name, target, origin, fetchSite, csrf, remote, forwardedProto, forwardedHost string
		trusted                                                               []netip.Prefix
		want                                                                  int
		secure                                                                bool
	}{
		{name: "https same origin", target: "https://rivune.test/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "same-origin", csrf: "1", want: http.StatusNoContent, secure: true},
		{name: "localhost http", target: "http://localhost:8080/api/v1/auth/web/refresh", origin: "http://localhost:8080", fetchSite: "same-origin", csrf: "1", want: http.StatusNoContent},
		{name: "ipv6 loopback http", target: "http://[::1]:8080/api/v1/auth/web/refresh", origin: "http://[::1]:8080", fetchSite: "same-origin", csrf: "1", want: http.StatusNoContent},
		{name: "lan http refused", target: "http://192.168.1.20/api/v1/auth/web/refresh", origin: "http://192.168.1.20", fetchSite: "same-origin", csrf: "1", want: http.StatusForbidden},
		{name: "missing csrf", target: "https://rivune.test/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "same-origin", want: http.StatusForbidden},
		{name: "cross site fetch", target: "https://rivune.test/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "cross-site", csrf: "1", want: http.StatusForbidden},
		{name: "origin mismatch", target: "https://rivune.test/api/v1/auth/web/refresh", origin: "https://evil.test", fetchSite: "same-origin", csrf: "1", want: http.StatusForbidden},
		{name: "untrusted proxy spoof", target: "http://192.168.1.20/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "same-origin", csrf: "1", remote: "198.51.100.20:1234", forwardedProto: "https", forwardedHost: "rivune.test", want: http.StatusForbidden},
		{name: "trusted proxy", target: "http://10.0.0.2/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "same-origin", csrf: "1", remote: "10.0.0.2:1234", forwardedProto: "https", forwardedHost: "rivune.test", trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, want: http.StatusNoContent, secure: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := testAPI(&fakeInstanceService{})
			api.auth = &fakeAuthService{refreshTokens: auth.TokenPair{AccessToken: "rivune_at_new", RefreshToken: "rivune_rt_new", AccessExpiresAt: time.Now().Add(time.Minute), RefreshExpiresAt: time.Now().Add(time.Hour)}}
			api.config.TrustedProxies = test.trusted
			request := httptest.NewRequest(http.MethodDelete, test.target, nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			request.Header.Set(webCSRFHeader, test.csrf)
			request.AddCookie(&http.Cookie{Name: webRefreshCookieName, Value: "rivune_rt_existing"})
			if test.remote != "" {
				request.RemoteAddr = test.remote
			}
			request.Header.Set("X-Forwarded-Proto", test.forwardedProto)
			request.Header.Set("X-Forwarded-Host", test.forwardedHost)
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if test.want == http.StatusOK || test.want == http.StatusNoContent {
				setCookie := response.Header().Get("Set-Cookie")
				if strings.Contains(setCookie, "Secure") != test.secure {
					t.Fatalf("Set-Cookie secure mismatch: %q", setCookie)
				}
			}
		})
	}
}

func TestWebRefreshRejectsDuplicateCookie(t *testing.T) {
	service := &fakeAuthService{}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := webAuthRequest(http.MethodPost, "https://rivune.test/api/v1/auth/web/refresh", nil)
	request.Header.Add("Cookie", webRefreshCookieName+"=rivune_rt_one; "+webRefreshCookieName+"=rivune_rt_two")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || service.refreshToken != "" {
		t.Fatalf("duplicate cookie status=%d token=%q", response.Code, service.refreshToken)
	}
}
