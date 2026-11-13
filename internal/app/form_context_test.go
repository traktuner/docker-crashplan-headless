package app

import (
	"context"
	"encoding/json"
	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStaleSecurityFormAfterCompletedNativeLogin(t *testing.T) {
	var changed atomic.Bool
	var applied atomic.Int32
	n := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			if r.Header.Get("Authorization") == "token B" {
				io.WriteString(w, `{"UsersMessage":{"myUserId":"2"},"ComputersMessage":{"computers":[{"peerId":"9","userId":"2","properties":{"ME":"true"}}]}}`)
			} else {
				io.WriteString(w, `{"UsersMessage":{"myUserId":"1"},"ComputersMessage":{"computers":[{"peerId":"7","userId":"1","properties":{"ME":"true"}}]}}`)
			}
		case "/v1/DataKey":
			if changed.Load() {
				io.WriteString(w, `{"securityKeyType":"PrivatePassword"}`)
			} else {
				io.WriteString(w, `{"securityKeyType":"AccountPassword"}`)
			}
		case "/v0/LoginRequest":
			io.WriteString(w, `{"httpToken":"B"}`)
		case "/v0/SecurityKeyTypeRequest":
			if r.Header.Get("Authorization") == "token B" {
				applied.Add(1)
			}
			changed.Store(true)
			io.WriteString(w, `{"errors":[]}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer n.Close()
	c, err := engine.NewClient(n.URL, n.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner := lifecycle.New(lifecycle.Options{Native: c, MonitorInterval: time.Hour})
	if err := owner.Begin(ctx, 1); err != nil {
		t.Fatal(err)
	}
	defer owner.End(1)
	c.SetToken("A")
	sessions, err := auth.New("operator", "fixture-web-password-12345", "https://backup.fixture.invalid", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := New(c, sessions, Options{Context: ctx, Lifecycle: owner})
	h := s.WebHandler()
	cookie, csrf := login(t, h)
	// Tab one reads account A's protection and prepares this account-level form.
	read := request(h, "GET", "/api/v1/security", "", cookie, "", "")
	if read.Code != 200 {
		t.Fatal("read failed", read.Code)
	}
	var viewed struct {
		Data struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &viewed); err != nil {
		t.Fatal(err)
	}
	if read.Header().Get(nativeContextHeader) == "" {
		t.Fatal("missing page context")
	}
	form := `{"type":"PrivatePassword","confirm":"UPGRADE TO PrivatePassword","password":"fixture-new-archive-password"}`
	before := c.SessionRevision()
	// Tab two completes an ordinary native account login on the same web session.
	logged := request(h, "POST", "/api/v1/account/login", `{"username":"B","password":"fixture-password","serverAddress":"fixture.invalid"}`, cookie, csrf, "https://backup.fixture.invalid")
	if logged.Code != 200 || c.SessionRevision() == before {
		t.Fatal("native login setup failed", logged.Code, logged.Body.String())
	}
	rr := httptest.NewRequest("POST", "https://backup.fixture.invalid/api/v1/security/key-type", strings.NewReader(form))
	rr.AddCookie(cookie)
	rr.Header.Set("Content-Type", "application/json")
	rr.Header.Set("Origin", "https://backup.fixture.invalid")
	rr.Header.Set("X-CSRF-Token", csrf)
	rr.Header.Set("X-CPH-Engine-Context", read.Header().Get("X-CPH-Engine-Context"))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, rr)
	t.Logf("viewed=%s nativeRevisionChanged=%v staleFormHTTP=%d appliedToB=%d", viewed.Data.Type, c.SessionRevision() != before, response.Code, applied.Load())
	if response.Code != http.StatusConflict || applied.Load() != 0 {
		t.Fatal("stale account-A form changed account B")
	}
	current := request(h, "POST", "/api/v1/security/key-type", form, cookie, csrf, "https://backup.fixture.invalid")
	if current.Code != http.StatusOK || applied.Load() != 1 {
		t.Fatal("current account form rejected", current.Code, current.Body.String())
	}
}

func TestWebResponseRetainsRequestContext(t *testing.T) {
	s, _, _ := newFixture(t)
	before := s.nativeContext(s.native.SessionRevision())
	h := s.webContext(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.native.SetToken("different-session")
		writeData(w, 200, "old-account-result")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://backup.fixture.invalid/api/v1/state", nil))
	if w.Header().Get(nativeContextHeader) != before {
		t.Fatal("old response labeled with the new account context")
	}
}

func TestQueuedWebMutationKeepsItsDisplayedSession(t *testing.T) {
	s, _, _ := newFixture(t)
	view := context.WithValue(context.Background(), webRevisionKey{}, s.native.SessionRevision())
	called := false
	queued := s.bindWebMutation(view, func(context.Context) error { called = true; return nil })
	s.native.SetToken("different-session")
	if err := queued(context.Background()); err == nil || called {
		t.Fatal("queued old-account form reached a new session")
	}
}
