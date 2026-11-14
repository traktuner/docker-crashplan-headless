// Package lifecycle owns native control for each JVM generation.
package lifecycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

var ErrBusy = errors.New("CONTROL_BUSY")
var ErrVersionDrift = errors.New("ENGINE_VERSION_DRIFT")

type RestoreGenerations interface {
	Begin(context.Context, uint64) error
	End(uint64) error
	Wake()
}

type Options struct {
	Restores        RestoreGenerations
	Native          *engine.Client
	Scans           *engine.Scans
	Logger          *slog.Logger
	MonitorInterval time.Duration
	Version, Build  string
}
type State struct {
	Generation       string `json:"generation"`
	NativeReady      bool   `json:"nativeReady"`
	NativeSession    bool   `json:"nativeSession"`
	EventsReady      bool   `json:"eventsReady"`
	SessionRestore   string `json:"sessionRestore"`
	ScanJournalReady bool   `json:"scanJournalReady"`
}
type operation struct {
	fn       func(context.Context) error
	result   chan error
	timeout  time.Duration
	revision uint64
}
type generation struct {
	id     uint64
	ctx    context.Context
	cancel context.CancelFunc
	queue  chan operation
	wg     sync.WaitGroup
	state  State
}
type Owner struct {
	mu       sync.RWMutex
	current  *generation
	options  Options
	notify   func()
	failures chan error
}

func New(options Options) *Owner {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.MonitorInterval == 0 {
		options.MonitorInterval = 5 * time.Second
	}
	return &Owner{options: options, failures: make(chan error, 1)}
}
func (o *Owner) Failures() <-chan error { return o.failures }
func (o *Owner) SetNotify(fn func())    { o.mu.Lock(); defer o.mu.Unlock(); o.notify = fn }
func (o *Owner) changed() {
	o.mu.RLock()
	fn := o.notify
	o.mu.RUnlock()
	if fn != nil {
		fn()
	}
}
func (o *Owner) Snapshot() State {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.current == nil {
		return State{SessionRestore: "not_running", ScanJournalReady: o.options.Scans != nil}
	}
	state := o.current.state
	state.NativeSession = o.options.Native.Authorized()
	state.EventsReady = o.options.Native.EventBus().Connected()
	return state
}

func (o *Owner) Begin(parent context.Context, id uint64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.current != nil {
		return ErrBusy
	}
	var unique [16]byte
	if _, err := rand.Read(unique[:]); err != nil {
		return engine.ErrUnavailable
	}
	// The random part keeps journal records of an earlier daemon process apart.
	label := hex.EncodeToString(unique[:]) + "-" + strconv.FormatUint(id, 10)
	ctx, cancel := context.WithCancel(parent)
	o.options.Native.SetToken("")
	ctx = o.options.Native.BindGeneration(ctx)
	g := &generation{id: id, ctx: ctx, cancel: cancel, queue: make(chan operation, 16), state: State{Generation: label, SessionRestore: "pending", ScanJournalReady: o.options.Scans != nil}}
	if o.options.Scans != nil {
		if err := o.options.Scans.BeginGeneration(label); err != nil {
			g.state.ScanJournalReady = false
			o.options.Logger.Warn("scan_journal_unavailable")
		}
	}
	if o.options.Restores != nil {
		if err := o.options.Restores.Begin(ctx, id); err != nil {
			cancel()
			o.options.Native.BindGeneration(nil)
			return err
		}
	}
	o.current = g
	// Register subscriptions before the first dial. UI invalidations coalesce.
	bus := o.options.Native.EventBus()
	for _, kind := range []string{"ApplicationStatusMessage", "BackupStatsSummaryMessage", "ScanStatsMessage", "RestoreJobMessage"} {
		ch, unsubscribe, err := bus.Subscribe(kind)
		if err != nil {
			continue
		}
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			defer unsubscribe()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ch:
					o.changed()
				}
			}
		}()
	}
	g.wg.Add(2)
	go o.work(g)
	go o.monitor(g)
	return nil
}
func (o *Owner) End(id uint64) error {
	o.mu.RLock()
	g := o.current
	o.mu.RUnlock()
	if g == nil || g.id != id {
		return nil
	}
	g.cancel()
	var helperErr error
	if o.options.Restores != nil {
		helperErr = o.options.Restores.End(id)
	}
	o.options.Native.CloseEvents()
	g.wg.Wait()
	o.options.Native.SetToken("")
	o.options.Native.BindGeneration(nil)
	var err error
	if o.options.Scans != nil {
		err = o.options.Scans.Interrupt()
	}
	o.mu.Lock()
	if o.current == g {
		o.current = nil
	}
	o.mu.Unlock()
	o.changed()
	return errors.Join(err, helperErr)
}

// Do cancels only the caller's wait. Once accepted, the operation uses the
// generation lifetime and a native timeout. It is never replayed by the owner.
func (o *Owner) Do(wait context.Context, fn func(context.Context) error) error {
	return o.do(wait, fn, 30*time.Second)
}

// DoAccount covers discovery and four vendor-requested login attempts.
func (o *Owner) DoAccount(wait context.Context, fn func(context.Context) error) error {
	return o.do(wait, fn, engine.AccountOperationTimeout)
}

func (o *Owner) do(wait context.Context, fn func(context.Context) error, timeout time.Duration) error {
	o.mu.RLock()
	g := o.current
	o.mu.RUnlock()
	if g == nil || g.ctx.Err() != nil {
		return engine.ErrUnavailable
	}
	op := operation{revision: o.options.Native.SessionRevision(), fn: fn, result: make(chan error, 1), timeout: timeout}
	select {
	case <-wait.Done():
		return engine.ErrUnavailable
	default:
	}
	select {
	case g.queue <- op:
	case <-g.ctx.Done():
		return engine.ErrUnavailable
	default:
		return ErrBusy
	}
	select {
	case err := <-op.result:
		return err
	case <-wait.Done():
		return engine.ErrIndeterminate
	case <-g.ctx.Done():
		return engine.ErrIndeterminate
	}
}
func (o *Owner) work(g *generation) {
	defer g.wg.Done()
	for {
		select {
		case <-g.ctx.Done():
			return
		case op := <-g.queue:
			if g.ctx.Err() != nil {
				op.result <- engine.ErrUnavailable
				continue
			}
			ctx, cancel := context.WithTimeout(g.ctx, op.timeout)
			bound, err := o.options.Native.BindSession(ctx, op.revision)
			if err == nil {
				err = op.fn(bound)
			}
			cancel()
			if err == nil && o.options.Restores != nil && o.options.Native.Authorized() {
				o.options.Restores.Wake()
			}
			op.result <- err
			o.changed()
		}
	}
}
func (o *Owner) setState(g *generation, fn func(*State)) {
	o.mu.Lock()
	if o.current == g {
		fn(&g.state)
	}
	o.mu.Unlock()
}
func (o *Owner) monitor(g *generation) {
	defer g.wg.Done()
	ticker := time.NewTicker(o.options.MonitorInterval)
	defer ticker.Stop()
	var attemptedRestore bool
	var lastTouch, lastScan time.Time
	touchFailed := false
	wasAuthorized := false
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(g.ctx, 20*time.Second)
			raw, err := o.options.Native.Call(ctx, "status", engine.OperationRequest{})
			var status struct {
				Version string `json:"productVersion"`
				Build   string `json:"buildNumber"`
			}
			ready := err == nil && json.Unmarshal(raw, &status) == nil && status.Version != "" && status.Build != ""
			if ready && ((o.options.Version != "" && status.Version != o.options.Version) || (o.options.Build != "" && status.Build != o.options.Build)) {
				cancel()
				select {
				case o.failures <- ErrVersionDrift:
				default:
				}
				return
			}
			o.setState(g, func(s *State) { s.NativeReady = ready })
			if ready && !attemptedRestore {
				err := o.DoAccount(ctx, func(bound context.Context) error { return o.options.Native.RestoreSession(bound) })
				attemptedRestore = !errors.Is(err, ErrBusy)
				if o.options.Native.Authorized() {
					err = nil
				}
				outcome := "complete"
				if err != nil {
					outcome = "needs_login"
					o.options.Logger.Warn("native_session_restore_unavailable")
				} else if !o.options.Native.Authorized() {
					// No saved session, for example after moving an existing /config.
					// Settings and archives are kept; the account needs one sign-in.
					outcome = "needs_login"
				}
				if outcome == "needs_login" {
					o.options.Logger.Warn("native_sign_in_required", "hint", "sign in once on the web Account page or with: cphctl login <username> <server> (password on stdin)")
				}
				o.setState(g, func(s *State) { s.SessionRestore = outcome })
			}
			authorized := o.options.Native.Authorized()
			if authorized && !wasAuthorized && o.options.Restores != nil {
				o.options.Restores.Wake()
			}
			wasAuthorized = authorized
			// Restore failure never prevents event delivery, token renewal or scans.
			if _, err := o.options.Native.OpenEvents(g.ctx); err != nil {
				o.setState(g, func(s *State) { s.EventsReady = false })
			}
			interval := 120 * time.Second
			if touchFailed {
				interval = 15 * time.Second
			}
			if time.Since(lastTouch) >= interval {
				err := o.options.Native.TouchSession(ctx)
				if err != nil && !touchFailed {
					o.options.Logger.Warn("native_session_keepalive_failed")
				}
				if err == nil && touchFailed {
					o.options.Logger.Info("native_session_keepalive_recovered")
				}
				touchFailed = err != nil
				lastTouch = time.Now()
			}
			if o.options.Scans != nil && time.Since(lastScan) >= 60*time.Second {
				if err := o.options.Scans.Observe(ctx); err != nil {
					o.options.Logger.Warn("scan_observation_failed")
				}
				lastScan = time.Now()
			}
			cancel()
			o.changed()
		}
	}
}
