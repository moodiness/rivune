package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func requestClientIP(request *http.Request, trustedProxies []netip.Prefix) string {
	remoteIP, ok := parseIPAddress(request.RemoteAddr)
	if !ok {
		return ""
	}
	if !isTrustedProxy(remoteIP, trustedProxies) {
		return remoteIP.String()
	}

	forwarded := strings.Join(request.Header.Values("X-Forwarded-For"), ",")
	addresses := strings.Split(forwarded, ",")
	var leftmost netip.Addr
	for index, raw := range addresses {
		address, valid := parseIPAddress(strings.TrimSpace(raw))
		if !valid {
			continue
		}
		if index == 0 || !leftmost.IsValid() {
			leftmost = address
		}
	}
	for index := len(addresses) - 1; index >= 0; index-- {
		address, valid := parseIPAddress(strings.TrimSpace(addresses[index]))
		if valid && !isTrustedProxy(address, trustedProxies) {
			return address.String()
		}
	}
	if leftmost.IsValid() {
		return leftmost.String()
	}
	if address, valid := parseIPAddress(request.Header.Get("X-Real-IP")); valid {
		return address.String()
	}
	return remoteIP.String()
}

func parseIPAddress(value string) (netip.Addr, bool) {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func isTrustedProxy(address netip.Addr, trustedProxies []netip.Prefix) bool {
	for _, network := range trustedProxies {
		if network.Contains(address) {
			return true
		}
	}
	return false
}
