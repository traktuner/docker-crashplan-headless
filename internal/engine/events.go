package engine

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"strings"
	"sync"
	"time"
)

// Events is the generation's persistent subscription hub. Connections may
// change; critical subscriptions exist before dial and survive transport loss.
// Payloads stay private and must never be copied directly into browser SSE.
type Events struct {
	connectionMu     sync.Mutex
	mu               sync.Mutex
	conn             *websocket.Conn
	connectionCancel context.CancelFunc
	connectionDone   chan struct{}
	closed           bool
	epoch            uint64
	subs             map[*eventSubscription]struct{}
	stop             chan struct{}
}
type eventSubscription struct {
	kind     string
	ch       chan json.RawMessage
	stop     chan struct{}
	critical bool
}

func (e *Events) Epoch() uint64         { e.mu.Lock(); defer e.mu.Unlock(); return e.epoch }
func (e *Events) String() string        { return "engine.Events{private}" }
func (e *Events) Done() <-chan struct{} { return e.stop }
func (e *Events) Connected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conn != nil && !e.closed
}
func (c *Client) EventBus() *Events {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	if c.events == nil {
		c.events = &Events{subs: make(map[*eventSubscription]struct{}), stop: make(chan struct{})}
	}
	return c.events
}
func (c *Client) OpenEvents(ctx context.Context) (*Events, error) {
	ctx = c.generationContext(ctx)
	if !c.currentGeneration(ctx) {
		return nil, ErrUnavailable
	}
	bus := c.EventBus()
	c.mu.RLock()
	life := c.lifetime
	c.mu.RUnlock()
	if life == nil {
		life = ctx
	}
	if err := bus.connect(life, c); err != nil {
		return nil, err
	}
	return bus, nil
}
func (e *Events) connect(ctx context.Context, c *Client) error {
	e.connectionMu.Lock()
	defer e.connectionMu.Unlock()
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return ErrUnavailable
	}
	if e.conn != nil {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()
	conn, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(c.base, "https")+"/v0/wsevent", &websocket.DialOptions{HTTPClient: c.http})
	if err != nil {
		return ErrUnavailable
	}
	conn.SetReadLimit(8 << 20)
	life, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		cancel()
		conn.CloseNow()
		return ErrUnavailable
	}
	e.conn = conn
	e.epoch++
	e.connectionCancel = cancel
	done := make(chan struct{})
	e.connectionDone = done
	e.mu.Unlock()
	if err := e.send(life, map[string]any{"KeepAliveMessage": struct{}{}}); err != nil {
		cancel()
		conn.CloseNow()
		e.mu.Lock()
		e.conn = nil
		e.mu.Unlock()
		close(done)
		return err
	}
	if token := c.nativeToken(); token != "" {
		if err := e.authenticate(token); err != nil {
			cancel()
			conn.CloseNow()
			e.mu.Lock()
			e.conn = nil
			e.mu.Unlock()
			close(done)
			return err
		}
	}
	go e.read(life, conn, cancel, done)
	go e.keepAlive(life)
	return nil
}
func (c *Client) CloseEvents() {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	if c.events == nil {
		return
	}
	e := c.events
	e.connectionMu.Lock()
	e.mu.Lock()
	e.closed = true
	close(e.stop)
	cancel, conn, done := e.connectionCancel, e.conn, e.connectionDone
	for sub := range e.subs {
		close(sub.stop)
		delete(e.subs, sub)
	}
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		conn.CloseNow()
	}
	if done != nil {
		<-done
	}
	e.connectionMu.Unlock()
	c.events = nil
}
func (c *Client) authenticateEvents(ctx context.Context, token string) error {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	if !c.currentGeneration(ctx) {
		return ErrUnavailable
	}
	e := c.events
	if e == nil {
		return nil
	}
	return e.authenticate(token)
}
func (e *Events) authenticate(token string) error {
	return e.send(context.Background(), map[string]any{"LoginTokenRequestMessage": map[string]string{"token": token}})
}
func (e *Events) send(parent context.Context, message any) error {
	e.mu.Lock()
	conn := e.conn
	closed := e.closed
	e.mu.Unlock()
	if conn == nil || closed {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	if wsjson.Write(ctx, conn, message) != nil {
		conn.CloseNow()
		return ErrUnavailable
	}
	return nil
}
func (e *Events) Subscribe(kind string) (<-chan json.RawMessage, func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, nil, ErrUnavailable
	}
	sub := &eventSubscription{kind: kind, ch: make(chan json.RawMessage, 32), stop: make(chan struct{}), critical: kind == "LaunchRestoreToolMessage" || kind == "SsoAuthFinalizeMessage" || kind == "SearchFileVersionsResultMessage" || kind == "GetFileContentsResultMessage"}
	e.subs[sub] = struct{}{}
	var once sync.Once
	return sub.ch, func() {
		once.Do(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			if _, ok := e.subs[sub]; ok {
				delete(e.subs, sub)
				close(sub.stop)
			}
		})
	}, nil
}
func (e *Events) read(ctx context.Context, conn *websocket.Conn, cancel context.CancelFunc, done chan struct{}) {
	defer func() {
		cancel()
		conn.CloseNow()
		e.mu.Lock()
		if e.conn == conn {
			e.conn = nil
		}
		e.mu.Unlock()
		close(done)
	}()
	for {
		var message map[string]json.RawMessage
		if wsjson.Read(ctx, conn, &message) != nil || message == nil {
			return
		}
		e.mu.Lock()
		subscriptions := make([]*eventSubscription, 0, len(e.subs))
		for sub := range e.subs {
			subscriptions = append(subscriptions, sub)
		}
		e.mu.Unlock()
		for _, sub := range subscriptions {
			raw, ok := message[sub.kind]
			if !ok {
				continue
			}
			copy := append(json.RawMessage(nil), raw...)
			if sub.critical {
				select {
				case sub.ch <- copy:
				case <-sub.stop:
				case <-ctx.Done():
					return
				}
			} else {
				select {
				case sub.ch <- copy:
				default:
					select {
					case <-sub.ch:
					default:
					}
					select {
					case sub.ch <- copy:
					default:
					}
				}
			}
		}
	}
}
func (e *Events) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if e.send(ctx, map[string]any{"KeepAliveMessage": struct{}{}}) != nil {
				return
			}
		}
	}
}
