package lifecycle

import (
	"context"
	"github.com/coder/websocket"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/statefile"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountMutationBudgetCoversNativeLoginAttempts(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	owner := New(Options{Native: native, MonitorInterval: time.Hour})
	owner.Begin(context.Background(), 1)
	defer owner.End(1)
	if err := owner.DoAccount(context.Background(), func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 5*190*time.Second || time.Until(deadline) > 16*time.Minute {
			t.Error("login is truncated by the routine mutation deadline")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptedMutationOutlivesWaitingHTTPClient(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	owner := New(Options{Native: native, MonitorInterval: time.Hour})
	if err := owner.Begin(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	defer owner.End(1)
	entered := make(chan struct{})
	operationContext := make(chan context.Context, 1)
	release := make(chan struct{})
	complete := make(chan struct{})
	request, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- owner.Do(request, func(ctx context.Context) error {
			operationContext <- ctx
			close(entered)
			select {
			case <-release:
				native.SetToken("fresh")
				close(complete)
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	<-entered
	cancel()
	<-result
	if ctx := <-operationContext; ctx.Err() != nil {
		t.Fatal("waiting client canceled the accepted operation")
	}
	close(release)
	select {
	case <-complete:
	case <-time.After(time.Second):
		t.Fatal("client cancellation killed native mutation")
	}
	if !native.Authorized() {
		t.Fatal("native result lost")
	}
}

func TestFailedAutomaticRestoreDoesNotGateEventsOrEraseFreshLogin(t *testing.T) {
	var attempts atomic.Int32
	var eventConnections atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/ApplicationStatus":
			io.WriteString(w, `{"productVersion":"12.1.1","buildNumber":"2"}`)
		case "/v1/AuthToken":
			attempts.Add(1)
			w.WriteHeader(503)
		case "/v0/LoginRequest":
			io.WriteString(w, `{"httpToken":"new-token"}`)
		case "/v0/wsevent":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			eventConnections.Add(1)
			defer conn.CloseNow()
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	store.Write("native-session", []byte("old-token"))
	native.UseSessionStore(store)
	owner := New(Options{Native: native, MonitorInterval: 10 * time.Millisecond})
	owner.Begin(context.Background(), 1)
	defer owner.End(1)
	deadline := time.Now().Add(time.Second)
	for (attempts.Load() == 0 || eventConnections.Load() == 0) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if eventConnections.Load() == 0 {
		t.Fatal("failed session restore blocked event socket")
	}
	time.Sleep(30 * time.Millisecond)
	if attempts.Load() != 1 {
		t.Fatal("failed restoration was repeated", attempts.Load())
	}
	if err := owner.Do(context.Background(), func(ctx context.Context) error {
		_, err := native.Login(ctx, engine.Credentials{Username: "fixture", Password: "fixture", ServerAddress: "fixture.invalid"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if !native.Authorized() || attempts.Load() != 1 {
		t.Fatal("automatic restore retried over a fresh login", attempts.Load())
	}
}

func TestEndedGenerationRejectsActionsAndClearsOnlyAfterWorkersEnd(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	owner := New(Options{Native: native, MonitorInterval: time.Hour})
	owner.Begin(context.Background(), 1)
	native.SetToken("old")
	if err := owner.End(1); err != nil {
		t.Fatal(err)
	}
	if native.Authorized() {
		t.Fatal("ended generation retained authorization")
	}
	if err := owner.Do(context.Background(), func(context.Context) error { t.Error("stopped engine received mutation"); return nil }); err == nil {
		t.Fatal("stopped owner accepted operation")
	}
}

type fixtureRestores struct {
	ctx    context.Context
	native *engine.Client
	began  bool
	ended  bool
	wakes  atomic.Int32
}

func (f *fixtureRestores) Begin(ctx context.Context, id uint64) error {
	f.ctx = ctx
	f.began = id == 1 && !f.native.Authorized()
	return nil
}
func (f *fixtureRestores) End(id uint64) error {
	f.ended = id == 1 && f.ctx.Err() != nil
	select {
	case <-f.native.EventBus().Done():
		f.ended = false
	default:
	}
	return nil
}
func (f *fixtureRestores) Wake() { f.wakes.Add(1) }
func TestRestoreHelperHooksOwnTheSameGenerationAndWakeAfterLogin(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	helpers := &fixtureRestores{native: native}
	owner := New(Options{Native: native, Restores: helpers, MonitorInterval: time.Hour})
	if err := owner.Begin(context.Background(), 1); err != nil || !helpers.began {
		t.Fatal("restore helpers missed generation start", err)
	}
	if err := owner.Do(context.Background(), func(context.Context) error { return native.SetToken("fixture") }); err != nil {
		t.Fatal(err)
	}
	if helpers.wakes.Load() != 1 {
		t.Fatal("native login did not wake pending helper")
	}
	if err := owner.End(1); err != nil || !helpers.ended {
		t.Fatal("restore helpers ended after native event bus", err)
	}
}
