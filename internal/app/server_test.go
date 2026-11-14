package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
	"github.com/traktuner/crashplan-headless/internal/statefile"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
)

type fixtureGate struct {
	checks int
	fail   bool
}

func (g *fixtureGate) Check() error {
	g.checks++
	if g.fail {
		return errors.New("source unavailable")
	}
	return nil
}

type fixtureCalls struct {
	atomic.Int32
	scans atomic.Int32
}

func newFixture(t *testing.T) (*Server, *fixtureCalls, *bytes.Buffer) {
	t.Helper()
	calls := &fixtureCalls{}
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/v0/LoginRequest":
			io.WriteString(w, `{"errors":[],"httpToken":"native-private-canary"}`)
		case "/v0/Command":
			calls.scans.Add(1)
			if r.Header.Get("Authorization") != "token native-private-canary" {
				t.Error("native authorization missing")
			}
			io.WriteString(w, `{"errors":[],"results":[{"command":{"name":"backup.scan"}}]}`)
		case "/v1/BackupSet/9007199254740993":
			io.WriteString(w, `{"id":"9007199254740993"}`)
		case "/v0/GetComputers":
			io.WriteString(w, `{"UsersMessage":{"myUserId":"11"},"ComputersMessage":{"computers":[{"peerId":"21","userId":"11","properties":{"ME":"true"}}]}}`)
		case "/v0/GetScanStats":
			io.WriteString(w, `{"backupSetId":9007199254740993,"scanning":false,"lastCompletedScanDate":"100","unamendedLastCompletedScanDate":"100","scanDurationInMs":"0"}`)
		case "/v1/ApplicationStatus":
			io.WriteString(w, `{"productVersion":"12.1.1","buildNumber":"2"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(native.Close)
	client, err := engine.NewClient(native.URL, native.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := auth.New("operator", "fixture-web-password-12345", "https://backup.fixture.invalid", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	scans, err := engine.NewScans(client, store)
	if err != nil {
		t.Fatal(err)
	}
	owner := lifecycle.New(lifecycle.Options{Native: client, Scans: scans, Logger: logger, MonitorInterval: time.Hour})
	owner.Begin(context.Background(), 1)
	t.Cleanup(func() { owner.End(1) })
	server := New(client, sessions, Options{Lifecycle: owner, Logger: logger, Sources: &fixtureGate{}, Scans: scans})
	return server, calls, logs
}

func request(h http.Handler, method, path, body string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://backup.fixture.invalid"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
		// Ordinary fixtures submit against a freshly read native session.
		// Stale-page tests send their retained header explicitly.
		state := httptest.NewRequest("GET", "https://backup.fixture.invalid/api/v1/session/status", nil)
		if cookie != nil {
			state.AddCookie(cookie)
		}
		view := httptest.NewRecorder()
		h.ServeHTTP(view, state)
		r.Header.Set(nativeContextHeader, view.Header().Get(nativeContextHeader))
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	w := request(h, "POST", "/api/v1/session", `{"username":"operator","password":"fixture-web-password-12345"}`, nil, "", "https://backup.fixture.invalid")
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatal("web login failed", w.Code)
	}
	var body struct {
		Data struct {
			CSRF string `json:"csrf"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Data.CSRF == "" {
		t.Fatal("missing web CSRF token")
	}
	return w.Result().Cookies()[0], body.Data.CSRF
}

func TestWebCannotReachNativeEngineWithoutOwnSession(t *testing.T) {
	server, calls, _ := newFixture(t)
	w := request(server.WebHandler(), "POST", "/api/v1/backup-sets/9007199254740993/scan", `{}`, nil, "", "https://backup.fixture.invalid")
	if w.Code != 401 || calls.Load() != 0 {
		t.Fatal("unauthorized web request reached native engine")
	}
}

func TestWebLoginNativeLoginAndScanDoNotExposeSecrets(t *testing.T) {
	server, calls, logs := newFixture(t)
	h := server.WebHandler()
	cookie, csrf := login(t, h)
	w := request(h, "POST", "/api/v1/account/login", `{"username":"fixture-user","password":"account-private-canary","serverAddress":"console.fixture.invalid"}`, cookie, csrf, "https://backup.fixture.invalid")
	if w.Code != 200 {
		t.Fatal("native login not routed", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "native-private-canary") {
		t.Fatal("native token sent to browser")
	}
	w = request(h, "POST", "/api/v1/backup-sets/9007199254740993/scan", `{}`, cookie, csrf, "https://backup.fixture.invalid")
	if w.Code != 202 || calls.scans.Load() != 1 {
		t.Fatal("native scan was not accepted exactly once", w.Code, calls.scans.Load())
	}
	if strings.Contains(logs.String(), "canary") || strings.Contains(logs.String(), "fixture-web-password") {
		t.Fatal("private data in application logs")
	}
}

func TestCSRFAndCallerSuppliedAuthorizationCannotBypassLocalSession(t *testing.T) {
	server, calls, _ := newFixture(t)
	h := server.WebHandler()
	cookie, csrf := login(t, h)
	w := request(h, "POST", "/api/v1/backup-sets/9007199254740993/scan", `{}`, cookie, csrf, "https://attacker.invalid")
	if w.Code != 403 || calls.Load() != 0 {
		t.Fatal("cross-origin request reached engine")
	}
	r := httptest.NewRequest("POST", "https://backup.fixture.invalid/api/v1/backup-sets/7/scan", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "token native-private-canary")
	r.Header.Set("X-CPH-Local", "1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || calls.Load() != 0 {
		t.Fatal("spoofed Unix authorization accepted")
	}
}

func TestUnixControlIsSeparateFromWebAuthorization(t *testing.T) {
	server, calls, _ := newFixture(t)
	r := httptest.NewRequest("GET", "http://unix/api/v1/state", nil)
	w := httptest.NewRecorder()
	server.UnixHandler().ServeHTTP(w, r)
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatal("private Unix control failed", w.Code)
	}
}

func TestScanSourceFailurePreventsDispatch(t *testing.T) {
	server, calls, _ := newFixture(t)
	server.native.SetToken("native-private-canary")
	server.options.Sources = &fixtureGate{fail: true}
	r := httptest.NewRequest("POST", "http://unix/api/v1/backup-sets/7/scan", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	server.UnixHandler().ServeHTTP(w, r)
	if w.Code < 400 || calls.Load() != 0 {
		t.Fatal("scan ran despite missing source")
	}
}

func TestInvalidScanDoesNotProbeSourceRoots(t *testing.T) {
	server, calls, _ := newFixture(t)
	server.native.SetToken("native-private-canary")
	gate := &fixtureGate{}
	server.options.Sources = gate
	r := httptest.NewRequest("POST", "http://unix/api/v1/backup-sets/not-an-id/scan", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	server.UnixHandler().ServeHTTP(w, r)
	if w.Code != 400 || gate.checks != 0 || calls.Load() != 0 {
		t.Fatal("invalid scan touched source roots", w.Code, gate.checks, calls.Load())
	}
}

func TestGenericNativeProxyIsNotAPublicProductRoute(t *testing.T) {
	server, calls, _ := newFixture(t)
	w := request(server.UnixHandler(), "POST", "/api/v1/engine/security.key.create", `{"body":{}}`, nil, "", "")
	if w.Code != 404 || calls.Load() != 0 {
		t.Fatal("generic native mutation is exposed", w.Code)
	}
}

// A browser event stream must outlive the server's ReadTimeout; each reconnect
// refreshes every status query and makes the interface visibly reload.
func TestEventStreamOutlivesReadTimeout(t *testing.T) {
	server, _, _ := newFixture(t)
	cookie, _ := login(t, server.WebHandler())
	web := httptest.NewUnstartedServer(server.WebHandler())
	web.Config.ReadTimeout = 300 * time.Millisecond
	web.StartTLS()
	defer web.Close()
	req, _ := http.NewRequest("GET", web.URL+"/api/v1/events", nil)
	req.Host = "backup.fixture.invalid"
	req.AddCookie(cookie)
	response, err := web.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("event stream refused", response.StatusCode)
	}
	first := make([]byte, 64)
	if _, err := response.Body.Read(first); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		_, err := response.Body.Read(make([]byte, 64))
		ended <- err
	}()
	select {
	case err := <-ended:
		t.Fatal("event stream ended after the read timeout", err)
	case <-time.After(time.Second):
	}
}
