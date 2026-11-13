package auth

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestForwardedPeerIsUsedOnlyBehindExplicitTrustedProxy(t *testing.T) {
	r := httptest.NewRequest("POST", "http://backup.fixture.invalid/api/v1/session", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.20")
	peer, err := ClientIP(r, nil)
	if err != nil || peer != "192.0.2.10" {
		t.Fatal("untrusted header selected", err)
	}
	peer, err = ClientIP(r, []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")})
	if err != nil || peer != "198.51.100.20" {
		t.Fatal("approved proxy not supported", err)
	}
	r.Header.Set("X-Forwarded-For", "198.51.100.99, 198.51.100.20")
	peer, err = ClientIP(r, []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")})
	if err != nil || peer != "198.51.100.20" {
		t.Fatal("client spoofed a proxy chain", err)
	}
}
