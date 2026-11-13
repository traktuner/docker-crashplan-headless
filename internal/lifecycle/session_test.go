package lifecycle

import (
	"context"
	"errors"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueuedOperationCannotCrossSessionInstallation(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	o := New(Options{Native: native, MonitorInterval: time.Hour})
	o.Begin(context.Background(), 1)
	defer o.End(1)
	native.SetToken("A")
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- o.Do(context.Background(), func(context.Context) error { close(entered); <-release; return native.SetToken("B") })
	}()
	<-entered
	called := false
	second := make(chan error, 1)
	go func() {
		second <- o.Do(context.Background(), func(context.Context) error { called = true; return nil })
	}()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for len(o.current.queue) == 0 {
		select {
		case <-ticker.C:
		case <-deadline:
			close(release)
			t.Fatal("second operation not queued")
		}
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; !errors.Is(err, engine.ErrSessionChanged) || called {
		t.Fatal("stale queued operation executed", err, called)
	}
	if err := o.Do(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal("fresh operation refused", err)
	}
}
