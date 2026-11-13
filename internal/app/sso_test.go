package app

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
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

func TestPublicSSOConfirmationWaitsForAdmittedSecurityOperation(t *testing.T) {
	ready := make(chan *websocket.Conn, 1)
	admitted, release := make(chan struct{}), make(chan struct{})
	var reads, logins atomic.Int32
	writes := make(chan string, 1)
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			io.WriteString(w, `{"UsersMessage":{"myUserId":"1"},"ComputersMessage":{"computers":[{"peerId":"7","userId":"1","properties":{"ME":"true"}}]}}`)
		case "/v1/LoginConfiguration":
			io.WriteString(w, `{"loginType":"cloud_sso"}`)
		case "/v0/SsoAuthInitiateRequest":
			io.WriteString(w, `{"ssoLoginUrl":"https://fixture.invalid/"}`)
		case "/v0/wsevent":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			ready <- conn
			defer conn.CloseNow()
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		case "/v0/LoginRequest":
			logins.Add(1)
			io.WriteString(w, `{"httpToken":"B"}`)
		case "/v1/DataKey":
			if reads.Add(1) == 1 {
				close(admitted)
				<-release
				io.WriteString(w, `{"securityKeyType":"AccountPassword"}`)
			} else {
				io.WriteString(w, `{"securityKeyType":"PrivatePassword"}`)
			}
		case "/v0/SecurityKeyTypeRequest":
			writes <- r.Header.Get("Authorization")
			io.WriteString(w, `{"errors":[]}`)
		default:
			t.Error("unexpected native route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _ := engine.NewClient(native.URL, native.Client(), time.Second)
	owner := lifecycle.New(lifecycle.Options{Native: client, MonitorInterval: time.Hour})
	owner.Begin(ctx, 1)
	defer owner.End(1)
	client.SetToken("A")
	s := New(client, nil, Options{Context: ctx, Lifecycle: owner})
	h := s.UnixHandler()
	start := request(h, "POST", "/api/v1/account/sso", `{"username":"alias","serverAddress":"fixture.invalid"}`, nil, "", "")
	if start.Code != 202 {
		t.Fatal("start failed", start.Code, start.Body.String())
	}
	var begin struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(start.Body.Bytes(), &begin)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- request(h, "POST", "/api/v1/security/key-type", `{"type":"PrivatePassword","confirm":"UPGRADE TO PrivatePassword","password":"fixture-password"}`, nil, "", "")
	}()
	select {
	case <-admitted:
	case <-ctx.Done():
		t.Fatal("security preflight not entered")
	}
	conn := <-ready
	wsjson.Write(ctx, conn, map[string]any{"SsoAuthFinalizeMessage": map[string]string{"username": "canonical", "authToken": "assertion"}})
	var state struct {
		Data engine.SSOState `json:"data"`
	}
	for {
		response := request(h, "GET", "/api/v1/account/sso/"+begin.Data.ID, "", nil, "", "")
		json.Unmarshal(response.Body.Bytes(), &state)
		if state.Data.State == "confirm" {
			break
		}
		select {
		case <-ctx.Done():
			close(release)
			t.Fatal("no confirmation state")
		case <-time.After(time.Millisecond):
		}
	}
	if logins.Load() != 0 {
		close(release)
		t.Fatal("callback installed session outside owner")
	}
	confirmed := make(chan *httptest.ResponseRecorder, 1)
	body, _ := json.Marshal(map[string]string{"assertionId": state.Data.AssertionID, "username": state.Data.Username})
	go func() {
		confirmed <- request(h, "POST", "/api/v1/account/sso/"+begin.Data.ID+"/confirm", string(body), nil, "", "")
	}()
	close(release)
	if w := <-done; w.Code != 200 {
		t.Fatal("security failed", w.Code, w.Body.String())
	}
	if got := <-writes; got != "token A" {
		t.Fatal("security crossed session")
	}
	if w := <-confirmed; w.Code != 200 {
		t.Fatal("confirmation failed", w.Code, w.Body.String())
	}
	if logins.Load() != 1 {
		t.Fatal("native login count", logins.Load())
	}
	if w := request(h, "POST", "/api/v1/account/sso/"+begin.Data.ID+"/confirm", string(body), nil, "", ""); w.Code == 200 {
		t.Fatal("confirmation replay accepted")
	}
	if strings.Contains(start.Body.String(), "assertion") {
		t.Fatal("token escaped")
	}
}

func TestPublicConfigurationWaitsForTheOwner(t *testing.T) {
	seen := make(chan struct{}, 1)
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- struct{}{}
		io.WriteString(w, `{"loginType":"local"}`)
	}))
	defer native.Close()
	client, _ := engine.NewClient(native.URL, native.Client(), time.Second)
	owner := lifecycle.New(lifecycle.Options{Native: client, MonitorInterval: time.Hour})
	owner.Begin(context.Background(), 1)
	defer owner.End(1)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- owner.Do(context.Background(), func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	h := New(client, nil, Options{Lifecycle: owner}).UnixHandler()
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- request(h, "POST", "/api/v1/account/configuration", `{"username":"fixture","serverAddress":"fixture.invalid"}`, nil, "", "")
	}()
	select {
	case <-seen:
		close(release)
		<-finished
		t.Fatal("authority discovery bypassed the owner")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-finished
	if response := <-result; response.Code != 200 {
		t.Fatal("queued discovery failed", response.Code)
	}
}
