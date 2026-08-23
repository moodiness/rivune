package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTVActualRequestsPreserveAuthenticationAndOptInKnownPlatforms(t *testing.T) {
	for _, platform := range []string{"webos", "tizen"} {
		t.Run(platform, func(t *testing.T) {
			authService := &fakeAuthService{}
			api := testAPI(&fakeInstanceService{})
			api.auth = authService
			request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sessions", nil)
			request.Header.Set("Origin", "null")
			request.Header.Set("Authorization", "Bearer tv-access-token")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(profileContextHeader, "rivune_pc_context")
			request.Header.Set(tvPlatformHeader, platform)
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("actual request status = %d, want 200: %s", response.Code, response.Body.String())
			}
			if authService.authenticateToken != "tv-access-token" {
				t.Fatalf("authentication token = %q, want tv-access-token", authService.authenticateToken)
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "null" {
				t.Fatalf("allow origin = %q, want null", response.Header().Get("Access-Control-Allow-Origin"))
			}
			if response.Header().Get("Access-Control-Expose-Headers") != tvCORSExposeHeaders {
				t.Fatalf("exposed headers = %q, want %q", response.Header().Get("Access-Control-Expose-Headers"), tvCORSExposeHeaders)
			}
			assertNoCredentialedOrWildcardCORS(t, response)
			assertVaryContains(t, response, "Origin", tvPlatformHeader)
		})
	}
}

func TestTVPreflightSupportsAllowedMethodsAndHeadersWithoutRouteDispatch(t *testing.T) {
	for _, method := range []string{"get", "POST", "Put", "pAtCh", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			called := false
			handler := tvCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusTeapot)
			}))
			request := newTVPreflight(method, "authorization, CONTENT-TYPE, x-rivune-profile-context, X-RIVUNE-TV-PLATFORM")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if called {
				t.Fatal("valid preflight reached the protected handler")
			}
			if response.Code != http.StatusNoContent {
				t.Fatalf("preflight status = %d, want 204", response.Code)
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "null" ||
				response.Header().Get("Access-Control-Allow-Methods") != tvCORSMethods ||
				response.Header().Get("Access-Control-Allow-Headers") != tvCORSHeaders ||
				response.Header().Get("Access-Control-Expose-Headers") != tvCORSExposeHeaders ||
				response.Header().Get("Access-Control-Max-Age") != tvCORSMaxAge {
				t.Fatalf("unexpected preflight headers: %v", response.Header())
			}
			assertNoCredentialedOrWildcardCORS(t, response)
			assertVaryContains(t, response, "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers")
		})
	}
}

func TestTVPreflightInAPIHandlerBypassesProtectedRoute(t *testing.T) {
	authService := &fakeAuthService{}
	api := testAPI(&fakeInstanceService{})
	api.auth = authService
	request := newTVPreflight(http.MethodGet, "Authorization, Content-Type, X-Rivune-Profile-Context, X-Rivune-TV-Platform")
	request.Header.Set("Authorization", "Bearer must-not-authenticate")
	request.URL.Path = "/api/v1/auth/sessions"
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("composed preflight status = %d, want 204: %s", response.Code, response.Body.String())
	}
	if authService.authenticateToken != "" {
		t.Fatalf("preflight reached authentication with token %q", authService.authenticateToken)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "null" {
		t.Fatalf("allow origin = %q, want null", response.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestTVPreflightRejectsUnknownOrIncompleteRequests(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		headers string
	}{
		{name: "unknown method", method: http.MethodConnect, headers: tvCORSHeaders},
		{name: "unknown header", method: http.MethodPost, headers: tvCORSHeaders + ", X-Arbitrary-Header"},
		{name: "missing platform marker", method: http.MethodPost, headers: "Authorization, Content-Type, X-Rivune-Profile-Context"},
		{name: "empty header member", method: http.MethodPost, headers: "Authorization,, X-Rivune-TV-Platform"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := tvCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			request := newTVPreflight(test.method, test.headers)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if called {
				t.Fatal("invalid preflight reached the protected handler")
			}
			if response.Code != http.StatusForbidden {
				t.Fatalf("invalid preflight status = %d, want 403", response.Code)
			}
			assertNoCORSPermissionHeaders(t, response)
			assertVaryContains(t, response, "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers")
		})
	}
}

func TestTVCORSDoesNotOptInUnknownPlatformsOrNormalOrigins(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		platform string
	}{
		{name: "unknown TV platform", origin: "null", platform: "browser"},
		{name: "missing TV platform", origin: "null"},
		{name: "ordinary cross origin", origin: "https://untrusted.example", platform: "webos"},
		{name: "same origin", platform: "webos"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := tvCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusAccepted)
			}))
			request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sessions", nil)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.platform != "" {
				request.Header.Set(tvPlatformHeader, test.platform)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if !called || response.Code != http.StatusAccepted {
				t.Fatalf("actual request dispatched = %t, status = %d; want true, 202", called, response.Code)
			}
			assertNoCORSPermissionHeaders(t, response)
			if test.origin == "" && len(response.Header().Values("Vary")) != 0 {
				t.Fatalf("same-origin response gained Vary headers: %v", response.Header().Values("Vary"))
			}
		})
	}
}

func newTVPreflight(method, headers string) *http.Request {
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/sessions", nil)
	request.Header.Set("Origin", "null")
	request.Header.Set("Access-Control-Request-Method", method)
	request.Header.Set("Access-Control-Request-Headers", headers)
	return request
}

func assertNoCredentialedOrWildcardCORS(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("credentialed CORS was enabled: %v", response.Header())
	}
	if response.Header().Get("Access-Control-Allow-Origin") == "*" {
		t.Fatalf("wildcard CORS was enabled: %v", response.Header())
	}
}

func assertNoCORSPermissionHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	for _, name := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Credentials",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Expose-Headers",
		"Access-Control-Max-Age",
	} {
		if value := response.Header().Get(name); value != "" {
			t.Fatalf("rejected response header %s = %q, want empty", name, value)
		}
	}
}

func assertVaryContains(t *testing.T, response *httptest.ResponseRecorder, expected ...string) {
	t.Helper()
	var values []string
	for _, line := range response.Header().Values("Vary") {
		for _, value := range strings.Split(line, ",") {
			values = append(values, strings.TrimSpace(value))
		}
	}
	for _, expectedValue := range expected {
		found := false
		for _, value := range values {
			if strings.EqualFold(value, expectedValue) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Vary = %v, missing %q", values, expectedValue)
		}
	}
}
