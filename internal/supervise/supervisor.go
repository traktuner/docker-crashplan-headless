// Package supervise owns only the container's JVM process group. It does not
// control the Docker host, host memory, other containers, or external services.
package supervise

import (
	"context"
	"errors"
	"github.com/traktuner/crashplan-headless/internal/sources"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var ErrConfiguration = errors.New("INVALID_SUPERVISOR_CONFIGURATION")
var ErrEngineExit = errors.New("ENGINE_EXITED")
var ErrNotRunning = errors.New("ENGINE_NOT_RUNNING")

type SourceGate interface{ Check() error }
type Generations interface {
	Begin(context.Context, uint64) error
	End(uint64) error
}
type Options struct {
	Command        []string
	Environment    []string
	Sources        SourceGate
	Logger         *slog.Logger
	SourceInterval time.Duration
	// SourceTimeout bounds one source check (default 20 s). A check that
	// takes longer counts as an ambiguous failure; it is not started again
	// while it still runs.
	SourceTimeout time.Duration
	// SourceFailures is how many consecutive ambiguous I/O failures stop a
	// running engine (default 3). Definite states stop it at once.
	SourceFailures int
	StopGrace      time.Duration
	Generations    Generations
	Failures       <-chan error
	Stdout, Stderr io.Writer
}
type State struct {
	Status     string `json:"status"`
	PID        int    `json:"pid"`
	Generation uint64 `json:"generation"`
	ExitCode   *int   `json:"exitCode,omitempty"`
}
type Supervisor struct {
	mu       sync.RWMutex
	options  Options
	state    State
	restart  chan uint64
	checkMu  sync.Mutex
	inflight chan error
}

// errSourceTimeout is an ambiguous failure: the check did not return in time.
var errSourceTimeout = sources.ErrNotReady

func New(options Options) (*Supervisor, error) {
	if len(options.Command) == 0 || options.Sources == nil {
		return nil, ErrConfiguration
	}
	if options.SourceInterval == 0 {
		options.SourceInterval = 30 * time.Second
	}
	if options.StopGrace == 0 {
		options.StopGrace = 120 * time.Second
	}
	if options.SourceTimeout == 0 {
		options.SourceTimeout = 20 * time.Second
	}
	if options.SourceFailures == 0 {
		options.SourceFailures = 3
	}
	if options.SourceInterval < time.Millisecond || options.StopGrace < time.Millisecond || options.SourceTimeout < time.Millisecond || options.SourceFailures < 1 {
		return nil, ErrConfiguration
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Supervisor{options: options, state: State{Status: "waiting_source"}, restart: make(chan uint64, 1)}, nil
}

// Check runs one source check with the configured deadline. At most one
// check runs at a time: after a timeout the hung call keeps its goroutine and
// the next Check waits for that same call instead of starting another. The
// API's scan path uses this too, so a hung NFS call cannot block a worker.
func (s *Supervisor) Check() error {
	s.checkMu.Lock()
	if s.inflight == nil {
		result := make(chan error, 1)
		s.inflight = result
		gate := s.options.Sources
		go func() { result <- gate.Check() }()
	}
	result := s.inflight
	s.checkMu.Unlock()
	timer := time.NewTimer(s.options.SourceTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		s.checkMu.Lock()
		if s.inflight == result {
			s.inflight = nil
		}
		s.checkMu.Unlock()
		return err
	case <-timer.C:
		s.options.Logger.Warn("source_check_timeout", "timeout_s", int(s.options.SourceTimeout/time.Second))
		return errSourceTimeout
	}
}

// ambiguousSource reports a failure that is not a definite source state.
func ambiguousSource(err error) bool {
	var reason sources.Reason
	return !errors.As(err, &reason)
}

func (s *Supervisor) Snapshot() State { s.mu.RLock(); defer s.mu.RUnlock(); return s.state }
func (s *Supervisor) update(status string, pid int, exit *int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Status = status
	s.state.PID = pid
	s.state.ExitCode = exit
	if status == "running" {
		s.state.Generation++
	}
}
func (s *Supervisor) Restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.PID == 0 || s.state.Status != "running" {
		return ErrNotRunning
	}
	select {
	case s.restart <- s.state.Generation:
	default:
	}
	return nil
}

func (s *Supervisor) stop(cmd *exec.Cmd, exit <-chan error) error {
	s.update("stopping", cmd.Process.Pid, nil)
	cleanupErr := s.endGeneration()
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return ErrEngineExit
	}
	timer := time.NewTimer(s.options.StopGrace)
	defer timer.Stop()
	select {
	case <-exit:
		return cleanupErr
	case <-timer.C:
		s.options.Logger.Warn("engine_stop_grace_expired")
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return ErrEngineExit
		}
		<-exit
		return cleanupErr
	}
}

func (s *Supervisor) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.options.SourceInterval)
	defer ticker.Stop()
	for {
		lastSourceReason := ""
		for sourceErr := s.Check(); sourceErr != nil; sourceErr = s.Check() {
			reason := sourceReason(sourceErr)
			if reason != lastSourceReason {
				if changed, ok := s.options.Sources.(interface{ Changed() string }); ok && reason == "SOURCE_CHANGED" {
					s.options.Logger.Warn("source_not_ready", "reason", reason, "source", changed.Changed(),
						"hint", "the source now comes from another filesystem or path; mount the original data, or remove /config/cph/source-identities.json while stopped if the change is intended")
				} else {
					s.options.Logger.Warn("source_not_ready", "reason", reason)
				}
				lastSourceReason = reason
			}
			s.update("waiting_source", 0, nil)
			select {
			case <-ctx.Done():
				s.update("stopped", 0, nil)
				return nil
			case <-ticker.C:
			}
		}
		if ctx.Err() != nil {
			s.update("stopped", 0, nil)
			return nil
		}
		cmd := exec.Command(s.options.Command[0], s.options.Command[1:]...)
		cmd.Env = s.options.Environment
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stdout = s.options.Stdout
		cmd.Stderr = s.options.Stderr
		if err := cmd.Start(); err != nil {
			s.update("failed", 0, nil)
			s.options.Logger.Error("engine_start_failed")
			return ErrEngineExit
		}
		generation := s.Snapshot().Generation + 1
		if s.options.Generations != nil {
			if err := s.options.Generations.Begin(ctx, generation); err != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				cmd.Wait()
				return err
			}
		}
		for {
			select {
			case <-s.restart:
				continue
			default:
			}
			break
		}
		s.update("running", cmd.Process.Pid, nil)
		s.options.Logger.Info("engine_started", "generation", s.Snapshot().Generation)
		exit := make(chan error, 1)
		go func() { exit <- cmd.Wait() }()
		restarting := false
		ambiguous := 0
	inner:
		for {
			select {
			case <-ctx.Done():
				err := s.stop(cmd, exit)
				s.update("stopped", 0, nil)
				return err
			case requested := <-s.restart:
				if requested != s.Snapshot().Generation {
					continue
				}
				restarting = true
				if err := s.stop(cmd, exit); err != nil {
					return err
				}
				break inner
			case <-ticker.C:
				sourceErr := s.Check()
				if sourceErr == nil {
					if ambiguous > 0 {
						s.options.Logger.Info("source_check_recovered")
					}
					ambiguous = 0
					continue
				}
				if ambiguousSource(sourceErr) {
					// An I/O error or timeout on a still-mounted source: stop
					// only after consecutive failures. Unmounts and changes are
					// definite and stop at once.
					ambiguous++
					if ambiguous < s.options.SourceFailures {
						s.options.Logger.Warn("source_check_failed", "attempt", ambiguous, "limit", s.options.SourceFailures)
						continue
					}
				}
				s.options.Logger.Error("source_not_ready", "reason", sourceReason(sourceErr))
				if err := s.stop(cmd, exit); err != nil {
					return err
				}
				break inner
			case failure := <-s.options.Failures:
				if failure == nil {
					continue
				}
				if err := s.stop(cmd, exit); err != nil {
					return err
				}
				s.update("failed", 0, nil)
				return failure
			case err := <-exit:
				s.endGeneration()
				code := 0
				if err != nil {
					var failed *exec.ExitError
					if errors.As(err, &failed) {
						code = failed.ExitCode()
					} else {
						code = -1
					}
				}
				s.update("failed", 0, &code)
				s.options.Logger.Error("engine_exited", "exit_code", code)
				return ErrEngineExit
			}
		}
		s.update("waiting_source", 0, nil)
		if restarting {
			s.options.Logger.Info("engine_restart_requested")
		}
	}
}

func (s *Supervisor) endGeneration() error {
	if s.options.Generations != nil {
		if err := s.options.Generations.End(s.Snapshot().Generation); err != nil {
			s.options.Logger.Warn("generation_cleanup_failed")
			return err
		}
	}
	return nil
}
func sourceReason(err error) string {
	var reason sources.Reason
	if errors.As(err, &reason) {
		return string(reason)
	}
	return "SOURCE_NOT_READY"
}
