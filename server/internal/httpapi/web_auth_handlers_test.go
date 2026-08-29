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
	request.Header.Set("Origin", "https://media.example")
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
	request := webAuthRequest(http.MethodPost, "https://media.example/api/v1/auth/web/login", []byte(`{"username":"admin","password":"secret","device":{"id":"current-device","ids":["current-device","remembered-device"],"name":"Browser","platform":"web"}}`))
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
	if service.loginInput.DeviceID != "current-device" || len(service.loginInput.DeviceIDs) != 2 ||
		service.loginInput.DeviceIDs[0] != "current-device" || service.loginInput.DeviceIDs[1] != "remembered-device" {
		t.Fatalf("web remembered device ids = primary %q candidates %v", service.loginInput.DeviceID, service.loginInput.DeviceIDs)
	}
}

func TestWebAuthRequestMatrix(t *testing.T) {
	tests := []struct {
		name, target, origin, fetchSite, csrf, remote, forwardedProto, forwardedHost, forwardedMarker, publicURL string
		trusted                                                                                                  []netip.Prefix
		want                                                                                                     int
		secure                                                                                                   bool
	}{
		{name: "https same origin", target: "https://rivune.test/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "same-origin", csrf: "1", want: http.StatusNoContent, secure: true},
		{name: "configured https origin without fetch metadata", target: "http://rivune:8080/api/v1/auth/web/refresh", origin: "https://rivune.domain.com", csrf: "1", remote: "172.18.0.3:1234", publicURL: "https://rivune.domain.com", want: http.StatusNoContent, secure: true},
		{name: "configured public origin permits direct private lan literal", target: "http://192.168.1.20:18080/api/v1/auth/web/refresh", origin: "http://192.168.1.20:18080", csrf: "1", publicURL: "https://rivune.domain.com", want: http.StatusNoContent},
		{name: "configured public origin permits direct private through trusted gateway without forwarding", target: "http://192.168.1.20:18080/api/v1/auth/web/refresh", origin: "http://192.168.1.20:18080", csrf: "1", remote: "10.0.0.2:1234", publicURL: "https://rivune.domain.com", trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, want: http.StatusNoContent},
		{name: "configured public origin rejects direct lan dns alias", target: "http://rivune.lan:18080/api/v1/auth/web/refresh", origin: "http://rivune.lan:18080", csrf: "1", publicURL: "https://rivune.domain.com", want: http.StatusForbidden},
		{name: "configured public origin rejects direct public ip", target: "https://8.8.8.8/api/v1/auth/web/refresh", origin: "https://8.8.8.8", csrf: "1", publicURL: "https://rivune.domain.com", want: http.StatusForbidden},
		{name: "configured public origin rejects direct shared address space", target: "http://100.64.0.2:18080/api/v1/auth/web/refresh", origin: "http://100.64.0.2:18080", csrf: "1", publicURL: "https://rivune.domain.com", want: http.StatusForbidden},
		{name: "configured public origin rejects mapped private address", target: "http://[::ffff:192.168.1.20]:18080/api/v1/auth/web/refresh", origin: "http://[::ffff:192.168.1.20]:18080", csrf: "1", publicURL: "https://rivune.domain.com", want: http.StatusForbidden},
		{name: "configured public origin rejects forwarded private fallback", target: "http://192.168.1.20:18080/api/v1/auth/web/refresh", origin: "http://192.168.1.20:18080", csrf: "1", remote: "10.0.0.2:1234", forwardedProto: "http", forwardedHost: "192.168.1.20:18080", publicURL: "https://rivune.domain.com", trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, want: http.StatusForbidden},
		{name: "configured public origin rejects generic forwarding marker", target: "http://192.168.1.20:18080/api/v1/auth/web/refresh", origin: "http://192.168.1.20:18080", csrf: "1", remote: "10.0.0.2:1234", forwardedMarker: "18080", publicURL: "https://rivune.domain.com", trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, want: http.StatusForbidden},
		{name: "configured public origin rejects mismatched lan origin", target: "http://192.168.1.20:18080/api/v1/auth/web/refresh", origin: "https://evil.test", csrf: "1", publicURL: "https://rivune.domain.com", want: http.StatusForbidden},
		{name: "configured public origin rejects mismatched lan port", target: "http://192.168.1.20:18080/api/v1/auth/web/refresh", origin: "http://192.168.1.20:18081", csrf: "1", publicURL: "https://rivune.domain.com", want: http.StatusForbidden},
		{name: "configured lan http origin without fetch metadata", target: "http://192.168.1.20:8080/api/v1/auth/web/refresh", origin: "http://192.168.1.20:8080", csrf: "1", publicURL: "http://192.168.1.20:8080", want: http.StatusNoContent},
		{name: "localhost http", target: "http://localhost:8080/api/v1/auth/web/refresh", origin: "http://localhost:8080", fetchSite: "same-origin", csrf: "1", want: http.StatusNoContent},
		{name: "ipv6 loopback http", target: "http://[::1]:8080/api/v1/auth/web/refresh", origin: "http://[::1]:8080", fetchSite: "same-origin", csrf: "1", want: http.StatusNoContent},
		{name: "missing origin", target: "https://rivune.test/api/v1/auth/web/refresh", fetchSite: "same-origin", csrf: "1", want: http.StatusForbidden},
		{name: "lan http refused", target: "http://192.168.1.20/api/v1/auth/web/refresh", origin: "http://192.168.1.20", fetchSite: "same-origin", csrf: "1", want: http.StatusForbidden},
		{name: "missing csrf", target: "https://rivune.test/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "same-origin", want: http.StatusForbidden},
		{name: "cross site fetch", target: "https://rivune.test/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "cross-site", csrf: "1", want: http.StatusForbidden},
		{name: "same site fetch", target: "https://rivune.test/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "same-site", csrf: "1", want: http.StatusForbidden},
		{name: "origin mismatch", target: "https://rivune.test/api/v1/auth/web/refresh", origin: "https://evil.test", fetchSite: "same-origin", csrf: "1", want: http.StatusForbidden},
		{name: "untrusted proxy spoof", target: "http://192.168.1.20/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "same-origin", csrf: "1", remote: "198.51.100.20:1234", forwardedProto: "https", forwardedHost: "rivune.test", want: http.StatusForbidden},
		{name: "trusted proxy", target: "http://10.0.0.2/api/v1/auth/web/refresh", origin: "https://rivune.test", fetchSite: "same-origin", csrf: "1", remote: "10.0.0.2:1234", forwardedProto: "https", forwardedHost: "rivune.test", trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, want: http.StatusNoContent, secure: true},
		{name: "configured public origin behind rewritten proxy", target: "http://rivune:8080/api/v1/auth/web/refresh", origin: "https://rivune.domain.com", fetchSite: "same-origin", csrf: "1", remote: "192.0.2.10:1234", publicURL: "https://rivune.domain.com", want: http.StatusNoContent, secure: true},
		{name: "configured public origin rejects forwarded spoof", target: "http://10.0.0.2/api/v1/auth/web/refresh", origin: "https://evil.test", fetchSite: "same-origin", csrf: "1", remote: "10.0.0.2:1234", forwardedProto: "https", forwardedHost: "evil.test", publicURL: "https://rivune.domain.com", trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := testAPI(&fakeInstanceService{})
			api.auth = &fakeAuthService{refreshTokens: auth.TokenPair{AccessToken: "rivune_at_new", RefreshToken: "rivune_rt_new", AccessExpiresAt: time.Now().Add(time.Minute), RefreshExpiresAt: time.Now().Add(time.Hour)}}
			api.config.TrustedProxies = test.trusted
			api.config.PublicURL = test.publicURL
			request := httptest.NewRequest(http.MethodDelete, test.target, nil)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			if test.csrf != "" {
				request.Header.Set(webCSRFHeader, test.csrf)
			}
			request.AddCookie(&http.Cookie{Name: webRefreshCookieName, Value: "rivune_rt_existing"})
			if test.remote != "" {
				request.RemoteAddr = test.remote
			}
			if test.forwardedProto != "" {
				request.Header.Set("X-Forwarded-Proto", test.forwardedProto)
			}
			if test.forwardedHost != "" {
				request.Header.Set("X-Forwarded-Host", test.forwardedHost)
			}
			if test.forwardedMarker != "" {
				request.Header.Set("X-Forwarded-Port", test.forwardedMarker)
			}
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

func TestWebAuthRequestRejectsAmbiguousSecurityHeaders(t *testing.T) {
	tests := []struct {
		name, header string
		values       []string
	}{
		{name: "duplicate origin", header: "Origin", values: []string{"https://media.example", "https://evil.test"}},
		{name: "duplicate fetch site", header: "Sec-Fetch-Site", values: []string{"same-origin", "cross-site"}},
		{name: "empty fetch site", header: "Sec-Fetch-Site", values: []string{""}},
		{name: "combined fetch site", header: "Sec-Fetch-Site", values: []string{"same-origin, cross-site"}},
		{name: "duplicate csrf", header: webCSRFHeader, values: []string{"1", "0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := testAPI(&fakeInstanceService{})
			api.auth = &fakeAuthService{}
			request := webAuthRequest(http.MethodDelete, "https://media.example/api/v1/auth/web/refresh", nil)
			request.Header[http.CanonicalHeaderKey(test.header)] = test.values
			request.AddCookie(&http.Cookie{Name: webRefreshCookieName, Value: "rivune_rt_existing"})
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("ambiguous %s status = %d, want %d", test.header, response.Code, http.StatusForbidden)
			}
		})
	}
}

func TestWebRefreshRejectsDuplicateCookie(t *testing.T) {
	service := &fakeAuthService{}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := webAuthRequest(http.MethodPost, "https://media.example/api/v1/auth/web/refresh", nil)
	request.Header.Add("Cookie", webRefreshCookieName+"=rivune_rt_one; "+webRefreshCookieName+"=rivune_rt_two")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || service.refreshToken != "" {
		t.Fatalf("duplicate cookie status=%d token=%q", response.Code, service.refreshToken)
	}
}
