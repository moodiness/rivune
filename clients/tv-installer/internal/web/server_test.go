package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/rivune/clients/tv-installer/internal/install"
	"github.com/moodiness/rivune/clients/tv-installer/internal/release"
)

type sourceStub struct{}

func (sourceStub) Latest(context.Context) (release.Release, error) {
	return release.Release{Version: "2.0.0", TagName: "v2.0.0"}, nil
}

func (sourceStub) Download(context.Context, release.TVPackage, string) error { return nil }

func TestMutationRequiresSameOriginAndSessionToken(t *testing.T) {
	handler := New(&install.Service{Source: sourceStub{}}, "test", "token", "127.0.0.1:1234", func() {})
	tests := []struct {
		name   string
		origin string
		token  string
		status int
	}{
		{"cross origin", "https://evil.example", "token", http.StatusForbidden},
		{"missing token", "http://127.0.0.1:1234", "", http.StatusForbidden},
		{"valid boundary", "http://127.0.0.1:1234", "token", http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1234/token/api/run", strings.NewReader("{}"))
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-Rivune-Token", test.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestStatusNeverExposesSessionToken(t *testing.T) {
	handler := New(&install.Service{Source: sourceStub{}}, "1.12.0", "secret-token", "127.0.0.1:1234", func() {})
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/secret-token/api/status", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	var value map[string]any
	if json.Unmarshal(response.Body.Bytes(), &value) != nil {
		t.Fatal("invalid JSON")
	}
	if strings.Contains(response.Body.String(), "secret-token") {
		t.Fatal("status exposed token")
	}
}
