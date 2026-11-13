package engine

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestR4SSOExplicitOrganizationKeepsDiscoveredContext(t *testing.T) {
	got := make(chan map[string]any, 1)
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/wsevent":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			for {
				if _, _, err = conn.Read(r.Context()); err != nil {
					return
				}
			}
		case "/v1/LoginConfiguration":
			io.WriteString(w, `{"loginType":"cloud_sso","regKey":"discovered-client-key"}`)
		case "/v0/SsoAuthInitiateRequest":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			got <- body
			io.WriteString(w, `{"ssoLoginUrl":"https://login.fixture.invalid/"}`)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	attempt, err := c.BeginSSO(ctx, LoginInfo{Username: "alice", ServerAddress: "fixture.invalid", Register: true, RegistrationKey: "owner-org-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	body := <-got
	if body["regKey"] != "discovered-client-key" || body["orgRegKey"] != "owner-org-key" {
		t.Errorf("SSO context mismatch: discovered_key_used=%t organization_preserved=%t", body["regKey"] == "discovered-client-key", body["orgRegKey"] == "owner-org-key")
	}
}
func TestR4CancelledSSOCallbackCannotCompleteAnotherAttempt(t *testing.T) {
	ready := make(chan *websocket.Conn, 1)
	login := make(chan string, 1)
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/LoginConfiguration":
			io.WriteString(w, `{"loginType":"cloud_sso"}`)
		case "/v0/wsevent":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			ready <- conn
			defer conn.CloseNow()
			for {
				if _, _, err = conn.Read(r.Context()); err != nil {
					return
				}
			}
		case "/v0/SsoAuthInitiateRequest":
			io.WriteString(w, `{"ssoLoginUrl":"https://login.fixture.invalid/"}`)
		case "/v0/LoginRequest":
			var body Credentials
			json.NewDecoder(r.Body).Decode(&body)
			login <- body.Username
			io.WriteString(w, `{"httpToken":"scratch-token"}`)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := c.BeginSSO(ctx, LoginInfo{Username: "alice", ServerAddress: "fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	conn := <-ready
	first.Close()
	second, err := c.BeginSSO(ctx, LoginInfo{Username: "bob", ServerAddress: "fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	changed := make(chan struct{}, 8)
	go second.Watch(ctx, func() { changed <- struct{}{} })
	if err := wsjson.Write(ctx, conn, map[string]any{"SsoAuthFinalizeMessage": map[string]string{"username": "alice", "authToken": "old-alice-callback"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-ctx.Done():
		t.Fatal("assertion not presented")
	}
	state := second.Status()
	if state.State != "confirm" || state.Username != "alice" || !state.Differs {
		t.Fatal("unexpected identity hidden", state)
	}
	select {
	case <-login:
		t.Fatal("unconfirmed stale callback dispatched")
	default:
	}
	if err := second.Discard(state.AssertionID); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"SsoAuthFinalizeMessage": map[string]string{"username": "bob", "authToken": "new-bob-callback"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-ctx.Done():
		t.Fatal("fresh assertion not presented")
	}
	state = second.Status()
	if result, err := second.Complete(ctx, state.AssertionID, "bob"); err != nil || !result.Complete {
		t.Fatal("valid replacement SSO refused", result, err)
	}
	if got := <-login; got != "bob" {
		t.Fatal("wrong identity confirmed")
	}
}
