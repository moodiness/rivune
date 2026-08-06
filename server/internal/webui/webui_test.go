package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesEntryPointAndBrowserRoutes(t *testing.T) {
	t.Parallel()

	for _, requestPath := range []string{"/", "/library", "/settings/profiles", "/media/series/tt9003001/season/3/episode/2"} {
		requestPath := requestPath
		t.Run(requestPath, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, requestPath, nil)
			response := httptest.NewRecorder()

			Handler(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", response.Code)
			}
			if !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
				t.Fatal("response did not contain the web client entry point")
			}
			if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-cache" {
				t.Fatalf("expected no-cache entry point, got %q", cacheControl)
			}
			if policy := response.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "script-src 'self'") {
				t.Fatalf("entry point did not allow its bundled scripts: %q", policy)
			}
		})
	}
}

func TestHandlerRejectsMissingAssetsAndAPIRoutes(t *testing.T) {
	t.Parallel()

	for _, requestPath := range []string{"/assets/missing.js", "/api/v1/missing", "/.well-known/missing"} {
		requestPath := requestPath
		t.Run(requestPath, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, requestPath, nil)
			response := httptest.NewRecorder()

			Handler(response, request)

			if response.Code != http.StatusNotFound {
				t.Fatalf("expected status 404, got %d", response.Code)
			}
		})
	}
}
