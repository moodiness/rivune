package addon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/netguard"
)

func TestDefaultHTTPTransportRejectsCloudMetadataAddress(t *testing.T) {
	_, _, err := NewHTTPTransport(nil).Manifest(context.Background(), "http://169.254.169.254/manifest.json")
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("manifest error = %v, want %v", err, ErrProviderUnavailable)
	}
	if !errors.Is(err, netguard.ErrOutboundDestinationNotPermitted) {
		t.Fatalf("manifest error does not preserve destination rejection: %v", err)
	}
}

func TestDefaultHTTPTransportUsesRestrictedClientOnlyForAllowedPrivateLiterals(t *testing.T) {
	transport := NewHTTPTransport(nil)
	if transport.publicClient == transport.privateLiteralClient {
		t.Fatal("public and private-literal addon transports share an egress policy")
	}
	for _, transportURL := range []string{
		"http://10.0.0.1/manifest.json",
		"http://100.64.0.1/manifest.json",
		"http://172.16.0.1/manifest.json",
		"http://192.168.0.1/manifest.json",
		"http://[fd12:3456:789a::1]/manifest.json",
	} {
		if !isPrivateNetworkTransportURL(transportURL) {
			t.Fatalf("allowed private literal was not recognized: %s", transportURL)
		}
	}
	for _, transportURL := range []string{
		"http://localhost/manifest.json",
		"http://127.0.0.1/manifest.json",
		"http://169.254.169.254/manifest.json",
		"http://[64:ff9b::a00:1]/manifest.json",
		"http://addon.internal/manifest.json",
	} {
		if isPrivateNetworkTransportURL(transportURL) {
			t.Fatalf("unsafe or DNS destination received private-literal privileges: %s", transportURL)
		}
	}
	redirect, _ := http.NewRequest(http.MethodGet, "http://192.168.0.2/manifest.json", nil)
	original, _ := http.NewRequest(http.MethodGet, "http://192.168.0.1/manifest.json", nil)
	if err := transport.privateLiteralClient.CheckRedirect(redirect, []*http.Request{original}); err == nil {
		t.Fatal("private-network addon redirect was allowed")
	}
}

func TestDefaultHTTPTransportRejectsNAT64NonPublicAddresses(t *testing.T) {
	for _, value := range []string{
		"64:ff9b::7f00:1",
		"64:ff9b::a00:1",
		"64:ff9b::ac10:1",
		"64:ff9b::c0a8:1",
		"64:ff9b::6440:1",
		"64:ff9b::a9fe:a9fe",
	} {
		t.Run(value, func(t *testing.T) {
			_, _, err := NewHTTPTransport(nil).Manifest(
				context.Background(),
				"http://["+value+"]/manifest.json",
			)
			if !errors.Is(err, ErrProviderUnavailable) {
				t.Fatalf("manifest error = %v, want %v", err, ErrProviderUnavailable)
			}
			if !errors.Is(err, netguard.ErrOutboundDestinationNotPermitted) {
				t.Fatalf("manifest error does not preserve NAT64 destination rejection: %v", err)
			}
		})
	}
}

func TestHTTPTransportRejectsRedirectToNonPublicNetworkBeforeConnecting(t *testing.T) {
	for _, value := range []string{
		"192.168.1.10",
		"[64:ff9b::7f00:1]",
		"[64:ff9b::a00:1]",
		"[64:ff9b::ac10:1]",
		"[64:ff9b::c0a8:1]",
		"[64:ff9b::6440:1]",
		"[64:ff9b::a9fe:a9fe]",
	} {
		t.Run(value, func(t *testing.T) {
			redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				http.Redirect(w, request, "http://"+value+"/manifest.json", http.StatusFound)
			}))
			t.Cleanup(redirector.Close)

			httpTransport := http.DefaultTransport.(*http.Transport).Clone()
			httpTransport.Proxy = nil
			httpTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address == "public-addon.example:80" {
					return (&net.Dialer{}).DialContext(ctx, network, redirector.Listener.Addr().String())
				}
				return netguard.DialContextPublic(ctx, network, address)
			}
			transport := NewHTTPTransport(&http.Client{Transport: httpTransport})
			_, _, err := transport.Manifest(context.Background(), "http://public-addon.example/manifest.json")
			if !errors.Is(err, ErrProviderUnavailable) {
				t.Fatalf("redirected manifest error = %v, want %v", err, ErrProviderUnavailable)
			}
			if !errors.Is(err, netguard.ErrOutboundDestinationNotPermitted) {
				t.Fatalf("redirected manifest did not preserve destination rejection: %v", err)
			}
		})
	}
}

func TestBudgetedPayloadReaderStopsAtAggregateNPlusOneAndCancelsContext(t *testing.T) {
	ctx, budget := WithPayloadBudget(context.Background(), 4, 1)
	defer budget.Cancel()
	sourceCtx := WithPayloadBudgetSource(ctx)
	source := bytes.NewBufferString("12345")

	payload, err := io.ReadAll(BudgetedPayloadReader(sourceCtx, source))
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("budgeted read error = %v, want %v", err, ErrInvalidResponse)
	}
	if string(payload) != "1234" || source.Len() != 0 {
		t.Fatalf("budgeted read retained %q with %d unread bytes, want exact budget plus one-byte probe", payload, source.Len())
	}
	if !budget.Exceeded() {
		t.Fatal("N+1 read did not mark payload budget exceeded")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("N+1 read did not cancel the shared context")
	}
}
