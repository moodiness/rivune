package metadata

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/netguard"
)

const maximumProviderRedirects = 5

// NewProviderHTTPClient returns the production transport used for a single
// metadata provider origin. Redirects never leave that HTTPS origin, and every
// dial revalidates DNS results so provider DNS cannot reach a special-use host.
func NewProviderHTTPClient(baseURL string, timeout time.Duration) *http.Client {
	expected, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(expected.Scheme, "https") || expected.Hostname() == "" || expected.User != nil {
		panic("metadata provider base URL must be an absolute HTTPS URL without user information")
	}
	expectedHost := strings.ToLower(strings.TrimSuffix(expected.Hostname(), "."))
	expectedPort := expected.Port()
	if expectedPort == "" {
		expectedPort = "443"
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 64 << 10
	transport.DialContext = dialPublicProvider
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maximumProviderRedirects {
				return errors.New("metadata provider redirect limit exceeded")
			}
			if request.URL.User != nil || !strings.EqualFold(request.URL.Scheme, "https") {
				return errors.New("metadata provider redirect must use HTTPS without user information")
			}
			redirectHost := strings.ToLower(strings.TrimSuffix(request.URL.Hostname(), "."))
			redirectPort := request.URL.Port()
			if redirectPort == "" {
				redirectPort = "443"
			}
			if redirectHost != expectedHost || redirectPort != expectedPort {
				return errors.New("metadata provider cross-origin redirect refused")
			}
			if address, parseErr := netip.ParseAddr(redirectHost); parseErr == nil && !netguard.IsPublicAddress(address) {
				return errors.New("metadata provider redirect address is not public")
			}
			return nil
		},
	}
}

func dialPublicProvider(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse metadata provider destination: %w", err)
	}
	if port != "443" {
		return nil, errors.New("metadata provider destination must use port 443")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata provider destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("metadata provider destination resolved to no addresses")
	}
	for _, candidate := range addresses {
		if !netguard.IsPublicAddress(candidate) {
			return nil, errors.New("metadata provider destination resolved to a non-public address")
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, candidate := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("connect to metadata provider: %w", lastErr)
}
