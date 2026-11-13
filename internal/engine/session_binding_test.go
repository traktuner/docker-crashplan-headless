package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestBoundSessionRefusesChangedRevisionBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, `{}`) })
	c.SetToken("A")
	ctx, err := c.BindSession(context.Background(), c.SessionRevision())
	if err != nil {
		t.Fatal(err)
	}
	c.SetToken("B")
	c.SetToken("A")
	_, err = c.request(ctx, "POST", "/fixture", []byte(`{}`), true, true)
	if !errors.Is(err, ErrSessionChanged) || !errors.Is(err, ErrNotDispatched) || calls.Load() != 0 {
		t.Fatal("changed session dispatched", err, calls.Load())
	}
}
func TestBoundSessionMarksDispatchedChangeIndeterminate(t *testing.T) {
	var c *Client
	c = fixture(t, func(w http.ResponseWriter, r *http.Request) { c.SetToken("B"); io.WriteString(w, `{}`) })
	c.SetToken("A")
	ctx, err := c.BindSession(context.Background(), c.SessionRevision())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.request(ctx, "POST", "/fixture", []byte(`{}`), true, true)
	if !errors.Is(err, ErrIndeterminate) {
		t.Fatal("changed session accepted after dispatch", err)
	}
}
func TestTokenInstallAdvancesOnlyItsCurrentOperation(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token B" {
			t.Error("wrong token")
		}
		io.WriteString(w, `{}`)
	})
	c.SetToken("A")
	first, _ := c.BindSession(context.Background(), c.SessionRevision())
	stale, _ := c.BindSession(context.Background(), c.SessionRevision())
	if err := c.acceptToken(first, "B"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.request(first, "GET", "/fixture", nil, true, false); err != nil {
		t.Fatal("login binding did not advance", err)
	}
	if err := c.acceptToken(stale, "C"); !errors.Is(err, ErrSessionChanged) {
		t.Fatal("stale login installed a token", err)
	}
}

func TestArchiveCredentialsCannotSurviveNativeSessionChange(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("stale archive credentials reached the engine") })
	c.SetToken("A")
	credentials := RestoreCredentials{verified: true, key: "YQ==", revision: c.SessionRevision()}
	c.SetToken("B")
	_, err := c.BrowseArchive(context.Background(), credentials, ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"})
	if !errors.Is(err, ErrSessionChanged) {
		t.Fatal("stale credentials accepted", err)
	}
}

func TestTokenInstallRejectsEndedGenerationWithoutAnOperationBinding(t *testing.T) {
	c := fixture(t, func(http.ResponseWriter, *http.Request) {})
	old := c.BindGeneration(context.Background())
	current := c.BindGeneration(context.Background())
	if err := c.acceptToken(old, "stale"); err == nil || c.Authorized() {
		t.Fatal("ended generation installed a token", err)
	}
	if err := c.acceptToken(current, "current"); err != nil {
		t.Fatal("current generation refused", err)
	}
}

type sessionBody struct {
	io.ReadCloser
	onClose func()
}

func (b sessionBody) Close() error { err := b.ReadCloser.Close(); b.onClose(); return err }

type sessionTransport struct {
	inner   http.RoundTripper
	onClose func()
}

func (t sessionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.inner.RoundTrip(r)
	if err == nil && r.URL.Path == "/v1/DataKey" {
		response.Body = sessionBody{response.Body, t.onClose}
	}
	return response, err
}
func TestKeyReadbackDetectsSessionABAWhileClosingTheResponse(t *testing.T) {
	for _, mode := range []string{"export", "derive"} {
		t.Run(mode, func(t *testing.T) {
			c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"securityKeyType":"CustomKey","dataKey":"a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="}`)
			})
			c.SetToken("A")
			c.http.Transport = sessionTransport{inner: c.http.Transport, onClose: func() { c.SetToken("B"); c.SetToken("A") }}
			var err error
			if mode == "export" {
				_, err = c.ExportArchiveKey(context.Background())
			} else {
				_, err = c.ExportDerivedKey(context.Background(), DerivedKeyInput{SourceID: "7", Passphrase: "fixture phrase"})
			}
			if err == nil {
				t.Fatal("key escaped its admitting session after ABA")
			}
		})
	}
}
