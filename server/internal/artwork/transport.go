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
)

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func normalizeURL(raw string, strict bool) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("empty artwork URL")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse artwork URL: %w", err)
	}
	if parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return "", errors.New("artwork URL must be absolute")
	}
	if parsed.User != nil {
		return "", errors.New("artwork URL must not contain user information")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if strict {
		if scheme != "https" {
			return "", errors.New("artwork URL must use HTTPS")
		}
	} else if scheme != "http" && scheme != "https" {
		return "", errors.New("artwork URL must use HTTP or HTTPS")
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
	if strict && port != "" && port != "443" {
		return "", errors.New("artwork URL must use port 443")
	}
	if strict {
		port = ""
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return "", errors.New("artwork URL host is not public")
		}
		if address, parseErr := netip.ParseAddr(host); parseErr == nil && !isPublicAddress(address) {
			return "", errors.New("artwork URL address is not public")
		}
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
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

func newProductionHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.MaxResponseHeaderBytes = 64 << 10
	transport.DialContext = strictDialContext
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if _, err := normalizeURL(request.URL.String(), true); err != nil {
				return fmt.Errorf("reject artwork redirect: %w", err)
			}
			return nil
		},
	}
}

func strictDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse artwork destination: %w", err)
	}
	if port != "443" {
		return nil, errors.New("artwork destination must use port 443")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve artwork destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("artwork destination resolved to no addresses")
	}
	for _, candidate := range addresses {
		if !isPublicAddress(candidate) {
			return nil, fmt.Errorf("artwork destination resolved to non-public address %s", candidate)
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
	return nil, fmt.Errorf("connect to artwork destination: %w", lastError)
}

func isPublicAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
