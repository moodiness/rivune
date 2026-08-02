package artwork

import (
	"context"
	"net/http"
	"net/netip"
	"testing"
)

func TestPublicAddressClassification(t *testing.T) {
	for _, candidate := range []string{
		"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "192.168.1.1", "192.0.2.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "224.0.0.1", "::", "::1", "fc00::1", "fe80::1", "2001:db8::1",
		"::ffff:127.0.0.1",
	} {
		address := netip.MustParseAddr(candidate)
		if isPublicAddress(address) {
			t.Errorf("non-public address %s was accepted", candidate)
		}
	}
	for _, candidate := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		address := netip.MustParseAddr(candidate)
		if !isPublicAddress(address) {
			t.Errorf("public address %s was rejected", candidate)
		}
	}
}

func TestProductionRedirectValidation(t *testing.T) {
	client := newProductionHTTPClient()
	for _, target := range []string{
		"http://example.com/poster.jpg",
		"https://127.0.0.1/poster.jpg",
		"https://example.com:8443/poster.jpg",
		"https://user@example.com/poster.jpg",
	} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatalf("create redirect request: %v", err)
		}
		if err := client.CheckRedirect(request, nil); err == nil {
			t.Errorf("unsafe redirect %q was accepted", target)
		}
	}
	safe, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/poster.jpg", nil)
	if err != nil {
		t.Fatalf("create safe redirect request: %v", err)
	}
	if err := client.CheckRedirect(safe, nil); err != nil {
		t.Fatalf("safe redirect rejected: %v", err)
	}
}

func TestStrictDialRejectsNonHTTPSPortBeforeResolution(t *testing.T) {
	if _, err := strictDialContext(context.Background(), "tcp", "example.com:80"); err == nil {
		t.Fatal("strict dialer accepted port 80")
	}
}
