package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/traktuner/crashplan-headless/internal/engine"
)

var ErrHelper = errors.New("RESTORE_HELPER_UNAVAILABLE")
var ErrPendingJob = errors.New("RESTORE_PENDING_JOB_OUTSIDE_ROOTS")

type HelperOptions struct {
	Native                                         *engine.Client
	Destinations                                   *Destinations
	Binary, ExpectedSHA, DataDir, LogDir, UserName string
	Console                                        io.Writer
	Logger                                         *slog.Logger
}
type HelperState struct {
	Generation uint64 `json:"generation"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	PID        int    `json:"pid"`
	Starts     int    `json:"starts"`
}
type launchMessage struct {
	User     string `json:"userName"`
	Request  string `json:"requestPipePath"`
	Response string `json:"responsePipePath"`
}
type helperGeneration struct {
	id           uint64
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	wake         chan struct{}
	data         *os.File
	dataIdentity os.FileInfo
	stopErr      error
	verified     bool
}
type Helpers struct {
	mu      sync.Mutex
	options HelperOptions
	current *helperGeneration
	state   HelperState
}

func NewHelpers(options HelperOptions) (*Helpers, error) {
	if options.Native == nil || options.Destinations == nil || !filepath.IsAbs(options.Binary) || !filepath.IsAbs(options.DataDir) || !filepath.IsAbs(options.LogDir) || options.UserName == "" || len(options.ExpectedSHA) != 64 {
		return nil, ErrHelper
	}
	if _, err := hex.DecodeString(options.ExpectedSHA); err != nil {
		return nil, ErrHelper
	}
	if options.Console == nil {
		options.Console = io.Discard
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Helpers{options: options, state: HelperState{State: "stopped"}}, nil
}
func (h *Helpers) Snapshot() HelperState { h.mu.Lock(); defer h.mu.Unlock(); return h.state }
func (h *Helpers) set(g *helperGeneration, state, reason string, pid int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current == g {
		h.state.State = state
		h.state.Reason = reason
		h.state.PID = pid
	}
}
func (h *Helpers) Begin(parent context.Context, id uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		return ErrHelper
	}
	data, err := openDirectory(h.options.DataDir)
	if err != nil {
		return ErrHelper
	}
	info, err := data.Stat()
	if err != nil {
		data.Close()
		return ErrHelper
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
		data.Close()
		return ErrHelper
	}
	ch, unsubscribe, err := h.options.Native.EventBus().Subscribe("LaunchRestoreToolMessage")
	if err != nil {
		data.Close()
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	g := &helperGeneration{id: id, ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), data: data, dataIdentity: info}
	h.current = g
	h.state = HelperState{Generation: id, State: "waiting"}
	go h.loop(g, ch, unsubscribe)
	return nil
}
func (h *Helpers) End(id uint64) error {
	h.mu.Lock()
	g := h.current
	if g == nil || g.id != id {
		h.mu.Unlock()
		return nil
	}
	g.cancel()
	h.mu.Unlock()
	<-g.done
	return g.stopErr
}

// Wake rechecks a retained native launch after login or job control. It never
// resubmits a restore job and never creates another native restore request.
func (h *Helpers) Wake() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		select {
		case h.current.wake <- struct{}{}:
		default:
		}
	}
}

var pipeName = regexp.MustCompile(`^restore-pipe-([0-9]{1,20})-(request|response)$`)

type pipePair struct {
	message           launchMessage
	request, response os.FileInfo
}

func (h *Helpers) pipes(g *helperGeneration, m launchMessage) (pipePair, error) {
	if m.User != h.options.UserName {
		return pipePair{}, ErrHelper
	}
	req, resp := pipeName.FindStringSubmatch(filepath.Base(m.Request)), pipeName.FindStringSubmatch(filepath.Base(m.Response))
	if len(req) != 3 || len(resp) != 3 || req[1] != resp[1] || req[2] != "request" || resp[2] != "response" || filepath.Dir(m.Request) != filepath.Dir(m.Response) || !filepath.IsAbs(m.Request) || filepath.Clean(m.Request) != m.Request || filepath.Clean(m.Response) != m.Response {
		return pipePair{}, ErrHelper
	}
	// The image's /var/lib/crashplan alias and /config/var identify one directory.
	parent, err := os.Open(filepath.Dir(m.Request))
	if err != nil {
		return pipePair{}, ErrHelper
	}
	info, err := parent.Stat()
	parent.Close()
	if err != nil || !os.SameFile(info, g.dataIdentity) {
		return pipePair{}, ErrHelper
	}
	pair := pipePair{message: m}
	for _, item := range []struct {
		path string
		out  *os.FileInfo
	}{{m.Request, &pair.request}, {m.Response, &pair.response}} {
		// Never open a FIFO for validation: that can release the native pipe handshake.
		info, err := os.Lstat(item.path)
		if err != nil {
			return pipePair{}, ErrHelper
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeNamedPipe == 0 || info.Mode().Perm() != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
			return pipePair{}, ErrHelper
		}
		*item.out = info
	}
	return pair, nil
}
func (h *Helpers) destination(path string) (*Destination, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrPendingJob
	}
	for index, root := range h.options.Destinations.Paths() {
		if path == root || strings.HasPrefix(path, root+"/") {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return nil, ErrPendingJob
			}
			d, err := h.options.Destinations.Resolve(strconv.Itoa(index), relative)
			if err != nil {
				return nil, ErrPendingJob
			}
			if d.Validate() != nil || !d.Writable() {
				d.Close()
				return nil, ErrPendingJob
			}
			return d, nil
		}
	}
	return nil, ErrPendingJob
}
func (h *Helpers) admit(ctx context.Context) error {
	jobs, err := h.options.Native.RestoreJobs(ctx)
	if err != nil {
		return err
	}
	count := 0
	for _, job := range jobs {
		if job.Completed || job.Canceled || !job.UsesHelper() {
			continue
		}
		if job.UserName() != h.options.UserName {
			return ErrPendingJob
		}
		d, err := h.destination(job.Destination())
		if err != nil {
			return err
		}
		d.Close()
		count++
	}
	if count == 0 {
		return ErrHelper
	}
	return nil
}
func (h *Helpers) verifyBinary(g *helperGeneration) error {
	if g.verified {
		return nil
	}
	fd, err := syscall.Open(h.options.Binary, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ErrHelper
	}
	f := os.NewFile(uintptr(fd), "restore-helper")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return ErrHelper
	}
	sum := sha256.New()
	n, err := io.Copy(sum, io.LimitReader(f, 64<<20+1))
	if err != nil || n > 64<<20 || hex.EncodeToString(sum.Sum(nil)) != h.options.ExpectedSHA {
		return ErrHelper
	}
	g.verified = true
	return nil
}

type helperOutput struct {
	mu       sync.Mutex
	buffer   []byte
	seen     bool
	ready    chan error
	expected launchMessage
	console  io.Writer
}

func (o *helperOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	if !o.seen {
		index := bytes.IndexByte(p, '\n')
		end := len(p)
		if index >= 0 {
			end = index + 1
		}
		if len(o.buffer)+end > 64<<10 {
			o.seen = true
			o.ready <- ErrHelper
			o.buffer = nil
			return n, nil
		}
		o.buffer = append(o.buffer, p[:end]...)
		if index < 0 {
			return n, nil
		}
		var result struct {
			Request  string `json:"RequestPipePath"`
			Response string `json:"ResponsePipePath"`
			Error    string `json:"Error"`
		}
		err := error(nil)
		if json.Unmarshal(o.buffer, &result) != nil || result.Request != o.expected.Request || result.Response != o.expected.Response || result.Error != "" {
			err = ErrHelper
		}
		o.seen = true
		o.buffer = nil
		o.ready <- err
		p = p[end:]
	}
	if len(p) > 0 {
		if _, err := o.console.Write(p); err != nil {
			return n, ErrHelper
		}
	}
	return n, nil
}
func (o *helperOutput) finish() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.seen {
		o.seen = true
		o.ready <- ErrHelper
	}
}

type helperProcess struct {
	cmd       *exec.Cmd
	done      chan struct{}
	output    *helperOutput
	pair      pipePair
	err       error
	stopTried bool
	stopErr   error
}

func (h *Helpers) start(g *helperGeneration, pair pipePair) (*helperProcess, error) {
	if h.verifyBinary(g) != nil {
		return nil, ErrHelper
	}
	output := &helperOutput{ready: make(chan error, 1), expected: pair.message, console: h.options.Console}
	cmd := exec.Command(h.options.Binary, "-userName", h.options.UserName, "-logDir", h.options.LogDir, pair.message.Request, pair.message.Response)
	cmd.Env = []string{}
	cmd.Stdin = nil
	cmd.Stdout = output
	cmd.Stderr = h.options.Console
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		return nil, ErrHelper
	}
	p := &helperProcess{cmd: cmd, done: make(chan struct{}), output: output, pair: pair}
	h.mu.Lock()
	if h.current == g {
		h.state.Starts++
	}
	h.mu.Unlock()
	go func() { p.err = cmd.Wait(); output.finish(); close(p.done) }()
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	select {
	case err := <-output.ready:
		if err == nil {
			return p, nil
		}
	case <-g.ctx.Done():
	case <-timer.C:
	}
	if err := stopHelper(p); err != nil {
		h.options.Logger.Error("restore_helper_stop_failed")
		return p, ErrHelper
	}
	return nil, ErrHelper
}
func stopHelper(p *helperProcess) (result error) {
	if p == nil {
		return nil
	}
	select {
	case <-p.done:
		return nil
	default:
	}
	if p.stopTried {
		return p.stopErr
	}
	p.stopTried = true
	defer func() { p.stopErr = result }()
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return ErrHelper
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return ErrHelper
	}
	timer.Reset(5 * time.Second)
	select {
	case <-p.done:
		return nil
	case <-timer.C:
		return ErrHelper
	}
}
func (h *Helpers) loop(g *helperGeneration, requests <-chan json.RawMessage, unsubscribe func()) {
	var process *helperProcess
	var pending *launchMessage
	reject := func(reason string) {
		if process != nil {
			h.set(g, "running", reason, process.cmd.Process.Pid)
		} else {
			h.set(g, "rejected", reason, 0)
		}
	}
	defer func() {
		g.stopErr = stopHelper(process)
		unsubscribe()
		g.data.Close()
		h.mu.Lock()
		if h.current == g {
			if g.stopErr == nil {
				h.state.PID = 0
				h.state.State = "stopped"
				h.current = nil
			} else {
				h.state.State = "failed"
				h.state.Reason = "RESTORE_HELPER_STOP_FAILED"
				if process != nil {
					h.state.PID = process.cmd.Process.Pid
				}
			}
		}
		h.mu.Unlock()
		close(g.done)
	}()
	for {
		var message launchMessage
		var exited <-chan struct{}
		if process != nil {
			exited = process.done
		}
		select {
		case <-g.ctx.Done():
			return
		case <-exited:
			reason := ""
			if process.err != nil {
				reason = "RESTORE_HELPER_EXITED"
				h.options.Logger.Warn("restore_helper_exited")
			}
			process = nil
			h.set(g, "exited", reason, 0)
			continue
		case <-g.wake:
			if pending == nil || process != nil {
				continue
			}
			message = *pending
		case raw := <-requests:
			if json.Unmarshal(raw, &message) != nil {
				reject("RESTORE_HELPER_INVALID_REQUEST")
				continue
			}
		}
		if g.ctx.Err() != nil {
			return
		}
		pair, err := h.pipes(g, message)
		if err != nil {
			reject("RESTORE_HELPER_INVALID_PIPES")
			continue
		}
		if process != nil {
			if process.pair.message == message && os.SameFile(process.pair.request, pair.request) && os.SameFile(process.pair.response, pair.response) {
				continue
			}
			if err := stopHelper(process); err != nil {
				h.set(g, "failed", "RESTORE_HELPER_STOP_FAILED", process.cmd.Process.Pid)
				return
			}
			process = nil
			h.set(g, "waiting", "", 0)
		}
		pending = &message
		ctx, cancel := context.WithTimeout(g.ctx, 30*time.Second)
		err = h.admit(ctx)
		cancel()
		if err != nil {
			reason := "RESTORE_HELPER_ADMISSION_FAILED"
			if errors.Is(err, ErrPendingJob) {
				reason = ErrPendingJob.Error()
			}
			if errors.Is(err, engine.ErrUnauthorized) {
				reason = "NATIVE_AUTHORIZATION_REQUIRED"
			}
			h.set(g, "rejected", reason, 0)
			h.options.Logger.Warn("restore_helper_refused", "reason", reason)
			continue
		}
		fresh, err := h.pipes(g, message)
		if err != nil || !os.SameFile(pair.request, fresh.request) || !os.SameFile(pair.response, fresh.response) {
			h.set(g, "rejected", "RESTORE_HELPER_PIPE_CHANGED", 0)
			continue
		}
		process, err = h.start(g, pair)
		if err != nil {
			if process != nil {
				h.set(g, "failed", "RESTORE_HELPER_STOP_FAILED", process.cmd.Process.Pid)
				return
			}
			h.set(g, "failed", "RESTORE_HELPER_LAUNCH_FAILED", 0)
			continue
		}
		pending = nil
		h.set(g, "running", "", process.cmd.Process.Pid)
		h.options.Logger.Info("restore_helper_started", "generation", g.id)
	}
}
