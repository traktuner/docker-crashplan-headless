package auth

import (
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

// maxForwardedHops bounds how far the declared proxy chain is followed.
const maxForwardedHops = 16

var forwardedWarning sync.Once

// ClientIP trusts only a declared proxy chain. Request forwarding headers have
// no effect by default and never influence public-origin validation.
//
// The result is only the login throttle bucket key. A malformed or overlong
// X-Forwarded-For never rejects a request: the walk from the right stops at
// the first entry that does not parse (or after 16 hops) and keeps the last
// address that did, which is a trusted proxy. All header lines are read, so a
// client line before the proxy's own line cannot choose the bucket. The only
// error is a peer address that does not parse.
func ClientIP(r *http.Request, trusted []netip.Prefix) (string, error) {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return "", ErrUnauthorized
	}
	address := peer.Addr().Unmap().WithZone("")
	isTrusted := func(address netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(address) {
				return true
			}
		}
		return false
	}
	values := r.Header.Values("X-Forwarded-For")
	if !isTrusted(address) || len(values) == 0 {
		return address.String(), nil
	}
	chain := strings.Split(strings.Join(values, ","), ",")
	hops := 0
	for i := len(chain) - 1; i >= 0 && isTrusted(address); i-- {
		entry := strings.TrimSpace(chain[i])
		if entry == "" {
			continue // an empty list element, e.g. a trailing comma
		}
		if hops == maxForwardedHops {
			warnForwarded("too_many_hops")
			break
		}
		hops++
		candidate, ok := forwardedAddress(entry)
		if !ok {
			warnForwarded("unparsable_entry")
			break
		}
		address = candidate
	}
	return address.String(), nil
}

// forwardedAddress accepts an IP, an IPv6 address with a zone, ip:port and
// [v6]:port, as proxies write them. The zone is dropped.
func forwardedAddress(entry string) (netip.Addr, bool) {
	if address, err := netip.ParseAddr(entry); err == nil {
		return address.Unmap().WithZone(""), true
	}
	if addrPort, err := netip.ParseAddrPort(entry); err == nil {
		return addrPort.Addr().Unmap().WithZone(""), true
	}
	if strings.HasPrefix(entry, "[") && strings.HasSuffix(entry, "]") {
		if address, err := netip.ParseAddr(entry[1 : len(entry)-1]); err == nil {
			return address.Unmap().WithZone(""), true
		}
	}
	return netip.Addr{}, false
}

// warnForwarded logs once per process; never the header value.
func warnForwarded(reason string) {
	forwardedWarning.Do(func() {
		slog.Warn("forwarded_for_ignored", "reason", reason, "hint", "the trusted proxy sent an X-Forwarded-For entry that is not an address; its own address is used for login throttling")
	})
}
