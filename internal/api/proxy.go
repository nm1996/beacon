package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies are the address ranges of proxies, such as load
// balancers, whose X-Forwarded-For header Beacon believes. The zero value
// trusts nobody, so the connection's address is always the client's.
type TrustedProxies []netip.Prefix

// trusts reports whether address is in one of the ranges.
func (proxies TrustedProxies) trusts(address netip.Addr) bool {
	for _, prefix := range proxies {
		if prefix.Contains(address) {
			return true
		}
	}

	return false
}

// clientAddress returns the address of the client that sent request, or
// false if the connection's address cannot be parsed.
//
// When the connection comes from a trusted proxy, it reads X-Forwarded-For
// from right to left and takes the first address that is not itself a
// trusted proxy. Everything left of that was written by the client and is
// never believed. A header that is missing, or that it cannot parse before
// reaching an untrusted address, leaves the connection's address as the
// client's.
func (proxies TrustedProxies) clientAddress(request *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}

	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}

	peer = peer.Unmap().WithZone("")
	if len(proxies) == 0 || !proxies.trusts(peer) {
		return peer, true
	}

	// Every X-Forwarded-For header in order, as one comma-separated list.
	hops := strings.Split(strings.Join(request.Header.Values("X-Forwarded-For"), ","), ",")

	leftmost := peer

	for index := len(hops) - 1; index >= 0; index-- {
		hop := strings.TrimSpace(hops[index])
		if hop == "" {
			continue
		}

		address, ok := parseHop(hop)
		if !ok {
			return peer, true
		}

		if !proxies.trusts(address) {
			return address, true
		}

		leftmost = address
	}

	// Every hop is a trusted proxy, so the request started inside.
	return leftmost, true
}

// parseHop parses one X-Forwarded-For entry: an address, with or without a
// port, and with brackets around IPv6 when there is a port.
func parseHop(hop string) (netip.Addr, bool) {
	if address, err := netip.ParseAddr(hop); err == nil {
		return address.Unmap().WithZone(""), true
	}

	if addressPort, err := netip.ParseAddrPort(hop); err == nil {
		return addressPort.Addr().Unmap().WithZone(""), true
	}

	return netip.Addr{}, false
}
