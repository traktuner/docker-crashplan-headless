package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
)

type Provider struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (Provider) String() string     { return "engine.Provider{url:redacted}" }
func (p Provider) GoString() string { return p.String() }

type SSOState struct {
	State       string       `json:"state"`
	AssertionID string       `json:"assertionId,omitempty"`
	Username    string       `json:"username,omitempty"`
	Differs     bool         `json:"differs,omitempty"`
	Result      *LoginResult `json:"result,omitempty"`
}

// Native assertions have no attempt ID. Present the asserted identity and require
// explicit operator confirmation; never infer which browser attempt produced it.
type SSOAttempt struct {
	Providers   []Provider `json:"providers"`
	client      *Client
	info        LoginInfo
	events      <-chan json.RawMessage
	unsubscribe func()
	busDone     <-chan struct{}
	generation  any
	revision    uint64
	mu          sync.Mutex
	state       SSOState
	token       string
	failure     error
	seen        map[[32]byte]bool
	stopped     chan struct{}
	closed      bool
	watching    bool
	completing  bool
	closeOnce   sync.Once
	releaseOnce sync.Once
}

func (*SSOAttempt) String() string { return "engine.SSOAttempt{private}" }
func (c *Client) BeginSSO(ctx context.Context, in LoginInfo) (*SSOAttempt, error) {
	ctx = c.generationContext(ctx)
	if !validLogin(in.Username, in.ServerAddress) {
		return nil, ErrInvalidRequest
	}
	if !c.loginMu.TryLock() {
		return nil, ErrLoginInProgress
	}
	var err error
	in, err = c.ssoContext(ctx, in)
	if err != nil {
		c.loginMu.Unlock()
		return nil, err
	}
	bus := c.EventBus()
	events, unsubscribe, err := bus.Subscribe("SsoAuthFinalizeMessage")
	if err != nil {
		c.loginMu.Unlock()
		return nil, err
	}
	a := &SSOAttempt{client: c, info: in, events: events, unsubscribe: unsubscribe, busDone: bus.Done(), generation: ctx.Value(generationKey{}), revision: c.SessionRevision(), state: SSOState{State: "waiting"}, seen: make(map[[32]byte]bool), stopped: make(chan struct{})}
	if _, err = c.OpenEvents(ctx); err != nil {
		a.Close()
		return nil, err
	}
	body, _ := json.Marshal(struct {
		Server   string `json:"serverAddress"`
		User     string `json:"username"`
		Register bool   `json:"register"`
		Key      string `json:"regKey,omitempty"`
		OrgKey   string `json:"orgRegKey,omitempty"`
	}{in.ServerAddress, in.Username, in.Register, in.RegistrationKey, in.OrganizationKey})
	raw, err := c.request(ctx, "PUT", "/v0/SsoAuthInitiateRequest", body, false, true)
	if err != nil {
		a.Close()
		return nil, err
	}
	var wire struct {
		Errors []json.RawMessage `json:"errors"`
		URL    string            `json:"ssoLoginUrl"`
		URLs   []struct {
			Name string `json:"one"`
			URL  string `json:"two"`
		} `json:"ssoLoginUrls"`
	}
	if json.Unmarshal(raw, &wire) != nil || len(wire.Errors) > 0 {
		a.Close()
		return nil, ErrRejected
	}
	for _, p := range wire.URLs {
		a.Providers = append(a.Providers, Provider{p.Name, p.URL})
	}
	if len(a.Providers) == 0 && wire.URL != "" {
		a.Providers = []Provider{{Name: "CrashPlan", URL: wire.URL}}
	}
	if len(a.Providers) == 0 {
		a.Close()
		return nil, ErrProtocol
	}
	for _, p := range a.Providers {
		u, e := url.Parse(p.URL)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			a.Close()
			return nil, ErrProtocol
		}
	}
	return a, nil
}
func (a *SSOAttempt) release() { a.releaseOnce.Do(a.client.loginMu.Unlock) }
func (a *SSOAttempt) Close() {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.token = ""
		if a.state.State == "waiting" || a.state.State == "confirm" {
			a.state = SSOState{State: "canceled"}
		}
		release := !a.completing
		a.mu.Unlock()
		close(a.stopped)
		a.unsubscribe()
		if release {
			a.release()
		}
	})
}
func (a *SSOAttempt) Status() SSOState {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.state
	if out.Result != nil {
		r := *out.Result
		out.Result = &r
	}
	return out
}
func (a *SSOAttempt) Failure() error { a.mu.Lock(); defer a.mu.Unlock(); return a.failure }

// Watch never makes an HTTP request or changes the native session.
func (a *SSOAttempt) Watch(ctx context.Context, notify func()) {
	a.mu.Lock()
	if a.watching || a.closed {
		a.mu.Unlock()
		return
	}
	a.watching = true
	a.mu.Unlock()
	defer a.Close()
	for {
		var raw json.RawMessage
		select {
		case <-a.stopped:
			return
		case <-ctx.Done():
			a.fail(ErrUnavailable)
			return
		case <-a.busDone:
			a.fail(ErrUnavailable)
			return
		case raw = <-a.events:
		}
		var event struct {
			Username string `json:"username"`
			Token    string `json:"authToken"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Token == "" || len(event.Token) > 65536 || strings.ContainsAny(event.Token, "\r\n\x00") || !validLogin(event.Username, a.info.ServerAddress) {
			a.fail(ErrProtocol)
			return
		}
		check := ctx
		if a.generation != nil {
			check = context.WithValue(ctx, generationKey{}, a.generation)
		}
		if !a.client.currentGeneration(check) {
			a.fail(ErrUnavailable)
			return
		}
		digest := sha256.Sum256([]byte(event.Token))
		a.mu.Lock()
		if a.closed || a.state.State != "waiting" || a.seen[digest] {
			a.mu.Unlock()
			continue
		}
		if len(a.seen) >= 32 {
			a.mu.Unlock()
			a.fail(ErrProtocol)
			return
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			a.mu.Unlock()
			a.fail(ErrUnavailable)
			return
		}
		a.seen[digest] = true
		a.token = event.Token
		a.state = SSOState{State: "confirm", AssertionID: hex.EncodeToString(id[:]), Username: event.Username, Differs: event.Username != a.info.Username}
		a.mu.Unlock()
		if notify != nil {
			notify()
		}
	}
}
func (a *SSOAttempt) fail(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.completing && a.state.State != "completed" {
		a.failure = err
		a.state = SSOState{State: "lost"}
	}
}
func (a *SSOAttempt) Discard(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.state.State != "confirm" || id != a.state.AssertionID {
		return ErrInvalidRequest
	}
	a.token = ""
	a.state = SSOState{State: "waiting"}
	return nil
}
func (a *SSOAttempt) Complete(ctx context.Context, id, username string) (LoginResult, error) {
	ctx = a.client.operationContext(ctx)
	a.mu.Lock()
	if a.closed || a.completing || a.state.State == "completed" {
		a.mu.Unlock()
		return LoginResult{}, ErrAlreadyCompleted
	}
	if a.state.State != "confirm" || id != a.state.AssertionID || username != a.state.Username {
		a.mu.Unlock()
		return LoginResult{}, ErrInvalidRequest
	}
	if a.generation != ctx.Value(generationKey{}) || !a.client.currentGeneration(ctx) || a.revision != a.client.SessionRevision() {
		a.mu.Unlock()
		return LoginResult{}, ErrSessionChanged
	}
	token := a.token
	a.token = ""
	a.completing = true
	a.state = SSOState{State: "completing"}
	a.mu.Unlock()
	defer func() { a.Close(); a.release() }()
	password := make([]byte, 24)
	if _, err := rand.Read(password); err != nil {
		a.mu.Lock()
		a.failure = ErrUnavailable
		a.state = SSOState{State: "rejected"}
		a.mu.Unlock()
		return LoginResult{}, ErrUnavailable
	}
	result, err := a.client.login(ctx, Credentials{Username: username, Password: base64.RawURLEncoding.EncodeToString(password), ServerAddress: a.info.ServerAddress, RegistrationKey: a.info.RegistrationKey, Register: a.info.Register, FirstName: a.info.FirstName, LastName: a.info.LastName, SSOToken: token})
	a.mu.Lock()
	a.failure = err
	a.state = SSOState{State: "completed", Result: &result}
	if err != nil {
		a.state = SSOState{State: "rejected"}
	}
	a.mu.Unlock()
	return result, err
}
