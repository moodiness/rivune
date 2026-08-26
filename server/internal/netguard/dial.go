package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var ErrOutboundDestinationNotPermitted = errors.New("outbound destination is not permitted")

var nat64WellKnownPrefix = netip.MustParsePrefix("64:ff9b::/96")

type nat64PrefixSet struct {
	prefixes []netip.Prefix
}

var configuredNAT64Prefixes atomic.Pointer[nat64PrefixSet]

func init() {
	configuredNAT64Prefixes.Store(&nat64PrefixSet{prefixes: []netip.Prefix{nat64WellKnownPrefix}})
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.100.100.200/32"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
	netip.MustParsePrefix("fd20:ce::254/128"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

var privateNetworkPrefixes = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
}

type PrivateOrigin struct {
	Origin  string
	Address netip.AddrPort
}

// ParsePrivateOrigin accepts one exact HTTP(S) origin backed by a literal LAN
// address. DNS names and special-use destinations stay outside this opt-in.
func ParsePrivateOrigin(raw string) (PrivateOrigin, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return PrivateOrigin{}, errors.New("private origin must be an exact HTTP(S) origin")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" ||
		(scheme != "http" && scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" {
		return PrivateOrigin{}, errors.New("private origin must be an exact HTTP(S) origin")
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	if err != nil || address.Is4In6() {
		return PrivateOrigin{}, errors.New("private origin host must be a canonical IP literal")
	}
	address = address.Unmap()
	if !IsAllowedAddress(address) || !IsPrivateNetworkAddress(address) {
		return PrivateOrigin{}, errors.New("private origin address must be an allowed LAN address")
	}
	portText := parsed.Port()
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return PrivateOrigin{}, errors.New("private origin must include an explicit TCP port")
	}
	addressPort := netip.AddrPortFrom(address, uint16(port))
	origin := scheme + "://" + net.JoinHostPort(address.String(), strconv.FormatUint(port, 10))
	return PrivateOrigin{Origin: origin, Address: addressPort}, nil
}

type ipResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// ConfigureNAT64Prefixes installs deployment-specific RFC 6052 translation
// prefixes in addition to the well-known 64:ff9b::/96 prefix. Configuration
// must complete before outbound clients begin serving requests.
func ConfigureNAT64Prefixes(prefixes []netip.Prefix) error {
	configured := make([]netip.Prefix, 1, len(prefixes)+1)
	configured[0] = nat64WellKnownPrefix
	for _, prefix := range prefixes {
		prefix = prefix.Masked()
		if !prefix.IsValid() || !prefix.Addr().Is6() || !validNAT64PrefixLength(prefix.Bits()) {
			return fmt.Errorf("invalid RFC 6052 NAT64 prefix %q", prefix)
		}
		for _, existing := range configured {
			if existing.Contains(prefix.Addr()) || prefix.Contains(existing.Addr()) {
				return fmt.Errorf("overlapping RFC 6052 NAT64 prefixes %q and %q", existing, prefix)
			}
		}
		configured = append(configured, prefix)
	}
	configuredNAT64Prefixes.Store(&nat64PrefixSet{prefixes: configured})
	return nil
}

func validNAT64PrefixLength(bits int) bool {
	switch bits {
	case 32, 40, 48, 56, 64, 96:
		return true
	default:
		return false
	}
}

// SanitizeURLError removes request destinations from URL errors while
// preserving their operation and unwrap chain. HTTP clients include the full
// request URL in these errors, including paths and query credentials.
func SanitizeURLError(err error) error {
	sanitized, _ := sanitizeURLError(err)
	return sanitized
}

type sanitizedWrappedError struct {
	message string
	cause   error
}

func (err *sanitizedWrappedError) Error() string {
	return err.message
}

func (err *sanitizedWrappedError) Unwrap() error {
	return err.cause
}

type sanitizedJoinedError struct {
	message string
	causes  []error
}

func (err *sanitizedJoinedError) Error() string {
	return err.message
}

func (err *sanitizedJoinedError) Unwrap() []error {
	return err.causes
}

func sanitizeURLError(err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	if urlErr, ok := err.(*url.Error); ok {
		if urlErr == nil {
			return err, false
		}
		cause, _ := sanitizeURLError(urlErr.Err)
		return &url.Error{Op: urlErr.Op, Err: cause}, true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		originalCause := wrapped.Unwrap()
		cause, changed := sanitizeURLError(originalCause)
		if !changed {
			return err, false
		}
		return &sanitizedWrappedError{
			message: replaceErrorCause(err.Error(), originalCause, cause),
			cause:   cause,
		}, true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		originalCauses := joined.Unwrap()
		causes := make([]error, len(originalCauses))
		message := err.Error()
		changed := false
		for index, originalCause := range originalCauses {
			cause, causeChanged := sanitizeURLError(originalCause)
			causes[index] = cause
			if causeChanged {
				changed = true
				message = replaceErrorCause(message, originalCause, cause)
			}
		}
		if changed {
			return &sanitizedJoinedError{message: message, causes: causes}, true
		}
	}
	return err, false
}

func replaceErrorCause(message string, original, sanitized error) string {
	if original == nil || sanitized == nil {
		return message
	}
	originalMessage := original.Error()
	if originalMessage == "" {
		return message
	}
	return strings.ReplaceAll(message, originalMessage, sanitized.Error())
}

// DialContext permits public and private-network providers while rejecting local,
// link-local, metadata-service, documentation, multicast, and reserved addresses.
func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialContext(ctx, network, address, allowedAddress)
}

// DialContextPublic restricts user-controlled destinations to public networks.
// Resolution is repeated for every connection, including redirected requests.
func DialContextPublic(ctx context.Context, network, address string) (net.Conn, error) {
	return dialContext(ctx, network, address, IsPublicAddress)
}

// DialContextLocal restricts a configured local service to loopback and
// private-network destinations while retaining DNS rebinding protection.
func DialContextLocal(ctx context.Context, network, address string) (net.Conn, error) {
	return dialContext(ctx, network, address, isLocalServiceAddress)
}

func isLocalServiceAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	return address.IsLoopback() || allowedAddress(address) && IsPrivateNetworkAddress(address)
}

func dialContext(ctx context.Context, network, address string, permitted func(netip.Addr) bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse outbound destination: %w", err)
	}
	addresses, err := resolveAddresses(ctx, host, net.DefaultResolver, permitted)
	if err != nil {
		return nil, err
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
	return nil, fmt.Errorf("connect to outbound destination: %w", lastError)
}

// ValidateURL resolves an HTTP(S) destination immediately before a subprocess
// receives it. Subprocesses cannot use DialContext, so this preserves the same
// sensitive-address policy at their closest enforceable network boundary.
func ValidateURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse outbound URL: %w", SanitizeURLError(err))
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("outbound URL is not permitted")
	}
	if strings.Contains(parsed.Host, "\\") {
		return errors.New("outbound URL is not permitted")
	}
	_, err = resolveAllowedAddresses(ctx, parsed.Hostname(), net.DefaultResolver)
	return err
}

func resolveAllowedAddresses(ctx context.Context, host string, resolver ipResolver) ([]netip.Addr, error) {
	return resolveAddresses(ctx, host, resolver, allowedAddress)
}

func resolveAddresses(ctx context.Context, host string, resolver ipResolver, permitted func(netip.Addr) bool) ([]netip.Addr, error) {
	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve outbound destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("outbound destination resolved to no addresses")
	}
	for _, candidate := range addresses {
		if !permitted(candidate) {
			return nil, ErrOutboundDestinationNotPermitted
		}
	}
	return addresses, nil
}

func allowedAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

// IsAllowedAddress reports whether address is a public or private-network
// destination while excluding loopback, link-local, metadata, documentation,
// multicast, and reserved ranges.
func IsAllowedAddress(address netip.Addr) bool {
	return allowedAddress(address)
}

// IsPrivateNetworkAddress reports whether address belongs to an explicit
// RFC1918, shared-address-space, or unique-local private network. Translated
// and special-use addresses are not treated as directly addressed LAN hosts.
func IsPrivateNetworkAddress(address netip.Addr) bool {
	address = address.Unmap()
	if _, matched, _ := translatedIPv4(address); matched {
		return false
	}
	for _, prefix := range privateNetworkPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// IsPublicAddress reports whether address is globally routable and outside
// private and special-use ranges that public-only egress must never reach.
func IsPublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if embedded, matched, valid := translatedIPv4(address); matched {
		return valid && isPublicAddress(embedded)
	}
	return isPublicAddress(address)
}

func translatedIPv4(address netip.Addr) (netip.Addr, bool, bool) {
	if !address.Is6() {
		return netip.Addr{}, false, false
	}
	bytes := address.As16()
	for _, prefix := range configuredNAT64Prefixes.Load().prefixes {
		if !prefix.Contains(address) {
			continue
		}
		var embedded [4]byte
		if prefix.Bits() == 96 {
			copy(embedded[:], bytes[12:16])
			return netip.AddrFrom4(embedded), true, true
		}
		if bytes[8] != 0 {
			return netip.Addr{}, true, false
		}
		prefixBytes := prefix.Bits() / 8
		beforeReservedOctet := 8 - prefixBytes
		copy(embedded[:beforeReservedOctet], bytes[prefixBytes:8])
		copy(embedded[beforeReservedOctet:], bytes[9:9+4-beforeReservedOctet])
		return netip.AddrFrom4(embedded), true, true
	}
	return netip.Addr{}, false, false
}

func isPublicAddress(address netip.Addr) bool {
	if !allowedAddress(address) {
		return false
	}
	address = address.Unmap()
	for _, prefix := range privateNetworkPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
