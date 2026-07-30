package httpapi

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestRequestClientIPIgnoresForwardingHeadersFromUntrustedPeer(t *testing.T) {
	request := httptest.NewRequest("GET", "http://rivune.test/api/v1/auth/me", nil)
	request.RemoteAddr = "198.51.100.20:4123"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")

	if got := requestClientIP(request, nil); got != "198.51.100.20" {
		t.Fatalf("expected direct peer address, got %q", got)
	}
}

func TestRequestClientIPSelectsFirstUntrustedAddressFromRight(t *testing.T) {
	request := httptest.NewRequest("GET", "http://rivune.test/api/v1/auth/me", nil)
	request.RemoteAddr = "10.0.0.2:4123"
	request.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.8")
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

	if got := requestClientIP(request, trusted); got != "203.0.113.9" {
		t.Fatalf("expected forwarded client address, got %q", got)
	}
}

func TestRequestClientIPUsesRealIPFromTrustedPeerWithoutForwardedChain(t *testing.T) {
	request := httptest.NewRequest("GET", "http://rivune.test/api/v1/auth/me", nil)
	request.RemoteAddr = "10.0.0.2:4123"
	request.Header.Set("X-Real-IP", "2001:db8::42")
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

	if got := requestClientIP(request, trusted); got != "2001:db8::42" {
		t.Fatalf("expected real client address, got %q", got)
	}
}
