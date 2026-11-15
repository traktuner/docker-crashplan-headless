package auth

import (
	"errors"
	"net/http/httptest"
	"testing"
)

func openFixture(t *testing.T) *Open {
	t.Helper()
	o, err := NewOpen([]string{"https://backup.fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOpenAdmitsReadsOnlyOnAnAllowedHost(t *testing.T) {
	o := openFixture(t)
	if session, err := o.Authorize(httptest.NewRequest("GET", "https://backup.fixture.invalid/api/v1/session", nil), false); err != nil || session.CSRF == "" || session.Mode != ModeNone {
		t.Fatal("read from the public origin refused or without token", err)
	}
	if _, err := o.Authorize(httptest.NewRequest("GET", "https://evil.invalid/api/v1/session", nil), false); !errors.Is(err, ErrOrigin) {
		t.Fatal("foreign host accepted", err)
	}
}

func TestOpenStillProtectsMutationsFromOtherSites(t *testing.T) {
	o := openFixture(t)
	good, err := o.Authorize(httptest.NewRequest("GET", "https://backup.fixture.invalid/", nil), false)
	if err != nil {
		t.Fatal(err)
	}
	mutation := func(origin, token string) error {
		r := httptest.NewRequest("POST", "https://backup.fixture.invalid/api/v1/x", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if token != "" {
			r.Header.Set("X-CSRF-Token", token)
		}
		_, err := o.Authorize(r, true)
		return err
	}
	if err := mutation("https://backup.fixture.invalid", good.CSRF); err != nil {
		t.Fatal("legitimate mutation refused", err)
	}
	for name, err := range map[string]error{
		"no origin":    mutation("", good.CSRF),
		"other origin": mutation("https://evil.invalid", good.CSRF),
		"no token":     mutation("https://backup.fixture.invalid", ""),
		"wrong token":  mutation("https://backup.fixture.invalid", "x"+good.CSRF),
	} {
		if !errors.Is(err, ErrCSRF) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
}

func TestOpenRejectsBadOrigins(t *testing.T) {
	if _, err := NewOpen(nil); err == nil {
		t.Fatal("no origin accepted")
	}
	if _, err := NewOpen([]string{"not an origin"}); err == nil {
		t.Fatal("invalid origin accepted")
	}
}

// Both implementations satisfy the interface; only the password one signs in.
var (
	_ Authenticator = (*Open)(nil)
	_ Authenticator = (*Manager)(nil)
	_ PasswordLogin = (*Manager)(nil)
)

func TestOpenRefusesCrossSiteReadsLikeTheCookieDid(t *testing.T) {
	o := openFixture(t)
	read := func(fetchSite, origin string) error {
		r := httptest.NewRequest("GET", "https://backup.fixture.invalid/api/v1/events", nil)
		if fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		_, err := o.Authorize(r, false)
		return err
	}
	for _, ok := range []string{"", "same-origin", "none"} {
		if err := read(ok, ""); err != nil {
			t.Fatalf("Sec-Fetch-Site %q refused: %v", ok, err)
		}
	}
	if err := read("same-origin", "https://backup.fixture.invalid"); err != nil {
		t.Fatal("same-origin read with its own Origin refused", err)
	}
	for _, bad := range []string{"cross-site", "same-site"} {
		if err := read(bad, ""); !errors.Is(err, ErrCSRF) {
			t.Fatalf("Sec-Fetch-Site %q accepted: %v", bad, err)
		}
	}
	if err := read("", "https://evil.invalid"); !errors.Is(err, ErrCSRF) {
		t.Fatal("read with a foreign Origin accepted", err)
	}
}
