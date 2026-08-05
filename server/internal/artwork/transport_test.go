package artwork

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestProductionRedirectValidation(t *testing.T) {
	client := newProductionHTTPClient()
	for _, target := range []string{
		"http://example.com/poster.jpg",
		"https://127.0.0.1/poster.jpg",
		"https://example.com:8443/poster.jpg",
		"https://user@example.com/poster.jpg",
		"https://[64:ff9b::7f00:1]/poster.jpg",
		"https://[64:ff9b::a00:1]/poster.jpg",
		"https://[64:ff9b::ac10:1]/poster.jpg",
		"https://[64:ff9b::c0a8:1]/poster.jpg",
		"https://[64:ff9b::6440:1]/poster.jpg",
		"https://[64:ff9b::a9fe:a9fe]/poster.jpg",
		"https://[64:ff9b:1::7f00:1]/poster.jpg",
		"https://[64:ff9b:1::a00:1]/poster.jpg",
		"https://[64:ff9b:1::a9fe:a9fe]/poster.jpg",
	} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatalf("create redirect request: %v", err)
		}
		if err := client.CheckRedirect(request, nil); err == nil {
			t.Errorf("unsafe redirect %q was accepted", target)
		}
	}
	for _, target := range []string{
		"https://example.com/poster.jpg",
		"https://[2606:4700:4700::1111]/poster.jpg",
		"https://[64:ff9b::101:101]/poster.jpg",
	} {
		safe, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatalf("create safe redirect request: %v", err)
		}
		if err := client.CheckRedirect(safe, nil); err != nil {
			t.Fatalf("safe redirect %q rejected: %v", target, err)
		}
	}
}

func TestProductionTransportScopesLANRedirectsAndDestinations(t *testing.T) {
	policy, err := newTransportPolicy([]string{"http://192.168.1.48:63113"})
	if err != nil {
		t.Fatalf("create transport policy: %v", err)
	}
	client := newProductionHTTPClient(policy)
	for _, target := range []string{
		"http://192.168.1.48:63113/poster.jpg?key=private",
		"https://example.com/poster.jpg",
	} {
		request, requestErr := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if requestErr != nil {
			t.Fatalf("create allowed redirect: %v", requestErr)
		}
		request.Header.Set("Referer", "https://source.example/private/artwork?token=secret")
		if redirectErr := client.CheckRedirect(request, nil); redirectErr != nil {
			t.Fatalf("allowed redirect was rejected: %v", redirectErr)
		}
		if request.Header.Get("Referer") != "" {
			t.Fatal("artwork redirect retained a source referrer")
		}
	}
	for _, target := range []string{
		"http://192.168.1.49:63113/poster.jpg",
		"http://192.168.1.48:63114/poster.jpg",
		"https://192.168.1.48:63113/poster.jpg",
	} {
		request, requestErr := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if requestErr != nil {
			t.Fatalf("create rejected redirect: %v", requestErr)
		}
		if redirectErr := client.CheckRedirect(request, nil); redirectErr == nil {
			t.Errorf("unconfigured redirect %q was accepted", target)
		}
	}
	if destination, allowed := policy.allowedLANDestination("192.168.1.48:63113"); !allowed || destination.String() != "192.168.1.48:63113" {
		t.Fatalf("configured LAN destination was rejected: %v allowed=%t", destination, allowed)
	}
	for _, destination := range []string{"192.168.1.48:63114", "192.168.1.49:63113", "1.1.1.1:443", "example.com:443"} {
		if _, allowed := policy.allowedLANDestination(destination); allowed {
			t.Errorf("public or unconfigured destination %q inherited LAN access", destination)
		}
	}
}

func TestStrictDialRejectsNonHTTPSPortBeforeResolution(t *testing.T) {
	if _, err := strictDialContext(context.Background(), "tcp", "example.com:80"); err == nil {
		t.Fatal("strict dialer accepted port 80")
	}
}

func TestStrictDialRejectsNAT64NonPublicDestinationsBeforeConnecting(t *testing.T) {
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
		connection, err := strictDialContext(context.Background(), "tcp", "["+value+"]:443")
		if connection != nil {
			_ = connection.Close()
			t.Fatalf("unexpected connection to NAT64 non-public destination %s", value)
		}
		if err == nil || !strings.Contains(err.Error(), "resolved to non-public address") {
			t.Fatalf("NAT64 non-public destination %s classification error = %v", value, err)
		}
	}
}

func TestNormalizeURLSanitizesMalformedDestination(t *testing.T) {
	_, err := normalizeURL(
		"https://provider.example/private/%zz?target=original&token=artwork-parse-secret",
		true,
	)
	if err == nil {
		t.Fatal("malformed artwork URL was accepted")
	}
	if strings.Contains(err.Error(), "/private/") || strings.Contains(err.Error(), "artwork-parse-secret") {
		t.Fatalf("artwork parse error exposed destination: %v", err)
	}
	var parseErr *url.Error
	if !errors.As(err, &parseErr) || parseErr.Op != "parse" || parseErr.URL != "" {
		t.Fatalf("artwork parse URL error was not sanitized: %#v", parseErr)
	}
}
