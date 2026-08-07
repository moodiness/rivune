package jellyfin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const routeTestUUID = "11111111-1111-4111-8111-111111111111"

func TestReservedPathsFailClosedInsideCompatNamespaces(t *testing.T) {
	reserved := []string{
		"/System/Info/Public",
		"/emby/System/Info/Public",
		"/Users/" + routeTestUUID + "/Items/" + routeTestUUID,
		"/Items/not-a-uuid",
		"/Items/" + routeTestUUID + "/Images/Logo/7",
		"/UserItems/Resume",
		"/SyncPlay/List",
		"/Playback/BitrateTest",
		"/Videos/" + routeTestUUID + "/stream.bad!",
		"/System/Info/Public/extra",
		"/system/ping",
		"/Emby/System/Ping",
		"/emby/emby/System/Ping",
	}
	for _, path := range reserved {
		if !IsReservedPath(path) {
			t.Errorf("IsReservedPath(%q) = false", path)
		}
	}
	for _, path := range []string{
		"/api/v1/System/Info/Public",
		"/embySystem/Info/Public",
		"/SystemStatus",
		"/watch",
	} {
		if IsReservedPath(path) {
			t.Errorf("IsReservedPath(%q) = true", path)
		}
	}
	if got, want := NormalizeEmbyPath("/emby/emby/System/Ping"), "/emby/System/Ping"; got != want {
		t.Fatalf("NormalizeEmbyPath stripped more than one prefix: got %q, want %q", got, want)
	}
}

func TestDispatcherServesAllFiftyTwoRoutesAtRootAndEmby(t *testing.T) {
	if len(routeDefinitions) != 52 {
		t.Fatalf("route definitions = %d, want 52", len(routeDefinitions))
	}
	calls := make(map[Route]int, len(routeDefinitions))
	handlers := make(map[Route]http.Handler, len(routeDefinitions))
	for _, definition := range routeDefinitions {
		definition := definition
		handlers[definition.Route] = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			calls[definition.Route]++
			if strings.HasPrefix(request.URL.Path, "/emby/") {
				t.Errorf("route %s received unnormalized path %q", definition.Route, request.URL.Path)
			}
			response.WriteHeader(http.StatusNoContent)
		})
	}
	handler, err := New(Dependencies{Handlers: handlers})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.Handler(handler)

	for _, definition := range routeDefinitions {
		path := routeTestPath(definition)
		for _, prefix := range []string{"", "/emby"} {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(definition.Method, prefix+path, nil))
			if response.Code != http.StatusNoContent {
				t.Errorf("%s %s%s status = %d, want 204", definition.Method, prefix, path, response.Code)
			}
		}
		if calls[definition.Route] != 2 {
			t.Errorf("route %s calls = %d, want 2", definition.Route, calls[definition.Route])
		}
	}
}

func TestDispatcherSupportsBoundedJellyfinWebCORS(t *testing.T) {
	calls := 0
	handler, err := New(Dependencies{Handlers: map[Route]http.Handler{
		RoutePublicSystemInfo: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			calls++
			response.WriteHeader(http.StatusNoContent)
		}),
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, prefix := range []string{"", "/emby"} {
		beforeCalls := calls
		preflight := httptest.NewRequest(http.MethodOptions, prefix+"/System/Info/Public", nil)
		preflight.Header.Set("Origin", "http://localhost:8081")
		preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
		preflight.Header.Set("Access-Control-Request-Headers", "cache-control, content-type, x-emby-authorization")
		preflight.Header.Set("Access-Control-Request-Private-Network", "true")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, preflight)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "*" ||
			!strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "Cache-Control") ||
			!strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "X-Emby-Authorization") ||
			response.Header().Get("Access-Control-Allow-Private-Network") != "true" || calls != beforeCalls {
			t.Fatalf("preflight %s status=%d headers=%v calls=%d", prefix, response.Code, response.Header(), calls)
		}

		actual := httptest.NewRequest(http.MethodGet, prefix+"/System/Info/Public", nil)
		actual.Header.Set("Origin", "http://localhost:8081")
		actualResponse := httptest.NewRecorder()
		handler.ServeHTTP(actualResponse, actual)
		if actualResponse.Code != http.StatusNoContent || actualResponse.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("actual CORS %s status=%d headers=%v", prefix, actualResponse.Code, actualResponse.Header())
		}
	}

	invalid := httptest.NewRequest(http.MethodOptions, "/System/Info/Public", nil)
	invalid.Header.Set("Origin", "http://localhost:8081")
	invalid.Header.Set("Access-Control-Request-Method", http.MethodGet)
	invalid.Header.Set("Access-Control-Request-Headers", "x-private-secret")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusNotFound || invalidResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("invalid preflight status=%d headers=%v", invalidResponse.Code, invalidResponse.Header())
	}
}

func TestDispatcherReturns404ForUnsupportedMethods(t *testing.T) {
	calls := 0
	handlers := make(map[Route]http.Handler, len(routeDefinitions))
	for _, definition := range routeDefinitions {
		handlers[definition.Route] = http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			calls++
			response.WriteHeader(http.StatusNoContent)
		})
	}
	handler, err := New(Dependencies{Handlers: handlers})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.Handler(handler)

	for _, definition := range routeDefinitions {
		for _, prefix := range []string{"", "/emby"} {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, prefix+routeTestPath(definition), nil))
			if response.Code != http.StatusNotFound {
				t.Errorf("PATCH %s%s status = %d, want 404", prefix, routeTestPath(definition), response.Code)
			}
			if response.Header().Get("Allow") != "" {
				t.Errorf("PATCH %s%s exposed Allow = %q", prefix, routeTestPath(definition), response.Header().Get("Allow"))
			}
		}
	}
	if calls != 0 {
		t.Fatalf("unsupported methods reached handlers %d times", calls)
	}
}

func TestGETDoesNotImplicitlyHandleHEAD(t *testing.T) {
	calls := 0
	handler, err := New(Dependencies{Handlers: map[Route]http.Handler{
		RouteSystemPing: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			calls++
			response.WriteHeader(http.StatusNoContent)
		}),
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.Handler(handler)
	for _, path := range []string{"/System/Ping", "/emby/System/Ping"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodHead, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("HEAD %s status = %d, want 404", path, response.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("HEAD reached GET handler %d times", calls)
	}
}

func TestMalformedWildcardPathsAreRejectedBeforeHandler(t *testing.T) {
	calls := 0
	implementation := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls++
		response.WriteHeader(http.StatusNoContent)
	})
	handlers := make(map[Route]http.Handler, len(routeDefinitions))
	for _, definition := range routeDefinitions {
		handlers[definition.Route] = implementation
	}
	handler, err := New(Dependencies{Handlers: handlers})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.Handler(handler)

	malformed := []string{
		"/Items/not-a-uuid",
		"/Users/not-a-uuid/Items/" + routeTestUUID,
		"/Users/" + routeTestUUID + "/Items/not-a-uuid",
		"/Shows/not-a-uuid/Seasons",
		"/Items/" + routeTestUUID + "/Images/Logo",
		"/Items/" + routeTestUUID + "/Images/Primary/1",
		"/Videos/" + routeTestUUID + "/stream.mp4!",
		"/DisplayPreferences/bad$value",
		"/Items/" + strings.Repeat("a", maximumCompatPathBytes+1),
	}
	for _, path := range malformed {
		for _, prefix := range []string{"", "/emby"} {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, prefix+path, nil))
			if response.Code != http.StatusNotFound {
				t.Errorf("GET %s%s status = %d, want 404", prefix, path, response.Code)
			}
		}
	}
	if calls != 0 {
		t.Fatalf("malformed paths reached route handlers %d times", calls)
	}
}

func TestMalformedSelectorsNeverAuthenticate(t *testing.T) {
	serverID, err := ParseServerID(routeTestUUID)
	if err != nil {
		t.Fatal(err)
	}
	authentication := &routeCountingAuthentication{}
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Rivune"},
		Authentication: authentication,
		Catalog:        &stateCatalog{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.Handler(handler)
	for _, path := range []string{
		"/Items/not-a-uuid",
		"/emby/Items/" + strings.Repeat("a", maximumCompatPathBytes+1),
		"/Users//Me",
		"/Users/./Me",
		"/Users%2FMe",
		`/Users\Me`,
		"/emby/Users//Me",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Emby-Token", compatTestToken(1))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, response.Code)
		}
	}
	if authentication.authenticateCalls != 0 {
		t.Fatalf("malformed selectors caused %d authentication calls", authentication.authenticateCalls)
	}
}

func TestStreamAndContainerDispatchPrecedence(t *testing.T) {
	seen := make([]Route, 0, 8)
	handlers := make(map[Route]http.Handler, 4)
	for _, route := range []Route{RouteStream, RouteStreamHead, RouteContainerStream, RouteContainerStreamHead} {
		route := route
		handlers[route] = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			seen = append(seen, route)
			if route == RouteContainerStream || route == RouteContainerStreamHead {
				if request.PathValue("container") != "mp4" {
					t.Errorf("container = %q, want mp4", request.PathValue("container"))
				}
			}
			response.WriteHeader(http.StatusNoContent)
		})
	}
	handler, err := New(Dependencies{Handlers: handlers})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.Handler(handler)
	for _, prefix := range []string{"", "/emby"} {
		for _, test := range []struct {
			method string
			path   string
			route  Route
		}{
			{http.MethodGet, "/Videos/" + routeTestUUID + "/stream", RouteStream},
			{http.MethodHead, "/Videos/" + routeTestUUID + "/stream", RouteStreamHead},
			{http.MethodGet, "/Videos/" + routeTestUUID + "/stream.mp4", RouteContainerStream},
			{http.MethodHead, "/Videos/" + routeTestUUID + "/stream.mp4", RouteContainerStreamHead},
		} {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(test.method, prefix+test.path, nil))
			if response.Code != http.StatusNoContent || len(seen) == 0 || seen[len(seen)-1] != test.route {
				t.Fatalf("%s %s%s status/route = %d/%v, want 204/%s", test.method, prefix, test.path, response.Code, seen, test.route)
			}
		}
	}
}

func TestStaticRouteSegmentsAreCaseInsensitive(t *testing.T) {
	calls := 0
	handler, err := New(Dependencies{Handlers: map[Route]http.Handler{
		RouteSystemPing: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			calls++
			response.WriteHeader(http.StatusNoContent)
		}),
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, path := range []string{"/system/ping", "/SyStEm/PiNg", "/emby/system/ping"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("case-insensitive route %s status=%d", path, response.Code)
		}
	}
	if calls != 3 {
		t.Fatalf("case-insensitive route calls=%d", calls)
	}
}

func TestNonCanonicalPathsReturn404(t *testing.T) {
	calls := 0
	handler, err := New(Dependencies{Handlers: map[Route]http.Handler{
		RouteSystemPing: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			calls++
			response.WriteHeader(http.StatusNoContent)
		}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/System/Ping/", nil),
		httptest.NewRequest(http.MethodGet, "/Emby/System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/emby/emby/System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System//Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System/./Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System/../System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System%2FPing", nil),
		httptest.NewRequest(http.MethodGet, "/System%5CPing", nil),
		httptest.NewRequest(http.MethodGet, `/System\Ping`, nil),
		httptest.NewRequest(http.MethodGet, "/emby//System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/emby/./System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/emby%2FSystem/Ping", nil),
	}
	incoherentRawPath := httptest.NewRequest(http.MethodGet, "/System/Ping", nil)
	incoherentRawPath.URL.RawPath = "/System/Info"
	requests = append(requests, incoherentRawPath)
	for _, request := range requests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", request.RequestURI, response.Code)
		}
		if response.Code == http.StatusMovedPermanently || response.Code == http.StatusTemporaryRedirect || response.Code == http.StatusPermanentRedirect {
			t.Errorf("GET %s redirected with status %d", request.RequestURI, response.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("non-canonical paths reached handler %d times", calls)
	}
}

func TestHandlerRejectsUnknownOrNilImplementations(t *testing.T) {
	for _, dependencies := range []Dependencies{
		{Handlers: map[Route]http.Handler{Route("unknown"): http.NotFoundHandler()}},
		{Handlers: map[Route]http.Handler{RouteSystemPing: nil}},
	} {
		if _, err := New(dependencies); !errors.Is(err, ErrInvalidDependencies) {
			t.Fatalf("New(%#v) error = %v, want ErrInvalidDependencies", dependencies, err)
		}
	}
}

func TestEveryRouteStaysOutsideNativeAPI(t *testing.T) {
	for _, route := range Routes() {
		if len(route.Pattern) < 1 || route.Pattern[0] != '/' {
			t.Fatalf("route %q has invalid pattern %q", route.Route, route.Pattern)
		}
		if strings.HasPrefix(route.Pattern, "/api/v1") {
			t.Fatalf("route %q is mounted in the native API: %s", route.Route, route.Pattern)
		}
	}
}

func TestCompleteRealDependenciesInstallEveryBuiltInRoute(t *testing.T) {
	serverID, err := ParseServerID(routeTestUUID)
	if err != nil {
		t.Fatalf("parse server ID: %v", err)
	}
	handler, err := New(Dependencies{
		ServerInfo:     ServerInfo{ID: serverID, Name: "Rivune", RuntimeVersion: "test"},
		Authentication: &stateAuthentication{},
		Catalog:        &stateCatalog{},
		Artwork:        &artworkDelivery{},
		Playback:       &statePlaybackDelivery{},
		Watchstate:     newMemoryWatchstate(),
	})
	if err != nil {
		t.Fatalf("New complete handler: %v", err)
	}
	if len(handler.handlers) != len(routeDefinitions) {
		missing := make([]Route, 0)
		for _, definition := range routeDefinitions {
			if handler.handlers[definition.Route] == nil {
				missing = append(missing, definition.Route)
			}
		}
		t.Fatalf("installed %d of %d built-ins; missing %v", len(handler.handlers), len(routeDefinitions), missing)
	}
}

func routeTestPath(definition RouteSpec) string {
	path := definition.Pattern
	path = strings.ReplaceAll(path, "{userId}", routeTestUUID)
	path = strings.ReplaceAll(path, "{itemId}", routeTestUUID)
	path = strings.ReplaceAll(path, "{seriesId}", routeTestUUID)
	path = strings.ReplaceAll(path, "{id}", routeTestUUID)
	path = strings.ReplaceAll(path, "{type}", "Primary")
	path = strings.ReplaceAll(path, "{index}", "0")
	path = strings.ReplaceAll(path, "{container}", "mp4")
	return path
}

type routeCountingAuthentication struct {
	authenticateCalls int
}

func (*routeCountingAuthentication) Login(context.Context, CompatLoginInput) (LoginResult, error) {
	return LoginResult{}, ErrInvalidCompatCredential
}

func (authentication *routeCountingAuthentication) Authenticate(context.Context, string) (AuthenticatedSession, error) {
	authentication.authenticateCalls++
	return AuthenticatedSession{}, ErrInvalidCompatCredential
}

func (*routeCountingAuthentication) Logout(context.Context, AuthenticatedSession) error {
	return nil
}
