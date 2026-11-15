package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/traktuner/crashplan-headless/internal/auth"
)

func TestAuthNoneOpensTheInterfaceWithoutSignIn(t *testing.T) {
	open, err := auth.NewOpen([]string{"https://backup.fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := newFixture(t)
	server.sessions = open
	h := server.WebHandler()

	read := httptest.NewRecorder()
	h.ServeHTTP(read, httptest.NewRequest("GET", "https://backup.fixture.invalid/api/v1/session", nil))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"mode":"none"`) || !strings.Contains(read.Body.String(), `"csrf":"`) {
		t.Fatal("session read without cookie refused", read.Code, read.Body.String())
	}

	login := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "https://backup.fixture.invalid/api/v1/session", strings.NewReader(`{"username":"a","password":"b"}`))
	r.Header.Set("Origin", "https://backup.fixture.invalid")
	h.ServeHTTP(login, r)
	if login.Code == http.StatusOK || len(login.Result().Cookies()) != 0 {
		t.Fatal("password sign-in is mounted although auth is off", login.Code)
	}

	foreign := httptest.NewRecorder()
	h.ServeHTTP(foreign, httptest.NewRequest("GET", "https://evil.invalid/api/v1/session", nil))
	if foreign.Code == http.StatusOK {
		t.Fatal("foreign host reached the API")
	}

	// A mutation goes through the whole handler: it passes the web checks with
	// the page's own origin and token, and another origin is refused.
	var csrf struct {
		Data struct {
			CSRF string `json:"csrf"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &csrf); err != nil || csrf.Data.CSRF == "" {
		t.Fatal("no token in the session answer", err, read.Body.String())
	}
	refused := func(w *httptest.ResponseRecorder) bool {
		body := w.Body.String()
		return strings.Contains(body, "CSRF_REJECTED") || strings.Contains(body, "WEB_AUTHORIZATION_REQUIRED") || strings.Contains(body, "PUBLIC_ORIGIN_MISMATCH")
	}
	if own := request(h, "POST", "/api/v1/backup/pause", "{}", nil, csrf.Data.CSRF, "https://backup.fixture.invalid"); refused(own) {
		t.Fatal("own-origin mutation refused by the web checks", own.Code, own.Body.String())
	}
	if other := request(h, "POST", "/api/v1/backup/pause", "{}", nil, csrf.Data.CSRF, "https://evil.invalid"); !refused(other) {
		t.Fatal("cross-origin mutation passed the web checks", other.Code, other.Body.String())
	}
	if noToken := request(h, "POST", "/api/v1/backup/pause", "{}", nil, "", "https://backup.fixture.invalid"); !refused(noToken) {
		t.Fatal("mutation without token passed the web checks", noToken.Code)
	}

	// Signing out must not claim success when there is no sign-in.
	out := request(h, "DELETE", "/api/v1/session", "", nil, csrf.Data.CSRF, "https://backup.fixture.invalid")
	if !strings.Contains(out.Body.String(), `"signedOut":false`) {
		t.Fatal("sign-out claimed success without a sign-in", out.Body.String())
	}
}
