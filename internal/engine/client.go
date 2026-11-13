// Package engine implements the private, loopback-only native CrashPlan boundary.
// It never logs vendor request or response bodies, credentials, or native tokens.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/traktuner/crashplan-headless/internal/statefile"
)

var (
	ErrInvalidRequest = errors.New("INVALID_REQUEST")
	ErrUnauthorized   = errors.New("NATIVE_AUTHORIZATION_REQUIRED")
	ErrIndeterminate  = errors.New("NATIVE_OPERATION_INDETERMINATE")
	ErrUnavailable    = errors.New("NATIVE_ENGINE_UNAVAILABLE")
	ErrProtocol       = errors.New("NATIVE_PROTOCOL_MISMATCH")
	ErrRejected       = errors.New("NATIVE_OPERATION_REJECTED")
	ErrNotDispatched  = errors.New("NATIVE_OPERATION_NOT_DISPATCHED")
	ErrLegacyPending  = errors.New("NATIVE_RESPONSE_PENDING")
	ErrScanJournal    = errors.New("SCAN_JOURNAL_UNAVAILABLE")
)

// nativeStatusError preserves only status metadata, never the response body.
type nativeStatusError struct{ status int }

func (*nativeStatusError) Error() string { return "NATIVE_HTTP_STATUS" }

// Client owns a native HTTP session. It is independent of the web login session.
// Native tokens stay inside this process and are cleared after a native 401.
type Client struct {
	base            string
	http            *http.Client
	mu              sync.RWMutex
	token           string
	revision        uint64
	loginMu         sync.Mutex
	eventsMu        sync.Mutex
	events          *Events
	legacy          map[string]*legacyLane
	contentsMu      sync.Mutex
	contents        *contentsTracker
	store           *statefile.Store
	lifetime        context.Context
	migration       *MigrationState
	accountTimeout  time.Duration
	archiveKeys     map[string]string
	searchMu        sync.Mutex
	searchUncertain bool
}

func (c *Client) String() string   { return "engine.Client{session:redacted}" }
func (c *Client) GoString() string { return c.String() }

// NewClient accepts literal loopback HTTPS addresses only. The caller supplies a
// transport that trusts the native engine's certificate. Redirects are forbidden.
func NewClient(base string, transport *http.Client, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrInvalidRequest
	}
	ip := net.ParseIP(u.Hostname())
	port, e := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || e != nil || port < 1 || port > 65535 || transport == nil || timeout <= 0 {
		return nil, ErrInvalidRequest
	}
	hc := *transport
	hc.Timeout = timeout
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: base, http: &hc, accountTimeout: AccountRequestTimeout}, nil
}

// SetAccountTimeout bounds the vendor's account requests separately from
// routine status calls. Its legacy login handler can wait for 180 seconds.
func (c *Client) SetAccountTimeout(timeout time.Duration) error {
	if timeout <= 0 || timeout > 5*time.Minute {
		return ErrInvalidRequest
	}
	c.mu.Lock()
	c.accountTimeout = timeout
	c.mu.Unlock()
	return nil
}

func (c *Client) SetToken(token string) error {
	if len(token) > 512 || strings.ContainsAny(token, "\r\n\t ") {
		return ErrInvalidRequest
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revision++
	c.token = token
	c.migration = nil
	c.archiveKeys = nil
	return nil
}

func (c *Client) Authorized() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token != ""
}

// BindGeneration is called before exposing a newly spawned engine to controls.
// Every mutation then uses this lifetime, independent of its waiting browser.
type generationKey struct{}
type generationIdentity struct{ marker byte }

func (c *Client) BindGeneration(ctx context.Context) context.Context {
	c.resetContents()
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx != nil {
		ctx = context.WithValue(ctx, generationKey{}, &generationIdentity{1})
	}
	c.revision++
	c.lifetime = ctx
	c.migration = nil
	c.archiveKeys = nil
	c.searchUncertain = false
	c.legacy = nil
	return ctx
}
func (c *Client) generationContext(fallback context.Context) context.Context {
	c.mu.RLock()
	ctx := fallback
	if fallback.Value(generationKey{}) == nil && c.lifetime != nil {
		ctx = c.lifetime
	}
	c.mu.RUnlock()
	if binding := fallback.Value(sessionKey{}); binding != nil {
		ctx = context.WithValue(ctx, sessionKey{}, binding)
	}
	return c.operationContext(ctx)
}

func (c *Client) acceptToken(ctx context.Context, token string) error {
	if len(token) == 0 || len(token) > 512 || strings.ContainsAny(token, "\r\n\t \x00") {
		return ErrProtocol
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if marker := ctx.Value(generationKey{}); marker != nil && (ctx.Err() != nil || c.lifetime == nil || c.lifetime.Value(generationKey{}) != marker) {
		return ErrIndeterminate
	}
	if !c.sessionCurrentLocked(ctx) {
		return errors.Join(ErrIndeterminate, ErrSessionChanged)
	}
	c.revision++
	c.archiveKeys = nil
	c.migration = nil
	c.token = token
	if binding, ok := ctx.Value(sessionKey{}).(*sessionBinding); ok {
		binding.revision = c.revision
		binding.token = token
		binding.identity = nil
		binding.selfTransition = ""
	}
	return nil
}

func (c *Client) currentGeneration(ctx context.Context) bool {
	marker := ctx.Value(generationKey{})
	if marker == nil {
		return true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ctx.Err() == nil && c.lifetime != nil && c.lifetime.Value(generationKey{}) == marker
}

func (c *Client) nativeToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

func validID(id string) bool {
	v, err := strconv.ParseInt(id, 10, 64)
	return err == nil && v > 0 && strconv.FormatInt(v, 10) == id
}

// CheckScan validates the local request and session without opening source
// roots or making a native request. Scan checks again immediately on dispatch.
func (c *Client) CheckScan(setID string) error {
	if !validID(setID) {
		return ErrInvalidRequest
	}
	if !c.Authorized() {
		return ErrUnauthorized
	}
	return nil
}

// Scan requests a normal metadata/deletion scan for one selected backup set.
// Acceptance is not scan completion. No failure is automatically replayed.
func (c *Client) Scan(ctx context.Context, setID string) error {
	if err := c.CheckScan(setID); err != nil {
		return err
	}
	return c.namedCommand(ctx, "backup.scan", "backup.scan true "+setID)
}

func (c *Client) namedCommand(ctx context.Context, name, command string) error {
	body, _ := json.Marshal(struct {
		Command string `json:"command"`
	}{command})
	raw, err := c.request(ctx, "POST", "/v0/Command", body, true, true)
	if err != nil {
		return err
	}
	var response struct {
		Errors  []json.RawMessage `json:"errors"`
		Results []struct {
			Command struct {
				Name string `json:"name"`
			} `json:"command"`
			Error json.RawMessage `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || len(response.Errors) != 0 || len(response.Results) != 1 {
		return ErrIndeterminate
	}
	result := response.Results[0]
	if len(result.Error) > 0 && string(result.Error) != "null" {
		return errors.Join(ErrIndeterminate, ErrRejected)
	}
	if result.Command.Name != name {
		return ErrIndeterminate
	}
	return nil
}

// request accepts only internal paths chosen by application operations. It does
// not implement retries. Response bodies remain private to the calling parser.
func (c *Client) request(ctx context.Context, method, path string, body []byte, auth, mutation bool) ([]byte, error) {
	ctx = c.operationContext(ctx)
	client := c.http
	deadline := 30 * time.Second
	account := path == "/v0/LoginRequest" || path == "/v0/SsoAuthInitiateRequest" || path == "/v1/LoginSetup" || strings.HasPrefix(path, "/v1/LoginConfiguration?") || path == "/v1/DeviceMigrationSelfService" || path == "/v1/DeviceMigrationAuthorized"
	if account {
		c.mu.RLock()
		deadline = c.accountTimeout
		c.mu.RUnlock()
		copy := *c.http
		copy.Timeout = deadline
		client = &copy
	}
	if mutation {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(c.generationContext(ctx), deadline)
		defer cancel()
	}
	if !c.currentGeneration(ctx) {
		return nil, ErrUnavailable
	}
	c.mu.RLock()
	if !c.sessionCurrentLocked(ctx) {
		c.mu.RUnlock()
		return nil, errors.Join(ErrNotDispatched, ErrSessionChanged)
	}
	token := c.token
	c.mu.RUnlock()
	if auth && token == "" {
		return nil, ErrUnauthorized
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "token "+token)
	}
	if mutation {
		if err := c.checkIdentity(ctx); err != nil {
			return nil, errors.Join(ErrNotDispatched, err)
		}
	}
	response, err := client.Do(req)
	if err != nil {
		if mutation {
			var connectionError *net.OpError
			if errors.As(err, &connectionError) && connectionError.Op == "dial" {
				return nil, errors.Join(ErrNotDispatched, ErrUnavailable)
			}
			return nil, ErrIndeterminate
		}
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if !c.currentGeneration(ctx) || !c.sessionCurrent(ctx) {
		if mutation {
			return nil, errors.Join(ErrIndeterminate, ErrSessionChanged)
		}
		return nil, errors.Join(ErrUnavailable, ErrSessionChanged)
	}
	if response.StatusCode == http.StatusUnauthorized {
		if !auth {
			return nil, ErrProtocol
		}
		// Do not clear a newer login token installed during this request.
		c.mu.Lock()
		if c.token == token && c.sessionCurrentLocked(ctx) {
			c.revision++
			c.token = ""
			c.archiveKeys = nil
			c.migration = nil
		}
		c.mu.Unlock()
		return nil, ErrUnauthorized
	}
	const limit = 8 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(raw) > limit {
		if mutation {
			return nil, ErrIndeterminate
		}
		return nil, ErrProtocol
	}
	if !c.currentGeneration(ctx) || !c.sessionCurrent(ctx) {
		if mutation {
			return nil, errors.Join(ErrIndeterminate, ErrSessionChanged)
		}
		return nil, errors.Join(ErrUnavailable, ErrSessionChanged)
	}
	if path != "/v0/GetComputers" {
		if err := c.checkIdentity(ctx); err != nil {
			if mutation {
				return nil, errors.Join(ErrIndeterminate, err)
			}
			return nil, err
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Business errors use non-2xx statuses. Keep the bounded body private to
		// the operation parser; the status alone cannot prove no side effect.
		if mutation {
			return raw, ErrIndeterminate
		}
		return raw, errors.Join(ErrUnavailable, &nativeStatusError{status: response.StatusCode})
	}
	return raw, nil
}
