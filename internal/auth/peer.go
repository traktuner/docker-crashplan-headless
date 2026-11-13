package auth

import (
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP trusts only a declared proxy chain. Request forwarding headers have
// no effect by default and never influence public-origin validation.
func ClientIP(r *http.Request, trusted []netip.Prefix) (string, error) {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return "", ErrUnauthorized
	}
	address := peer.Addr().Unmap()
	isTrusted := func(address netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(address) {
				return true
			}
		}
		return false
	}
	if !isTrusted(address) || r.Header.Get("X-Forwarded-For") == "" {
		return address.String(), nil
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(chain) > 16 {
		return "", ErrUnauthorized
	}
	for i := len(chain) - 1; i >= 0 && isTrusted(address); i-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return "", ErrUnauthorized
		}
		address = candidate.Unmap()
	}
	return address.String(), nil
}
