package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebSessionIsIndependentAndCookieIsProtected(t *testing.T) {
	m, err := New("operator", "fixture-password-12345", "https://backup.fixture.invalid", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	session, err := m.Login("operator", "fixture-password-12345")
	if err != nil {
		t.Fatal(err)
	}
	cookie := m.Cookie(session)
	if !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unprotected session cookie: %+v", cookie)
	}
	r := httptest.NewRequest("POST", "https://backup.fixture.invalid/api/scan", nil)
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://backup.fixture.invalid")
	r.Header.Set("X-CSRF-Token", session.CSRF)
	principal, err := m.Authorize(r, true)
	if err != nil || principal.ID != session.ID {
		t.Fatal("own web session rejected", err)
	}
	r.Header.Set("Authorization", "token native-token-canary")
	r.Header.Del("Cookie")
	if _, err := m.Authorize(r, false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("native token accepted as local UI authentication")
	}
}

func TestUnrelatedClientFailuresDoNotLockOutAdmin(t *testing.T) {
	m, _ := NewOrigins("operator", "fixture-password-12345", []string{"https://backup.fixture.invalid", "https://alias.fixture.invalid"}, 30*time.Minute)
	for i := 0; i < 5; i++ {
		m.LoginFrom(context.Background(), "192.0.2.1", "operator", "wrong")
	}
	session, err := m.LoginFrom(context.Background(), "192.0.2.2", "operator", "fixture-password-12345")
	if err != nil {
		t.Fatal("unrelated failures locked out admin", err)
	}
	r := httptest.NewRequest("GET", "https://alias.fixture.invalid/api/v1/state", nil)
	r.AddCookie(m.Cookie(session))
	r.Header.Set("X-Forwarded-Host", "attacker.invalid")
	if _, err := m.Authorize(r, false); err != nil {
		t.Fatal("approved alias not usable", err)
	}
}

func TestSessionIdleLimitSlidesButAbsoluteLimitDoesNot(t *testing.T) {
	m, _ := New("operator", "fixture-password-12345", "https://backup.fixture.invalid", 30*time.Minute)
	now := time.Now()
	m.now = func() time.Time { return now }
	s, err := m.Login("operator", "fixture-password-12345")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://backup.fixture.invalid/api/v1/state", nil)
	r.AddCookie(m.Cookie(s))
	for i := 0; i < 35; i++ {
		now = now.Add(20 * time.Minute)
		if _, err := m.Authorize(r, false); err != nil {
			t.Fatal("active session expired early", err)
		}
	}
	now = now.Add(20 * time.Minute)
	if _, err := m.Authorize(r, false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("absolute 12h limit not enforced")
	}
}

func TestCrossOriginAndMissingCSRFAreRejected(t *testing.T) {
	m, _ := New("operator", "fixture-password-12345", "https://backup.fixture.invalid", time.Hour)
	session, _ := m.Login("operator", "fixture-password-12345")
	for _, origin := range []string{"", "https://attacker.invalid", "https://backup.fixture.invalid.attacker.invalid"} {
		r := httptest.NewRequest("POST", "https://backup.fixture.invalid/api/scan", nil)
		r.AddCookie(m.Cookie(session))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", session.CSRF)
		if _, err := m.Authorize(r, true); !errors.Is(err, ErrCSRF) {
			t.Errorf("origin %q: %v", origin, err)
		}
	}
	r := httptest.NewRequest("POST", "https://backup.fixture.invalid/api/scan", nil)
	r.AddCookie(m.Cookie(session))
	r.Header.Set("Origin", "https://backup.fixture.invalid")
	if _, err := m.Authorize(r, true); !errors.Is(err, ErrCSRF) {
		t.Fatal("missing CSRF accepted", err)
	}
}

func TestDNSRebindingHostAndExpiredOrLoggedOutSessionsFail(t *testing.T) {
	m, _ := New("operator", "fixture-password-12345", "https://backup.fixture.invalid", time.Hour)
	session, _ := m.Login("operator", "fixture-password-12345")
	r := httptest.NewRequest("GET", "https://attacker.invalid/api/status", nil)
	r.AddCookie(m.Cookie(session))
	if _, err := m.Authorize(r, false); !errors.Is(err, ErrOrigin) {
		t.Fatal("unapproved host accepted")
	}
	r = httptest.NewRequest("GET", "https://backup.fixture.invalid/api/status", nil)
	r.AddCookie(m.Cookie(session))
	m.Logout(session.ID)
	if _, err := m.Authorize(r, false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("logged-out session still works")
	}
	expiring, _ := m.Login("operator", "fixture-password-12345")
	r = httptest.NewRequest("GET", "https://backup.fixture.invalid/api/status", nil)
	r.AddCookie(m.Cookie(expiring))
	m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := m.Authorize(r, false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired session accepted")
	}
}

func TestWrongPasswordAndRepeatedFailuresStayUnauthorized(t *testing.T) {
	m, _ := New("operator", "fixture-password-12345", "https://backup.fixture.invalid", time.Hour)
	for i := 0; i < 5; i++ {
		if _, err := m.Login("operator", "wrong-password"); !errors.Is(err, ErrUnauthorized) {
			t.Fatal(err)
		}
	}
	if _, err := m.Login("operator", "fixture-password-12345"); err != nil {
		t.Fatal("earlier failures locked out a valid login", err)
	}
}

func TestPassiveEventStreamDoesNotRenewIdleSession(t *testing.T) {
	m, _ := New("operator", "fixture-password-12345", "https://backup.fixture.invalid", 30*time.Minute)
	now := time.Now()
	m.now = func() time.Time { return now }
	session, _ := m.Login("operator", "fixture-password-12345")
	r := httptest.NewRequest("GET", "https://backup.fixture.invalid/api/v1/events", nil)
	r.AddCookie(m.Cookie(session))
	now = now.Add(20 * time.Minute)
	if _, err := m.AuthorizePassive(r); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Minute)
	if _, err := m.AuthorizePassive(r); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("SSE heartbeat extended idle login")
	}
}

func TestCookieUsesTheSelectedApprovedOrigin(t *testing.T) {
	m, _ := NewOrigins("operator", "fixture-password-12345", []string{"https://backup.fixture.invalid", "http://backup.fixture.invalid"}, time.Hour)
	s, _ := m.Login("operator", "fixture-password-12345")
	r := httptest.NewRequest("POST", "http://backup.fixture.invalid/api/v1/session", nil)
	r.Header.Set("Origin", "http://backup.fixture.invalid")
	if m.CookieFor(s, r).Secure {
		t.Fatal("explicit HTTP origin cannot use its login cookie")
	}
	r.Header.Set("Origin", "https://backup.fixture.invalid")
	if !m.CookieFor(s, r).Secure {
		t.Fatal("HTTPS cookie not secure")
	}
}

func TestRejectsWeakPasswordAndNonCanonicalOrigins(t *testing.T) {
	for _, origin := range []string{"https://backup.fixture.invalid/path", "https://user:password@backup.fixture.invalid", "https://backup.fixture.invalid?token=x", "javascript:alert(1)", "https://backup.fixture.invalid#x"} {
		if _, err := New("operator", "fixture-password-12345", origin, time.Hour); err == nil {
			t.Errorf("unsafe origin accepted: %s", origin)
		}
	}
	if _, err := New("operator", "short", "https://backup.fixture.invalid", time.Hour); err == nil {
		t.Fatal("weak web password accepted")
	}
}
