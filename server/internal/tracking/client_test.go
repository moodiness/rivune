package tracking

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTraktDeviceAuthorizationAndTokenExchange(t *testing.T) {
	var tokenRequest map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device/code":
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "private-code", "user_code": "ABCD1234", "verification_url": "https://trakt.tv/activate", "expires_in": 600, "interval": 5})
		case "/oauth/device/token":
			if err := json.NewDecoder(r.Body).Decode(&tokenRequest); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 604800, "created_at": time.Now().Unix()})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newProviderClient("client-id", "client-secret", "", server.Client())
	client.trakt.baseURL = server.URL
	client.trakt.authURL = server.URL
	code, err := client.beginDeviceAuthorization(context.Background(), "trakt")
	if err != nil {
		t.Fatal(err)
	}
	if code.ProviderCode != "private-code" || code.UserCode != "ABCD1234" || code.Interval != 5 {
		t.Fatalf("unexpected code: %+v", code)
	}
	token, err := client.pollDeviceAuthorization(context.Background(), "trakt", code.ProviderCode)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" || token.RefreshToken != "refresh" || token.ExpiresAt == nil {
		t.Fatalf("unexpected token: %+v", token)
	}
	if tokenRequest["code"] != "private-code" || tokenRequest["client_secret"] != "client-secret" {
		t.Fatalf("unexpected token request: %+v", tokenRequest)
	}
}

func TestSimklPINPendingDoesNotExposeToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/pin" {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK", "device_code": "DEVICE_CODE", "user_code": "ABCDE", "verification_uri": "https://simkl.com/pin", "expires_in": 900, "interval": 5})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "KO", "message": "Authorization pending"})
	}))
	defer server.Close()

	client := newProviderClient("", "", "simkl-client", server.Client())
	client.simkl.baseURL = server.URL
	code, err := client.beginDeviceAuthorization(context.Background(), "simkl")
	if err != nil {
		t.Fatal(err)
	}
	if code.ProviderCode != "ABCDE" || code.VerificationURL != "https://simkl.com/pin" {
		t.Fatalf("unexpected code: %+v", code)
	}
	if _, err := client.pollDeviceAuthorization(context.Background(), "simkl", code.ProviderCode); err != ErrAuthorizationWait {
		t.Fatalf("expected pending result, got %v", err)
	}
}

func TestProviderProgressUsesMappedIDsAndPercentage(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/scrobble/pause" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("missing bearer token")
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"action": "pause"})
	}))
	defer server.Close()

	client := newProviderClient("client-id", "client-secret", "", server.Client())
	client.trakt.baseURL = server.URL
	err := client.send(context.Background(), "trakt", "access", "progress", Event{PositionSeconds: 900, DurationSeconds: 3600}, mediaItem{MediaType: "movie", Title: "Example", Year: 2025, IDs: map[string]any{"imdb": "tt1234567", "tmdb": int64(42)}})
	if err != nil {
		t.Fatal(err)
	}
	if body["progress"] != float64(25) {
		t.Fatalf("unexpected progress: %+v", body)
	}
	movie := body["movie"].(map[string]any)
	ids := movie["ids"].(map[string]any)
	if ids["imdb"] != "tt1234567" || ids["tmdb"] != float64(42) {
		t.Fatalf("unexpected IDs: %+v", ids)
	}
}

func TestSimklLibraryAddUsesPerItemDestination(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync/add-to-list" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("client_id") != "simkl-client" {
			t.Fatalf("missing client ID query")
		}
		if r.Header.Get("simkl-api-key") != "simkl-client" || r.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("missing Simkl credentials")
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"added": map[string]any{"movies": []any{}}})
	}))
	defer server.Close()

	client := newProviderClient("", "", "simkl-client", server.Client())
	client.simkl.baseURL = server.URL
	err := client.send(context.Background(), "simkl", "access", "library", Event{InLibrary: true, OccurredAt: time.Now()}, mediaItem{
		MediaType: "movie", Title: "Example", Year: 2025, IDs: map[string]any{"imdb": "tt1234567"},
	})
	if err != nil {
		t.Fatal(err)
	}
	movie := body["movies"].([]any)[0].(map[string]any)
	if movie["to"] != "plantowatch" {
		t.Fatalf("unexpected Simkl destination: %+v", movie)
	}
	if _, exists := body["to"]; exists {
		t.Fatalf("destination must be per item: %+v", body)
	}
}

func TestClearPlaybackDeletesOnlyMappedEntry(t *testing.T) {
	deleted := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sync/playback":
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{"id": 41, "movie": map[string]any{"ids": map[string]any{"imdb": "tt7654321"}}},
				map[string]any{"id": 42, "movie": map[string]any{"ids": map[string]any{"imdb": "tt1234567"}}},
			})
		case r.Method == http.MethodDelete:
			deleted = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newProviderClient("client-id", "client-secret", "", server.Client())
	client.trakt.baseURL = server.URL
	err := client.send(context.Background(), "trakt", "access", "progress", Event{Cleared: true}, mediaItem{
		MediaType: "movie", IDs: map[string]any{"imdb": "tt1234567"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != "/sync/playback/42" {
		t.Fatalf("unexpected playback deletion %q", deleted)
	}
}
