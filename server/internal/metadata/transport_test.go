package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderHTTPClientRejectsMetadataAndLoopbackRedirects(t *testing.T) {
	client := NewProviderHTTPClient("https://provider.example/v1", time.Second)
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data",
		"https://169.254.169.254/latest/meta-data",
		"https://127.0.0.1/private",
		"https://[::1]/private",
	} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatalf("create redirect request for %q: %v", target, err)
		}
		if err := client.CheckRedirect(request, nil); err == nil {
			t.Errorf("unsafe redirect %q was accepted", target)
		}
	}
}

func TestProviderHTTPClientRejectsNAT64NonPublicSameOriginRedirects(t *testing.T) {
	for _, value := range []string{
		"64:ff9b::7f00:1",
		"64:ff9b::a00:1",
		"64:ff9b::ac10:1",
		"64:ff9b::c0a8:1",
		"64:ff9b::6440:1",
		"64:ff9b::a9fe:a9fe",
		"64:ff9b:1::7f00:1",
		"64:ff9b:1::a00:1",
		"64:ff9b:1::a9fe:a9fe",
	} {
		client := NewProviderHTTPClient("https://["+value+"]/v1", time.Second)
		request, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodGet,
			"https://["+value+"]/v2",
			nil,
		)
		if err != nil {
			t.Fatalf("create NAT64 redirect request for %s: %v", value, err)
		}
		if err := client.CheckRedirect(request, nil); err == nil {
			t.Errorf("NAT64 non-public redirect %s was accepted", value)
		}
	}
}

func TestProviderHTTPClientAllowsPublicNAT64AndIPv6SameOriginRedirects(t *testing.T) {
	for _, value := range []string{
		"64:ff9b::101:101",
		"2606:4700:4700::1111",
	} {
		client := NewProviderHTTPClient("https://["+value+"]/v1", time.Second)
		request, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodGet,
			"https://["+value+"]/v2",
			nil,
		)
		if err != nil {
			t.Fatalf("create public IPv6 redirect request for %s: %v", value, err)
		}
		if err := client.CheckRedirect(request, nil); err != nil {
			t.Fatalf("public IPv6 redirect %s rejected: %v", value, err)
		}
	}
}

func TestDialPublicProviderRejectsNAT64NonPublicDestinationsBeforeConnecting(t *testing.T) {
	for _, value := range []string{
		"64:ff9b::7f00:1",
		"64:ff9b::a00:1",
		"64:ff9b::ac10:1",
		"64:ff9b::c0a8:1",
		"64:ff9b::6440:1",
		"64:ff9b::a9fe:a9fe",
		"64:ff9b:1::7f00:1",
		"64:ff9b:1::a00:1",
		"64:ff9b:1::a9fe:a9fe",
	} {
		connection, err := dialPublicProvider(context.Background(), "tcp", "["+value+"]:443")
		if connection != nil {
			_ = connection.Close()
			t.Fatalf("unexpected connection to NAT64 non-public destination %s", value)
		}
		if err == nil || err.Error() != "metadata provider destination resolved to a non-public address" {
			t.Fatalf("NAT64 non-public destination %s classification error = %v", value, err)
		}
	}
}

func TestProviderHTTPClientDoesNotSendSecretsAcrossOrigins(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		destinationRequests.Add(1)
		if request.Header.Get("Authorization") != "" || request.Header.Get("api-key") != "" {
			t.Error("cross-origin redirect received metadata provider credentials")
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()

	origin := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	client := NewProviderHTTPClient(origin.URL, 5*time.Second)
	client.Transport = origin.Client().Transport
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, origin.URL+"/start", nil)
	if err != nil {
		t.Fatalf("create origin request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer test-access-token")
	request.Header.Set("api-key", "test-api-key")
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "cross-origin redirect refused") {
		t.Fatalf("cross-origin redirect error = %v", err)
	}
	if destinationRequests.Load() != 0 {
		t.Fatalf("cross-origin destination received %d requests, want 0", destinationRequests.Load())
	}
}

func TestProviderHTTPClientAllowsSameOriginHTTPSRedirect(t *testing.T) {
	client := NewProviderHTTPClient("https://provider.example/v1", time.Second)
	previous, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://provider.example/v1/start", nil)
	if err != nil {
		t.Fatalf("create previous request: %v", err)
	}
	redirect, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://provider.example/v2/result", nil)
	if err != nil {
		t.Fatalf("create redirect request: %v", err)
	}
	if err := client.CheckRedirect(redirect, []*http.Request{previous}); err != nil {
		t.Fatalf("same-origin HTTPS redirect rejected: %v", err)
	}
}
