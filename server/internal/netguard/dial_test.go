package netguard

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type fixedResolver []netip.Addr

func (resolver fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return resolver, nil
}

func TestAllowedAddressPreservesPrivateProvidersAndRejectsSensitiveDestinations(t *testing.T) {
	for _, value := range []string{
		"1.1.1.1",
		"10.0.0.10",
		"100.64.0.10",
		"172.30.0.10",
		"192.168.1.10",
		"2606:4700:4700::1111",
		"fd12:3456:789a::10",
	} {
		if !allowedAddress(netip.MustParseAddr(value)) {
			t.Fatalf("expected provider address %s to remain permitted", value)
		}
	}

	for _, value := range []string{
		"0.0.0.0",
		"127.0.0.1",
		"169.254.169.254",
		"100.100.100.200",
		"192.0.2.1",
		"224.0.0.1",
		"::1",
		"fe80::1",
		"fd00:ec2::254",
		"fd20:ce::254",
		"ff02::1",
	} {
		if allowedAddress(netip.MustParseAddr(value)) {
			t.Fatalf("expected sensitive address %s to be rejected", value)
		}
	}
}

func TestLocalServiceAddressAllowsOnlyLoopbackAndPrivateNetworks(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "::1", "10.0.0.10", "192.168.1.10", "fd12::10"} {
		if !isLocalServiceAddress(netip.MustParseAddr(value)) {
			t.Fatalf("local service address %s was rejected", value)
		}
	}
	for _, value := range []string{"1.1.1.1", "169.254.169.254", "192.0.2.1", "64:ff9b::a00:1"} {
		if isLocalServiceAddress(netip.MustParseAddr(value)) {
			t.Fatalf("non-local service address %s was accepted", value)
		}
	}
}

func TestPublicAddressClassifiesNAT64WellKnownPrefixByEmbeddedIPv4(t *testing.T) {
	for _, value := range []string{
		"10.0.0.10",
		"100.64.0.10",
		"172.30.0.10",
		"192.168.1.10",
		"fd12:3456:789a::10",
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
		if IsPublicAddress(netip.MustParseAddr(value)) {
			t.Fatalf("expected private or local-use address %s to be rejected for public-only egress", value)
		}
	}
	for _, value := range []string{
		"1.1.1.1",
		"64:ff9b::101:101",
		"2606:4700:4700::1111",
	} {
		if !IsPublicAddress(netip.MustParseAddr(value)) {
			t.Fatalf("expected public address %s to remain permitted", value)
		}
	}
}

func TestPublicAddressClassifiesEveryConfiguredRFC6052PrefixByEmbeddedIPv4(t *testing.T) {
	prefixes := []string{
		"2001:db8::/32",
		"2001:db8:aa00::/40",
		"2001:db8:aa01::/48",
		"2001:db8:aa02:bb00::/56",
		"2001:db8:aa03:bb04::/64",
		"2001:db8:aa05:bb06:cc07:dd00::/96",
	}
	t.Cleanup(func() {
		if err := ConfigureNAT64Prefixes(nil); err != nil {
			t.Fatalf("restore NAT64 prefixes: %v", err)
		}
	})
	for _, rawPrefix := range prefixes {
		prefix := netip.MustParsePrefix(rawPrefix)
		if err := ConfigureNAT64Prefixes([]netip.Prefix{prefix}); err != nil {
			t.Fatalf("configure NAT64 prefix %s: %v", prefix, err)
		}
		for _, rawIPv4 := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254"} {
			translated := synthesizeNAT64Address(prefix, netip.MustParseAddr(rawIPv4))
			if IsPublicAddress(translated) {
				t.Fatalf("configured prefix %s exposed embedded address %s as %s", prefix, rawIPv4, translated)
			}
		}
		public := synthesizeNAT64Address(prefix, netip.MustParseAddr("1.1.1.1"))
		if !IsPublicAddress(public) {
			t.Fatalf("configured prefix %s rejected public embedded address %s", prefix, public)
		}
	}
}

func TestConfigureNAT64PrefixesRejectsInvalidOrOverlappingNetworks(t *testing.T) {
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("2001:db8::/72"),
		netip.MustParsePrefix("2001:db8::/32"),
	} {
		configured := []netip.Prefix{prefix}
		if prefix.Bits() == 32 {
			configured = append(configured, netip.MustParsePrefix("2001:db8:1::/48"))
		}
		if err := ConfigureNAT64Prefixes(configured); err == nil {
			t.Fatalf("accepted invalid NAT64 prefixes %v", configured)
		}
	}
}

func synthesizeNAT64Address(prefix netip.Prefix, ipv4 netip.Addr) netip.Addr {
	bytes := prefix.Addr().As16()
	ipv4Bytes := ipv4.As4()
	if prefix.Bits() == 96 {
		copy(bytes[12:16], ipv4Bytes[:])
		return netip.AddrFrom16(bytes)
	}
	prefixBytes := prefix.Bits() / 8
	beforeReservedOctet := 8 - prefixBytes
	copy(bytes[prefixBytes:8], ipv4Bytes[:beforeReservedOctet])
	bytes[8] = 0
	copy(bytes[9:9+4-beforeReservedOctet], ipv4Bytes[beforeReservedOctet:])
	return netip.AddrFrom16(bytes)
}

func TestResolvePublicAddressesRejectsMixedDNSRebindingAnswers(t *testing.T) {
	addresses, err := resolveAddresses(context.Background(), "provider.example", fixedResolver{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("192.168.1.10"),
	}, IsPublicAddress)
	if addresses != nil {
		t.Fatalf("mixed public and private answers were returned: %v", addresses)
	}
	if !errors.Is(err, ErrOutboundDestinationNotPermitted) {
		t.Fatalf("mixed DNS answer error = %v", err)
	}
}

func TestDialContextPublicRejectsNAT64NonPublicDestinationsBeforeConnecting(t *testing.T) {
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
		connection, err := DialContextPublic(context.Background(), "tcp", net.JoinHostPort(value, "443"))
		if connection != nil {
			_ = connection.Close()
			t.Fatalf("unexpected connection to NAT64 non-public destination %s", value)
		}
		if !errors.Is(err, ErrOutboundDestinationNotPermitted) {
			t.Fatalf("NAT64 non-public destination %s error = %v", value, err)
		}
	}
}

func TestDialContextPublicRejectsNAT64NonPublicRedirectsBeforeConnecting(t *testing.T) {
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
		t.Run(value, func(t *testing.T) {
			redirector := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				http.Redirect(response, request, "http://["+value+"]/private", http.StatusFound)
			}))
			t.Cleanup(redirector.Close)

			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address == "public.example:80" {
					return (&net.Dialer{}).DialContext(ctx, network, redirector.Listener.Addr().String())
				}
				return DialContextPublic(ctx, network, address)
			}
			client := &http.Client{Transport: transport}
			response, err := client.Get("http://public.example/start")
			if response != nil {
				_ = response.Body.Close()
				t.Fatalf("unexpected response from NAT64 non-public destination %s", value)
			}
			if !errors.Is(err, ErrOutboundDestinationNotPermitted) {
				t.Fatalf("NAT64 non-public redirect %s error = %v", value, err)
			}
		})
	}
}

func TestDialContextRejectsLoopbackBeforeConnecting(t *testing.T) {
	connection, err := DialContext(context.Background(), "tcp", "127.0.0.1:80")
	if connection != nil {
		_ = connection.Close()
		t.Fatal("unexpected loopback connection")
	}
	if err == nil {
		t.Fatal("expected loopback destination to be rejected")
	}
}

func TestResolveAllowedAddressesRejectsMixedDNSRebindingAnswers(t *testing.T) {
	addresses, err := resolveAllowedAddresses(context.Background(), "provider.example", fixedResolver{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("169.254.169.254"),
	})
	if addresses != nil {
		t.Fatalf("mixed public and metadata answers were returned: %v", addresses)
	}
	if err == nil || err.Error() != "outbound destination is not permitted" {
		t.Fatalf("mixed DNS answer error = %v", err)
	}
}

func TestSanitizeURLErrorRemovesDestinationsAndPreservesCauses(t *testing.T) {
	secretURL := "https://provider.example/private/master.m3u8?target=segment.ts&token=network-secret"
	dnsCause := &net.DNSError{Err: "no such host", Name: "provider.example"}
	original := fmt.Errorf("fetch media: %w", &url.Error{
		Op:  "Get",
		URL: secretURL,
		Err: &url.Error{Op: "redirect", URL: "https://cdn.example/secret/key.bin?token=redirect-secret", Err: dnsCause},
	})

	sanitized := SanitizeURLError(original)

	if strings.Contains(sanitized.Error(), "/private/") ||
		strings.Contains(sanitized.Error(), "network-secret") ||
		strings.Contains(sanitized.Error(), "redirect-secret") {
		t.Fatalf("sanitized error exposed a request destination: %v", sanitized)
	}
	if !errors.Is(sanitized, dnsCause) {
		t.Fatalf("sanitized error lost DNS cause: %v", sanitized)
	}
	var resolvedDNS *net.DNSError
	if !errors.As(sanitized, &resolvedDNS) || resolvedDNS != dnsCause {
		t.Fatalf("sanitized error lost typed DNS cause: %v", sanitized)
	}
	var outer *url.Error
	if !errors.As(sanitized, &outer) || outer.Op != "Get" || outer.URL != "" {
		t.Fatalf("outer URL error was not sanitized with its operation intact: %#v", outer)
	}
	inner, ok := outer.Unwrap().(*url.Error)
	if !ok || inner.Op != "redirect" || inner.URL != "" {
		t.Fatalf("redirect URL error was not sanitized with its operation intact: %#v", outer.Unwrap())
	}
}

func TestSanitizeURLErrorPreservesTLSErrorType(t *testing.T) {
	tlsCause := tls.RecordHeaderError{Msg: "TLS handshake failed"}
	sanitized := SanitizeURLError(&url.Error{
		Op:  "Get",
		URL: "https://provider.example/private/video.mp4?token=tls-secret",
		Err: tlsCause,
	})

	var resolvedTLS tls.RecordHeaderError
	if !errors.As(sanitized, &resolvedTLS) || resolvedTLS.Msg != tlsCause.Msg {
		t.Fatalf("sanitized error lost typed TLS cause: %v", sanitized)
	}
	if strings.Contains(sanitized.Error(), "tls-secret") || strings.Contains(sanitized.Error(), "/private/") {
		t.Fatalf("sanitized TLS error exposed request destination: %v", sanitized)
	}
}

func TestValidateURLSanitizesMalformedDestination(t *testing.T) {
	err := ValidateURL(
		context.Background(),
		"https://provider.example/private/%zz?target=segment.ts&token=parse-secret",
	)
	if err == nil {
		t.Fatal("malformed outbound URL was accepted")
	}
	if strings.Contains(err.Error(), "/private/") || strings.Contains(err.Error(), "parse-secret") {
		t.Fatalf("outbound URL parse error exposed destination: %v", err)
	}
	var parseErr *url.Error
	if !errors.As(err, &parseErr) || parseErr.Op != "parse" || parseErr.URL != "" {
		t.Fatalf("outbound parse URL error was not sanitized: %#v", parseErr)
	}
}

func TestParsePrivateOriginRequiresExactAllowedIPLiteral(t *testing.T) {
	t.Parallel()
	accepted := map[string]string{
		"http://192.168.1.48:63113": "http://192.168.1.48:63113",
		"https://[fd12::48]:8443/":  "https://[fd12::48]:8443",
		"HTTP://192.168.1.48:63113": "http://192.168.1.48:63113",
	}
	for raw, expected := range accepted {
		origin, err := ParsePrivateOrigin(raw)
		if err != nil {
			t.Fatalf("parse private origin %q: %v", raw, err)
		}
		if origin.Origin != expected {
			t.Fatalf("private origin %q normalized to %q, want %q", raw, origin.Origin, expected)
		}
	}

	for _, raw := range []string{
		"http://lan.example:8080",
		"http://user@192.168.1.48:8080",
		"http://192.168.1.48:8080/path",
		"http://192.168.1.48:8080?key=secret",
		"http://192.168.1.48:8080/#fragment",
		"http://192.168.1.48",
		"http://192.168.1.48:8080/%zz",
		"http://127.0.0.1:8080",
		"http://169.254.169.254:80",
		"http://192.0.2.1:8080",
		"http://8.8.8.8:8080",
		"ftp://192.168.1.48:21",
	} {
		if _, err := ParsePrivateOrigin(raw); err == nil {
			t.Errorf("unsafe private origin %q was accepted", raw)
		}
	}
}
