package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/statefile"
)

func TestNativeSessionRestartsOnlyAfterAutoLoginValidation(t *testing.T) {
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	valid := true
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/LoginRequest":
			io.WriteString(w, `{"httpToken":"native-private-canary"}`)
		case "/v1/AuthToken":
			var body struct {
				Token string `json:"token"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Token != "native-private-canary" || r.URL.RawQuery != "" {
				t.Error("wrong native token validation")
			}
			json.NewEncoder(w).Encode(map[string]bool{"valid": valid})
		default:
			t.Error("unexpected token route")
		}
	})
	c.UseSessionStore(store)
	if _, err := c.Login(context.Background(), Credentials{Username: "fixture", Password: "fixture", ServerAddress: "fixture.invalid"}); err != nil {
		t.Fatal(err)
	}
	c.SetToken("")
	// SetToken is an in-memory primitive; generation teardown retains the file.
	if err := c.RestoreSession(context.Background()); err != nil || !c.Authorized() {
		t.Fatal("session not restored", err)
	}
	valid = false
	// A live token is still usable with autoLogin=false; this endpoint's flag
	// describes restart eligibility, not the authorization of active requests.
	if err := c.TouchSession(context.Background()); err != nil || !c.Authorized() {
		t.Fatal("auto-login policy incorrectly revoked live authorization", err)
	}
	c.SetToken("")
	if err := c.RestoreSession(context.Background()); err != nil || c.Authorized() {
		t.Fatal("ineligible auto-login restored", err)
	}
	// One valid:false must not erase the saved session; only a successful
	// sign-in replaces it. A later eligible answer restores the session.
	raw, err := store.Read("native-session")
	if err != nil || string(raw) == "" {
		t.Fatal("ineligible answer erased the saved token", err)
	}
	valid = true
	if err := c.RestoreSession(context.Background()); err != nil || !c.Authorized() {
		t.Fatal("saved session not restored after a transient valid:false", err)
	}
}

func TestNativeSessionRejectsUnvalidatedTokenOnTransportFailure(t *testing.T) {
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.Write("native-session", []byte("fixture-token"))
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503); io.WriteString(w, `{"valid":true}`) })
	c.UseSessionStore(store)
	if err := c.RestoreSession(context.Background()); err == nil || c.Authorized() {
		t.Fatal("failed token validation accepted")
	}
}

func TestRestoreNeverClearsARecentlyAuthenticatedSession(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("restore queried over a fresh session") })
	c.SetToken("fresh-session")
	if err := c.RestoreSession(context.Background()); err != nil || !c.Authorized() {
		t.Fatal("session restore erased fresh login", err)
	}
}

func TestSessionRestoreDoesNotWaitBehindInteractiveSSO(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("busy restore reached native endpoint") })
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	done := make(chan error, 1)
	go func() { done <- c.RestoreSession(context.Background()) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrLoginInProgress) {
			t.Fatal(err)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("restore blocked behind interactive login")
	}
}

func TestLateLoginCannotInstallTokenInAnotherGeneration(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, `{"httpToken":"old-token"}`)
	})
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	first = c.BindGeneration(first)
	done := make(chan error, 1)
	go func() {
		_, err := c.Login(first, Credentials{Username: "fixture", Password: "fixture", ServerAddress: "fixture.invalid"})
		done <- err
	}()
	<-entered
	c.BindGeneration(context.Background())
	c.SetToken("new-token")
	close(release)
	if err := <-done; !errors.Is(err, ErrIndeterminate) {
		t.Fatal("late generation result accepted", err)
	}
	if c.nativeToken() != "new-token" {
		t.Fatal("old response replaced new generation token")
	}
}
