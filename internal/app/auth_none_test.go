package app

import (
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
}
