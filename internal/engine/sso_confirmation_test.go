package engine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestSSORequiresConfirmedAuthorityIdentity(t *testing.T) {
	ready := make(chan *websocket.Conn, 1)
	logins := make(chan Credentials, 4)
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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
			var in Credentials
			json.NewDecoder(r.Body).Decode(&in)
			logins <- in
			io.WriteString(w, `{"httpToken":"native-token"}`)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, err := c.BeginSSO(ctx, LoginInfo{Username: "alias", ServerAddress: "fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := c.LoginConfiguration(ctx, LoginInfo{Username: "other", ServerAddress: "other.invalid"}); !errors.Is(err, ErrLoginInProgress) {
		t.Fatal("discovery changed authority during a live SSO attempt", err)
	}
	changed := make(chan struct{}, 8)
	go a.Watch(ctx, func() { changed <- struct{}{} })
	conn := <-ready
	wsjson.Write(ctx, conn, map[string]any{"SsoAuthFinalizeMessage": map[string]string{"username": "canonical", "authToken": "assertion"}})
	select {
	case <-changed:
	case <-ctx.Done():
		t.Fatal("no confirmation")
	}
	state := a.Status()
	if state.State != "confirm" || state.Username != "canonical" || !state.Differs || len(logins) != 0 {
		t.Fatal("callback dispatched without confirmation", state)
	}
	if _, err := a.Complete(ctx, state.AssertionID, "alias"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("typed alias substituted for confirmed identity", err)
	}
	result, err := a.Complete(ctx, state.AssertionID, "canonical")
	if err != nil || !result.Complete {
		t.Fatal(result, err)
	}
	if got := <-logins; got.Username != "canonical" || got.SSOToken != "assertion" {
		t.Fatal("confirmed identity changed")
	}
	if _, err := a.Complete(ctx, state.AssertionID, "canonical"); !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatal("confirmation replayed", err)
	}
}

func completeConfirmedSSO(t *testing.T, a *SSOAttempt, ctx context.Context) (LoginResult, error) {
	t.Helper()
	changed := make(chan struct{}, 8)
	go a.Watch(ctx, func() { changed <- struct{}{} })
	select {
	case <-changed:
	case <-ctx.Done():
		return LoginResult{}, ErrUnavailable
	}
	state := a.Status()
	return a.Complete(ctx, state.AssertionID, state.Username)
}
