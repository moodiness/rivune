package artwork

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

type transportPolicy struct {
	lanOrigins      map[string]netip.AddrPort
	lanDestinations map[netip.AddrPort]struct{}
}

func newTransportPolicy(origins []string) (transportPolicy, error) {
	if len(origins) > 32 {
		return transportPolicy{}, errors.New("too many LAN artwork origins")
	}
	policy := transportPolicy{
		lanOrigins:      make(map[string]netip.AddrPort, len(origins)),
		lanDestinations: make(map[netip.AddrPort]struct{}, len(origins)),
	}
	for _, raw := range origins {
		origin, err := netguard.ParsePrivateOrigin(raw)
		if err != nil {
			return transportPolicy{}, fmt.Errorf("invalid LAN artwork origin: %w", err)
		}
		policy.lanOrigins[origin.Origin] = origin.Address
		policy.lanDestinations[origin.Address] = struct{}{}
	}
	return policy, nil
}

func normalizeURL(raw string, strict bool) (string, error) {
	return normalizeURLWithPolicy(raw, strict, transportPolicy{})
}

func normalizeURLWithPolicy(raw string, strict bool, policy transportPolicy) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("empty artwork URL")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse artwork URL: %w", netguard.SanitizeURLError(err))
	}
	if parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" || strings.Contains(parsed.Host, "\\") {
		return "", errors.New("artwork URL must be absolute")
	}
	if parsed.User != nil {
		return "", errors.New("artwork URL must not contain user information")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if !strict {
		if scheme != "http" && scheme != "https" {
			return "", errors.New("artwork URL must use HTTP or HTTPS")
		}
	} else if scheme != "http" && scheme != "https" {
		return "", errors.New("artwork URL must use HTTPS or an allowed LAN origin")
	}
	parsed.Scheme = scheme

	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" || strings.Contains(host, "%") {
		return "", errors.New("artwork URL has an invalid host")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return "", errors.New("artwork URL has an invalid port")
	}
	port := parsed.Port()
	effectivePort := port
	if effectivePort == "" {
		switch scheme {
		case "http":
			effectivePort = "80"
		case "https":
			effectivePort = "443"
		}
	}

	lanAllowed := false
	lanHost := ""
	if strict {
		if address, parseErr := netip.ParseAddr(host); parseErr == nil && !address.Is4In6() {
			address = address.Unmap()
			origin := scheme + "://" + net.JoinHostPort(address.String(), effectivePort)
			if expected, exists := policy.lanOrigins[origin]; exists {
				candidate, addressErr := netip.ParseAddrPort(net.JoinHostPort(address.String(), effectivePort))
				lanAllowed = addressErr == nil && candidate == expected
				if lanAllowed {
					lanHost = address.String()
				}
			}
		}
		if !lanAllowed {
			if scheme != "https" {
				return "", errors.New("artwork URL must use HTTPS")
			}
			if effectivePort != "443" {
				return "", errors.New("artwork URL must use port 443")
			}
			if host == "localhost" || strings.HasSuffix(host, ".localhost") {
				return "", errors.New("artwork URL host is not public")
			}
			if address, parseErr := netip.ParseAddr(host); parseErr == nil && !netguard.IsPublicAddress(address) {
				return "", errors.New("artwork URL address is not public")
			}
		}
	}

	if lanAllowed {
		parsed.Host = net.JoinHostPort(lanHost, effectivePort)
	} else if port != "" && effectivePort != "443" {
		parsed.Host = net.JoinHostPort(host, effectivePort)
	} else if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	} else {
		parsed.Host = host
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

func newProductionHTTPClient(policies ...transportPolicy) *http.Client {
	var policy transportPolicy
	if len(policies) > 0 {
		policy = policies[0]
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.MaxResponseHeaderBytes = 64 << 10
	transport.DialContext = policy.dialContext
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			request.Header.Del("Referer")
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if _, err := normalizeURLWithPolicy(request.URL.String(), true, policy); err != nil {
				return fmt.Errorf("reject artwork redirect: %w", err)
			}
			return nil
		},
	}
}

func strictDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (transportPolicy{}).dialContext(ctx, network, address)
}

func (policy transportPolicy) allowedLANDestination(destination string) (netip.AddrPort, bool) {
	host, port, err := net.SplitHostPort(destination)
	if err != nil {
		return netip.AddrPort{}, false
	}
	address, err := netip.ParseAddr(host)
	if err != nil || address.Is4In6() {
		return netip.AddrPort{}, false
	}
	address = address.Unmap()
	candidate, err := netip.ParseAddrPort(net.JoinHostPort(address.String(), port))
	if err != nil {
		return netip.AddrPort{}, false
	}
	_, allowed := policy.lanDestinations[candidate]
	return candidate, allowed
}

func (policy transportPolicy) dialContext(ctx context.Context, network, destination string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(destination)
	if err != nil {
		return nil, errors.New("parse artwork destination")
	}
	if candidate, allowed := policy.allowedLANDestination(destination); allowed {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		return dialer.DialContext(ctx, network, candidate.String())
	}
	if port != "443" {
		return nil, errors.New("artwork destination must use port 443")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("resolve artwork destination")
	}
	if len(addresses) == 0 {
		return nil, errors.New("artwork destination resolved to no addresses")
	}
	for _, candidate := range addresses {
		if !netguard.IsPublicAddress(candidate) {
			return nil, errors.New("artwork destination resolved to non-public address")
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastError error
	for _, candidate := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastError = dialErr
	}
	if lastError != nil {
		return nil, errors.New("connect to artwork destination")
	}
	return nil, errors.New("artwork destination unavailable")
}
