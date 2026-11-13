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
	mu      sync.RWMutex
	options Options
	state   State
	restart chan uint64
}

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
	if options.SourceInterval < time.Millisecond || options.StopGrace < time.Millisecond {
		return nil, ErrConfiguration
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Supervisor{options: options, state: State{Status: "waiting_source"}, restart: make(chan uint64, 1)}, nil
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
		for sourceErr := s.options.Sources.Check(); sourceErr != nil; sourceErr = s.options.Sources.Check() {
			reason := sourceReason(sourceErr)
			if reason != lastSourceReason {
				s.options.Logger.Warn("source_not_ready", "reason", reason)
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
				if s.options.Sources.Check() != nil {
					s.options.Logger.Error("source_not_ready")
					if err := s.stop(cmd, exit); err != nil {
						return err
					}
					break inner
				}
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
