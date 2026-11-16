package app

import (
	"testing"
	"time"
)

// The web sign-in route takes the "keep me signed in" choice; nothing else does.
func TestWebSignInHonoursRememberAndOnlyThere(t *testing.T) {
	server, _, _ := newFixture(t)
	h := server.WebHandler()
	const origin = "https://backup.fixture.invalid"
	plain := request(h, "POST", "/api/v1/session", `{"username":"operator","password":"fixture-web-password-12345"}`, nil, "", origin)
	kept := request(h, "POST", "/api/v1/session", `{"username":"operator","password":"fixture-web-password-12345","remember":true}`, nil, "", origin)
	if plain.Code != 200 || kept.Code != 200 || len(plain.Result().Cookies()) != 1 || len(kept.Result().Cookies()) != 1 {
		t.Fatal("web sign-in failed", plain.Code, kept.Code)
	}
	if c := plain.Result().Cookies()[0]; !c.Expires.IsZero() {
		t.Fatalf("a normal sign-in must give a browser-session cookie, got Expires %v", c.Expires)
	}
	if c := kept.Result().Cookies()[0]; time.Until(c.Expires) < 80*24*time.Hour {
		t.Fatalf("a kept sign-in must give a long-lived cookie, got Expires %v", c.Expires)
	}

	cookie, csrf := login(t, h)
	w := request(h, "POST", "/api/v1/account/login", `{"username":"someone@example.invalid","password":"x","serverAddress":"console.example.invalid","remember":true}`, cookie, csrf, origin)
	if w.Code < 400 || w.Code >= 500 {
		t.Fatalf("the CrashPlan account sign-in must not accept a remember field, got %d", w.Code)
	}
}
