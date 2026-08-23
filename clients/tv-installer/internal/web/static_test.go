package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/rivune/clients/tv-installer/internal/install"
)

func TestEmbeddedInterfaceIsServedOnlyInsideTokenScope(t *testing.T) {
	handler := New(&install.Service{Source: sourceStub{}}, "test", "session-token", "127.0.0.1:1234", func() {})
	valid := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/session-token/app.js", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, valid)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "javascript") || response.Body.Len() == 0 {
		t.Fatalf("embedded JavaScript was not served: %d %s", response.Code, response.Body.String())
	}
	missingToken := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/app.js", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, missingToken)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unscoped JavaScript returned %d", response.Code)
	}
}
