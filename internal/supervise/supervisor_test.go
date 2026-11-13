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
