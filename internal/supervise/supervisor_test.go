package supervise

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/sources"
)

type testGate struct{ ready atomic.Bool }

func (g *testGate) Check() error {
	if !g.ready.Load() {
		return errors.New("not ready")
	}
	return nil
}

func TestChildWaitsForSourcesAndStopsNormally(t *testing.T) {
	if os.Getenv("CPH_TEST_CHILD") == "1" {
		time.Sleep(time.Hour)
		return
	}
	gate := &testGate{}
	s, err := New(Options{Command: []string{os.Args[0], "-test.run=TestChildWaitsForSourcesAndStopsNormally"}, Environment: append(os.Environ(), "CPH_TEST_CHILD=1"), Sources: gate, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), SourceInterval: 10 * time.Millisecond, StopGrace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(30 * time.Millisecond)
	if s.Snapshot().PID != 0 {
		t.Fatal("engine started before source readiness")
	}
	gate.ready.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for s.Snapshot().PID == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Snapshot().PID == 0 {
		t.Fatal("engine did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("child shutdown hung")
	}
	if s.Snapshot().PID != 0 {
		t.Fatal("stopped process still reported running")
	}
}

func TestMissingRequiredSourceGateCannotStartEngine(t *testing.T) {
	if _, err := New(Options{Command: []string{"/bin/true"}}); err == nil {
		t.Fatal("missing source check accepted")
	}
}

func TestRestartIsRejectedDuringStopping(t *testing.T) {
	s, _ := New(Options{Command: []string{"/bin/true"}, Sources: &testGate{}})
	s.update("stopping", 123, nil)
	if err := s.Restart(); !errors.Is(err, ErrNotRunning) {
		t.Fatal("restart queued for the next engine", err)
	}
}

type scriptGate struct {
	calls atomic.Int32
	next  func(call int32) error
}

func (g *scriptGate) Check() error { return g.next(g.calls.Add(1)) }

func runSourceScript(t *testing.T, name string, gate SourceGate, failures int) (*Supervisor, context.CancelFunc, chan error) {
	t.Helper()
	s, err := New(Options{Command: []string{os.Args[0], "-test.run=" + name}, Environment: append(os.Environ(), "CPH_TEST_CHILD=1"), Sources: gate, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), SourceInterval: 10 * time.Millisecond, StopGrace: time.Second, SourceFailures: failures})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	return s, cancel, done
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(time.Millisecond)
	}
}

// Perf 3: two ambiguous I/O failures in a row do not stop the engine; a
// success resets the count.
func TestAmbiguousSourceErrorsAreDebounced(t *testing.T) {
	if os.Getenv("CPH_TEST_CHILD") == "1" {
		time.Sleep(time.Hour)
		return
	}
	gate := &scriptGate{next: func(call int32) error {
		// call 1 starts the engine; then fail, fail, ok, fail, fail, ok...
		if call > 1 && call%3 != 1 {
			return sources.ErrNotReady
		}
		return nil
	}}
	s, cancel, done := runSourceScript(t, "TestAmbiguousSourceErrorsAreDebounced", gate, 0)
	defer cancel()
	waitFor(t, "engine did not start", func() bool { return s.Snapshot().PID != 0 })
	pid := s.Snapshot().PID
	waitFor(t, "checks did not run", func() bool { return gate.calls.Load() > 12 })
	if s.Snapshot().PID != pid || s.Snapshot().Status != "running" {
		t.Fatal("ambiguous source errors stopped the engine", s.Snapshot())
	}
	cancel()
	<-done
}

func TestThreeAmbiguousSourceErrorsStopTheEngine(t *testing.T) {
	if os.Getenv("CPH_TEST_CHILD") == "1" {
		time.Sleep(time.Hour)
		return
	}
	var failing atomic.Bool
	gate := &scriptGate{next: func(int32) error {
		if failing.Load() {
			return sources.ErrNotReady
		}
		return nil
	}}
	s, cancel, done := runSourceScript(t, "TestThreeAmbiguousSourceErrorsStopTheEngine", gate, 0)
	defer cancel()
	waitFor(t, "engine did not start", func() bool { return s.Snapshot().PID != 0 })
	start := gate.calls.Load()
	failing.Store(true)
	waitFor(t, "engine kept running", func() bool { return s.Snapshot().PID == 0 })
	if n := gate.calls.Load() - start; n < 3 {
		t.Fatal("engine stopped before three consecutive failures", n)
	}
	cancel()
	<-done
}

func TestDefiniteSourceStateStopsAtOnce(t *testing.T) {
	if os.Getenv("CPH_TEST_CHILD") == "1" {
		time.Sleep(time.Hour)
		return
	}
	var unmounted atomic.Bool
	gate := &scriptGate{next: func(int32) error {
		if unmounted.Load() {
			return sources.NotMounted
		}
		return nil
	}}
	// With a debounce of 1000 only a definite state can stop the engine here.
	s, cancel, done := runSourceScript(t, "TestDefiniteSourceStateStopsAtOnce", gate, 1000)
	defer cancel()
	waitFor(t, "engine did not start", func() bool { return s.Snapshot().PID != 0 })
	unmounted.Store(true)
	waitFor(t, "a definite source state did not stop the engine", func() bool { return s.Snapshot().PID == 0 })
	cancel()
	<-done
}

type hungGate struct {
	calls   atomic.Int32
	release chan struct{}
}

func (g *hungGate) Check() error { g.calls.Add(1); <-g.release; return nil }

func TestHungSourceCheckHasADeadlineAndRunsOnce(t *testing.T) {
	gate := &hungGate{release: make(chan struct{})}
	s, err := New(Options{Command: []string{"/bin/true"}, Sources: gate, SourceTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := s.Check(); !errors.Is(err, sources.ErrNotReady) || !ambiguousSource(err) {
		t.Fatal("hung check not reported as ambiguous", err)
	}
	if err := s.Check(); err == nil || time.Since(started) > time.Second {
		t.Fatal("hung check blocked the caller", err)
	}
	if gate.calls.Load() != 1 {
		t.Fatal("a second check started while the first still hangs", gate.calls.Load())
	}
	close(gate.release)
	waitFor(t, "released check result not delivered", func() bool { return s.Check() == nil })
}
