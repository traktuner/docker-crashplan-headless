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

// Audit M5: the header is only a throttle key. Proxy formats are understood,
// every header line counts, and nothing malformed rejects a sign-in.
func TestForwardedForFormsNeverRejectAndCannotChooseTheBucket(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.20.0.0/16")}
	cases := []struct {
		name   string
		values []string
		want   string
	}{
		{"client spoof then proxy append", []string{"198.51.100.99, 203.0.113.7"}, "203.0.113.7"},
		{"two header lines, proxy adds its own", []string{"198.51.100.99", "203.0.113.7"}, "203.0.113.7"},
		{"ip:port from proxy", []string{"203.0.113.7:51234"}, "203.0.113.7"},
		{"bracketed v6 with port", []string{"[2001:db8::1]:51234"}, "2001:db8::1"},
		{"bracketed v6", []string{"[2001:db8::1]"}, "2001:db8::1"},
		{"v6 zone is dropped", []string{"fe80::1%eth0"}, "fe80::1"},
		{"mapped v4", []string{"::ffff:203.0.113.7"}, "203.0.113.7"},
		{"trailing comma", []string{"203.0.113.7,"}, "203.0.113.7"},
		{"client garbage before proxy entry", []string{"garbage, 203.0.113.7"}, "203.0.113.7"},
		{"unknown token keeps the proxy", []string{"unknown"}, "172.20.0.2"},
		{"empty header keeps the proxy", []string{""}, "172.20.0.2"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "http://backup.fixture.invalid/api/v1/session", nil)
		r.RemoteAddr = "172.20.0.2:40000"
		for _, v := range c.values {
			r.Header.Add("X-Forwarded-For", v)
		}
		got, err := ClientIP(r, trusted)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestForwardedForStopsAfterSixteenTrustedHops(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.20.0.0/16")}
	r := httptest.NewRequest("POST", "http://backup.fixture.invalid/api/v1/session", nil)
	r.RemoteAddr = "172.20.0.2:40000"
	chain := "203.0.113.7"
	for i := 0; i < 20; i++ {
		chain += ", 172.20.1.1"
	}
	r.Header.Set("X-Forwarded-For", chain)
	got, err := ClientIP(r, trusted)
	if err != nil || got != "172.20.1.1" {
		t.Fatalf("overlong chain: got %q, %v", got, err)
	}
}
