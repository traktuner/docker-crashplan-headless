package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestSSOSharesOnePersistentNativeEventConnectionWithRestore(t *testing.T) {
	var connections atomic.Int32
	ready := make(chan *websocket.Conn, 1)
	received := make(chan map[string]json.RawMessage, 16)
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/LoginConfiguration":
			io.WriteString(w, `{"loginType":"cloud_sso"}`)
		case "/v0/wsevent":
			connections.Add(1)
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.CloseNow()
			ready <- conn
			for {
				var event map[string]json.RawMessage
				if wsjson.Read(r.Context(), conn, &event) != nil {
					return
				}
				received <- event
			}
		case "/v0/SsoAuthInitiateRequest":
			io.WriteString(w, `{"ssoLoginUrl":"https://login.fixture.invalid/"}`)
		case "/v0/LoginRequest":
			io.WriteString(w, `{"httpToken":"private-native-token"}`)
		default:
			t.Error("unexpected native request")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	bus, err := c.OpenEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseEvents()
	restore, closeRestore, err := bus.Subscribe("LaunchRestoreToolMessage")
	if err != nil {
		t.Fatal(err)
	}
	defer closeRestore()
	conn := <-ready
	attempt, err := c.BeginSSO(ctx, LoginInfo{Username: "fixture", ServerAddress: "console.fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"SsoAuthFinalizeMessage": map[string]string{"username": "fixture", "authToken": "private-sso-token"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := completeConfirmedSSO(t, attempt, ctx); err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 1 {
		t.Fatal("SSO opened another websocket")
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"LaunchRestoreToolMessage": map[string]string{"userName": "root", "requestPipePath": "/fixture/request", "responsePipePath": "/fixture/response"}}); err != nil {
		t.Fatal("SSO close terminated shared bus", err)
	}
	select {
	case event, ok := <-restore:
		if !ok || !json.Valid(event) {
			t.Fatal("restore event lost")
		}
	case <-ctx.Done():
		t.Fatal("restore event not delivered")
	}
	for {
		select {
		case msg := <-received:
			if raw, ok := msg["LoginTokenRequestMessage"]; ok {
				var auth struct {
					Token string `json:"token"`
				}
				json.Unmarshal(raw, &auth)
				if auth.Token != "private-native-token" {
					t.Fatal("event auth token missing")
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("event session was not authenticated")
		}
	}
}

func TestEventDisconnectPreservesSubscriptionForReconnect(t *testing.T) {
	closePeer := make(chan struct{})
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-closePeer
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	bus, err := c.OpenEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseEvents()
	events, unsub, err := bus.Subscribe("LaunchRestoreToolMessage")
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()
	close(closePeer)
	select {
	case <-events:
		t.Fatal("generation subscription closed on transport loss")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestCriticalSubscriptionExistsBeforeDialAndSurvivesReconnect(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		n := calls.Add(1)
		wsjson.Write(r.Context(), conn, map[string]any{"LaunchRestoreToolMessage": map[string]int32{"sequence": n}})
		if n == 1 {
			return
		}
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	defer c.CloseEvents()
	bus := c.EventBus()
	messages, unsubscribe, err := bus.Subscribe("LaunchRestoreToolMessage")
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if _, err := c.OpenEvents(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-messages:
	case <-ctx.Done():
		t.Fatal("initial launch lost")
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := c.OpenEvents(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case raw, ok := <-messages:
		if !ok || !strings.Contains(string(raw), "2") {
			t.Fatal("reconnect launch lost")
		}
	case <-ctx.Done():
		t.Fatal("reconnect launch not delivered")
	}
}
