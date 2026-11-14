// Package lifecycle owns native control for each JVM generation.
package lifecycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/traktuner/crashplan-headless/internal/engine"
)

var ErrBusy = errors.New("CONTROL_BUSY")
var ErrVersionDrift = errors.New("ENGINE_VERSION_DRIFT")

type RestoreGenerations interface {
	Begin(context.Context, uint64) error
	End(uint64) error
	Wake()
}

// RestoreStats receives live restore statistics events of the current
// generation. Reset is called when a generation begins.
type RestoreStats interface {
	ResetRestoreStats()
	ObserveRestoreStats(raw []byte)
}

type Options struct {
	Restores        RestoreGenerations
	RestoreStats    RestoreStats
	Native          *engine.Client
	Scans           *engine.Scans
	Logger          *slog.Logger
	MonitorInterval time.Duration
	// RestoreRetry is the first delay before a failed automatic session
	// restore is tried again; it doubles up to ten minutes. Default 15 s.
	RestoreRetry   time.Duration
	Version, Build string
}
type State struct {
	Generation       string `json:"generation"`
	NativeReady      bool   `json:"nativeReady"`
	NativeSession    bool   `json:"nativeSession"`
	EventsReady      bool   `json:"eventsReady"`
	SessionRestore   string `json:"sessionRestore"`
	ScanJournalReady bool   `json:"scanJournalReady"`
	// Lanes lists native response classes whose answers are unresolved, as
	// {class: "uncertain"}. Empty when all classes are usable.
	Lanes map[string]string `json:"lanes"`
}

// Operation states: a queued operation whose waiter leaves before the worker
// starts it is dropped, so it can never run later behind the waiter's back.
const (
	opQueued int32 = iota
	opStarted
	opAbandoned
)

type operation struct {
	fn       func(context.Context) error
	result   chan error
	timeout  time.Duration
	revision uint64
	state    *atomic.Int32
}
type generation struct {
	id     uint64
	ctx    context.Context
	cancel context.CancelFunc
	queue  chan operation
	kick   chan struct{}
	wg     sync.WaitGroup
	state  State
}

// notifyEvents are engine event kinds that invalidate browser data. Bodies
// never reach the browser; the SSE stream carries a bare "update".
var notifyEvents = []string{
	"ApplicationStatusMessage", "BackupStatsSummaryMessage", "BackupStatsMessage",
	"BackupStartedMessage", "BackupStoppedMessage", "BackupCompletedMessage",
	"ScanStatsMessage", "AlertListMessage",
	"RestoreJobsResponseMessage", "RestoreStatsMessage", "RestoreStartedMessage", "RestoreStoppedMessage",
	"ComputersMessage", "ComputerConnectedMessage", "ComputerDisconnectedMessage", "TargetComputerUsagesMessage",
}

// notifySpacing is the minimum gap between event-driven browser updates.
const notifySpacing = time.Second

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
	if options.RestoreRetry == 0 {
		options.RestoreRetry = 15 * time.Second
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
		return State{SessionRestore: "not_running", ScanJournalReady: o.options.Scans != nil, Lanes: map[string]string{}}
	}
	state := o.current.state
	state.NativeSession = o.options.Native.Authorized()
	state.EventsReady = o.options.Native.EventBus().Connected()
	state.Lanes = o.options.Native.UncertainLanes()
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
	g := &generation{id: id, ctx: ctx, cancel: cancel, queue: make(chan operation, 16), kick: make(chan struct{}, 1), state: State{Generation: label, SessionRestore: "pending", ScanJournalReady: o.options.Scans != nil}}
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
	if o.options.RestoreStats != nil {
		o.options.RestoreStats.ResetRestoreStats()
	}
	// Register subscriptions before the first dial. UI invalidations coalesce:
	// at most one event-driven update per notifySpacing.
	bus := o.options.Native.EventBus()
	for _, kind := range notifyEvents {
		ch, unsubscribe, err := bus.Subscribe(kind)
		if err != nil {
			continue
		}
		stats := o.options.RestoreStats
		if kind != "RestoreStatsMessage" && kind != "RestoreStoppedMessage" {
			stats = nil
		}
		// Connection changes must not wait for the cached target list.
		targets := strings.HasPrefix(kind, "Computer")
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			defer unsubscribe()
			for {
				select {
				case <-ctx.Done():
					return
				case raw := <-ch:
					if stats != nil {
						stats.ObserveRestoreStats(raw)
					}
					if targets {
						o.options.Native.InvalidateTargets()
					}
					select {
					case g.kick <- struct{}{}:
					default:
					}
				}
			}
		}()
	}
	g.wg.Add(3)
	go o.notifier(g)
	go o.work(g)
	go o.monitor(g)
	return nil
}

// notifier turns event kicks into browser updates, spaced by notifySpacing.
func (o *Owner) notifier(g *generation) {
	defer g.wg.Done()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-g.kick:
			o.changed()
			timer := time.NewTimer(notifySpacing)
			select {
			case <-g.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
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
	op := operation{revision: o.options.Native.SessionRevision(), fn: fn, result: make(chan error, 1), timeout: timeout, state: new(atomic.Int32)}
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
	case <-g.ctx.Done():
	}
	if op.state.CompareAndSwap(opQueued, opAbandoned) {
		// Never started: the worker drops it, so nothing was sent.
		return errors.Join(engine.ErrNotDispatched, engine.ErrUnavailable)
	}
	return engine.ErrIndeterminate
}
func (o *Owner) work(g *generation) {
	defer g.wg.Done()
	for {
		select {
		case <-g.ctx.Done():
			return
		case op := <-g.queue:
			if !op.state.CompareAndSwap(opQueued, opStarted) {
				// The waiter left before the start; it was told nothing ran.
				continue
			}
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
	// Session restore: tried at the first ready tick, then again with backoff
	// while it fails transiently and nobody signed in. A definite answer (no
	// saved session, or the engine says it is not eligible) ends the attempts.
	restoreDone := false
	var nextRestore time.Time
	retry := o.options.RestoreRetry
	var lastTouch, lastScan time.Time
	touchFailed := false
	wasAuthorized := false
	// Browser updates only when something the browser shows changed.
	var last struct {
		ready, authorized, events bool
		restore                   string
		lanes                     int
	}
	first := true
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
			if ready && !restoreDone && !o.options.Native.Authorized() && !time.Now().Before(nextRestore) {
				err := o.DoAccount(ctx, func(bound context.Context) error { return o.options.Native.RestoreSession(bound) })
				if o.options.Native.Authorized() {
					err = nil
				}
				outcome := "complete"
				switch {
				case errors.Is(err, ErrBusy) || errors.Is(err, engine.ErrLoginInProgress):
					// Another account operation runs; try again next tick.
					outcome = ""
				case err != nil:
					outcome = "needs_login"
					o.options.Logger.Warn("native_session_restore_unavailable", "retry_in_s", int(retry/time.Second))
					nextRestore = time.Now().Add(retry)
					retry = min(retry*2, 10*time.Minute)
				case !o.options.Native.Authorized():
					// No usable saved session, for example after moving an existing
					// /config. Settings and archives are kept; the account needs one sign-in.
					outcome = "needs_login"
					restoreDone = true
					o.options.Logger.Warn("native_sign_in_required", "hint", "sign in once on the web Account page or with: cphctl login <username> <server> (password on stdin)")
				default:
					restoreDone = true
				}
				if outcome != "" {
					o.setState(g, func(s *State) { s.SessionRestore = outcome })
				}
			}
			authorized := o.options.Native.Authorized()
			if authorized && !wasAuthorized {
				// A sign-in ends automatic restore attempts for this generation.
				restoreDone = true
				o.setState(g, func(s *State) {
					if s.SessionRestore == "needs_login" || s.SessionRestore == "pending" {
						s.SessionRestore = "complete"
					}
				})
				if o.options.Restores != nil {
					o.options.Restores.Wake()
				}
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
			snapshot := o.Snapshot()
			if first || snapshot.NativeReady != last.ready || snapshot.NativeSession != last.authorized || snapshot.EventsReady != last.events || snapshot.SessionRestore != last.restore || len(snapshot.Lanes) != last.lanes {
				o.changed()
			}
			first = false
			last.ready, last.authorized, last.events = snapshot.NativeReady, snapshot.NativeSession, snapshot.EventsReady
			last.restore, last.lanes = snapshot.SessionRestore, len(snapshot.Lanes)
		}
	}
}
