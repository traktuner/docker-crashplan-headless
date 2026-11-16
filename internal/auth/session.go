// Package auth protects this product's web surface. It does not bypass, issue,
// or accept native CrashPlan authentication tokens.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnauthorized  = errors.New("WEB_AUTHORIZATION_REQUIRED")
	ErrCSRF          = errors.New("CSRF_REJECTED")
	ErrOrigin        = errors.New("PUBLIC_ORIGIN_MISMATCH")
	ErrRateLimited   = errors.New("WEB_LOGIN_RATE_LIMITED")
	ErrConfiguration = errors.New("INVALID_WEB_AUTH_CONFIGURATION")

	// Reported through the callback of NewPersistent; they never stop a sign-in.
	ErrNotSaved           = errors.New("WEB_SESSIONS_NOT_SAVED")
	ErrRevocationNotSaved = errors.New("WEB_SESSION_REVOCATION_NOT_SAVED")
	ErrNotRestored        = errors.New("WEB_SESSIONS_NOT_RESTORED")
)

const cookieName = "cph_session"

const (
	// A session without "keep me signed in" ends after the idle time given to
	// the manager, or after sessionAbsolute, and does not survive a restart.
	sessionAbsolute = 12 * time.Hour
	// A remembered session slides over 30 idle days and ends after 90 days.
	rememberIdle     = 30 * 24 * time.Hour
	rememberAbsolute = 90 * 24 * time.Hour
	// Sliding a remembered session forward is written to disk at most this often.
	persistEvery = 6 * time.Hour
	persistFile  = "web-sessions.json"
	maxSessions  = 32
)

type Session struct {
	ID   string `json:"-"`
	CSRF string `json:"csrf"`
	// Mode tells the web app which sign-in applies; "none" has no sign-out.
	Mode     string `json:"mode,omitempty"`
	expires  time.Time
	created  time.Time
	absolute time.Time
	// remember marks a "keep me signed in" session; only these are saved.
	remember bool
}

// Store keeps the remembered sessions private across restarts. statefile.Store
// implements it; Read reports a missing record as os.ErrNotExist.
type Store interface {
	Read(name string) ([]byte, error)
	Write(name string, raw []byte) error
}

// savedSession is one remembered session on disk. The cookie value itself is
// never stored, only a keyed hash of it, so the file cannot be used to sign in.
// Times are Unix seconds.
type savedSession struct {
	Key      string `json:"k"`
	CSRF     string `json:"c"`
	Created  int64  `json:"t"`
	Expires  int64  `json:"e"`
	Absolute int64  `json:"a"`
}

// savedSessions is the whole file. Salt and Check tie it to the web user name
// and password: Check is a keyed hash that only matches when the key derived
// from the current credentials is the one that wrote the file.
type savedSessions struct {
	Version  int            `json:"v"`
	Salt     string         `json:"n"`
	Check    string         `json:"x"`
	Sessions []savedSession `json:"s"`
}

const savedVersion = 2

func (s Session) String() string { return "auth.Session{redacted}" }

type Manager struct {
	username   [32]byte
	salt       []byte
	key        []byte
	origins    map[string]bool
	scheme     string
	ttl        time.Duration
	now        func() time.Time
	mu         sync.Mutex
	sessions   map[string]Session // keyed by sid(cookie value)
	buckets    map[string]attemptBucket
	nextHash   time.Time
	hashing    chan struct{}
	admissions chan struct{}
	// sidKey keys the session lookup hash. It is random per start, or derived
	// from the credentials when sessions are saved, so that a changed password
	// or user name ends every saved session.
	sidKey      []byte
	store       Store
	onSaveError func(error)
	persistMu   sync.Mutex
	// salt and check are written to the file; see savedSessions.
	sidSalt []byte
	check   string
	// persistedAt is the last successful save. After a failed save, dirty stays
	// set and the next sign-in, sign-out or request after retryAt tries again;
	// pendingRevocation says the failed save was about removing a session.
	persistedAt       time.Time
	dirty             bool
	retryAt           time.Time
	pendingRevocation bool
}

func (m *Manager) String() string { return "auth.Manager{redacted}" }

func canonicalOrigin(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", ErrConfiguration
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}

func newManager(username, password, publicOrigin string, ttl time.Duration) (*Manager, error) {
	if username == "" || len(username) > 256 || len(password) < 12 || len(password) > 65536 || ttl <= 0 {
		return nil, ErrConfiguration
	}
	origin, err := canonicalOrigin(publicOrigin)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, ErrConfiguration
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if err != nil {
		return nil, ErrConfiguration
	}
	sidKey := make([]byte, 32)
	if _, err := rand.Read(sidKey); err != nil {
		return nil, ErrConfiguration
	}
	return &Manager{username: sha256.Sum256([]byte(username)), salt: salt, key: key, sidKey: sidKey, scheme: strings.SplitN(origin, ":", 2)[0], ttl: ttl, now: time.Now, sessions: make(map[string]Session), buckets: make(map[string]attemptBucket), hashing: make(chan struct{}, 2), admissions: make(chan struct{}, 8)}, nil
}

// sid is the key under which a session is kept; the cookie value is a secret
// that is only ever compared through this keyed hash.
func (m *Manager) sid(id string) string {
	mac := hmac.New(sha256.New, m.sidKey)
	mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func randomID() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", ErrConfiguration
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

type attemptBucket struct {
	failures int
	window   time.Time
}

func New(username, password, origin string, ttl time.Duration) (*Manager, error) {
	return NewOrigins(username, password, []string{origin}, ttl)
}
func NewOrigins(username, password string, origins []string, ttl time.Duration) (*Manager, error) {
	if len(origins) == 0 || len(origins) > 8 {
		return nil, ErrConfiguration
	}
	m, err := newManager(username, password, origins[0], ttl)
	if err != nil {
		return nil, err
	}
	m.origins = make(map[string]bool)
	for _, value := range origins {
		origin, err := canonicalOrigin(value)
		if err != nil {
			return nil, err
		}
		m.origins[origin] = true
		if strings.HasPrefix(origin, "https:") {
			m.scheme = "https"
		}
	}
	return m, nil
}

// NewPersistent is NewOrigins that also keeps "keep me signed in" sessions in
// store, so that they survive a restart. A changed user name or password ends
// them all: the file records which credentials wrote it, and a file from other
// credentials is emptied at start, so that changing the password back does not
// bring old sessions back. A store problem never stops the sign-in; it is
// reported through onSaveError (see ErrNotSaved, ErrRevocationNotSaved and
// ErrNotRestored).
func NewPersistent(username, password string, origins []string, ttl time.Duration, store Store, onSaveError func(error)) (*Manager, error) {
	return newPersistent(username, password, origins, ttl, store, onSaveError, time.Now)
}

// newPersistent takes the clock so that tests can start a manager at any time.
func newPersistent(username, password string, origins []string, ttl time.Duration, store Store, onSaveError func(error), now func() time.Time) (*Manager, error) {
	m, err := NewOrigins(username, password, origins, ttl)
	if err != nil {
		return nil, err
	}
	m.now = now
	m.store = store
	m.onSaveError = onSaveError
	var saved savedSessions
	haveFile := false
	raw, readErr := store.Read(persistFile)
	switch {
	case errors.Is(readErr, os.ErrNotExist):
	case readErr != nil:
		m.report(fmt.Errorf("%w: %w", ErrNotRestored, readErr))
	default:
		haveFile = json.Unmarshal(raw, &saved) == nil && saved.Version == savedVersion
		if haveFile {
			salt, err := base64.RawURLEncoding.DecodeString(saved.Salt)
			haveFile = err == nil && len(salt) == 16
		}
		if !haveFile {
			m.report(fmt.Errorf("%w: %w", ErrNotRestored, ErrConfiguration))
			saved = savedSessions{}
		}
	}
	// Each installation has its own random salt, so guesses cannot be shared.
	salt := make([]byte, 16)
	if haveFile {
		salt, _ = base64.RawURLEncoding.DecodeString(saved.Salt)
	} else if _, err := rand.Read(salt); err != nil {
		return nil, ErrConfiguration
	}
	key, err := pbkdf2.Key(sha256.New, password, append(append([]byte{}, salt...), []byte("|cph-web-session-v2|"+username)...), 600000, 32)
	if err != nil {
		return nil, ErrConfiguration
	}
	m.sidKey = key
	m.sidSalt = salt
	check := hmac.New(sha256.New, key)
	check.Write([]byte("cph-web-session-check"))
	m.check = base64.RawURLEncoding.EncodeToString(check.Sum(nil))
	if !haveFile {
		return m, nil
	}
	if subtle.ConstantTimeCompare([]byte(saved.Check), []byte(m.check)) == 1 {
		m.restore(saved.Sessions)
	} else if len(saved.Sessions) > 0 {
		// Written for other credentials: end those sessions for good.
		m.save(true)
	}
	return m, nil
}

func (m *Manager) report(err error) {
	if m.onSaveError != nil {
		m.onSaveError(err)
	}
}

// restore adopts the saved sessions that have not ended. Times from the file
// are clamped to the limits a new session would get, so a wrong clock when the
// file was written cannot make a session last longer.
func (m *Manager) restore(items []savedSession) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range items {
		if len(m.sessions) >= maxSessions {
			break
		}
		created := time.Unix(item.Created, 0)
		absolute := time.Unix(item.Absolute, 0)
		expires := time.Unix(item.Expires, 0)
		if created.After(now.Add(time.Minute)) {
			continue
		}
		if limit := created.Add(rememberAbsolute); absolute.After(limit) {
			absolute = limit
		}
		if limit := now.Add(rememberIdle); expires.After(limit) {
			expires = limit
		}
		if expires.After(absolute) {
			expires = absolute
		}
		if len(item.Key) != 43 || len(item.CSRF) < 32 || len(item.CSRF) > 128 || !now.Before(expires) || !now.Before(absolute) {
			continue
		}
		m.sessions[item.Key] = Session{CSRF: item.CSRF, remember: true, created: created, expires: expires, absolute: absolute}
	}
}

// save writes the remembered sessions. It takes a fresh snapshot under the
// lock and writes outside it, one writer at a time, so the file always ends
// with the newest state. revocation says that a session was removed, which a
// failed save must not hide: the removed session would come back at the next
// restart.
func (m *Manager) save(revocation bool) {
	if m.store == nil {
		return
	}
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.Lock()
	now := m.now()
	saved := savedSessions{Version: savedVersion, Salt: base64.RawURLEncoding.EncodeToString(m.sidSalt), Check: m.check}
	for key, s := range m.sessions {
		if s.remember && now.Before(s.expires) && now.Before(s.absolute) {
			saved.Sessions = append(saved.Sessions, savedSession{Key: key, CSRF: s.CSRF, Created: s.created.Unix(), Expires: s.expires.Unix(), Absolute: s.absolute.Unix()})
		}
	}
	m.mu.Unlock()
	raw, err := json.Marshal(saved)
	if err == nil {
		err = m.store.Write(persistFile, raw)
	}
	m.mu.Lock()
	kind := ErrNotSaved
	if err != nil {
		m.pendingRevocation = m.pendingRevocation || revocation
		if m.pendingRevocation {
			kind = ErrRevocationNotSaved
		}
		m.dirty = true
		m.retryAt = m.now().Add(time.Minute)
	} else {
		m.dirty, m.pendingRevocation = false, false
		m.persistedAt = now
	}
	m.mu.Unlock()
	if err != nil {
		m.report(fmt.Errorf("%w: %w", kind, err))
	}
}

// retryDue says an earlier save failed and a minute has passed. Call with m.mu held.
func (m *Manager) retryDue(now time.Time) bool { return m.dirty && !now.Before(m.retryAt) }

func (m *Manager) Login(username, password string) (Session, error) {
	return m.LoginFrom(context.Background(), "local", username, password)
}

// The caller supplies the peer IP from RemoteAddr. Forwarded headers do not
// influence throttling. Password hashing never holds the session mutex.
func (m *Manager) LoginFrom(ctx context.Context, peer, username, password string) (Session, error) {
	return m.LoginRemember(ctx, peer, username, password, false)
}

// LoginRemember is LoginFrom; remember asks for a long-lived session that is
// kept across restarts.
func (m *Manager) LoginRemember(ctx context.Context, peer, username, password string, remember bool) (Session, error) {
	if len(password) > 65536 || len(username) > 256 || len(peer) > 256 {
		return Session{}, ErrUnauthorized
	}
	m.mu.Lock()
	now := m.now()
	for key, bucket := range m.buckets {
		if now.Sub(bucket.window) >= 30*time.Second {
			delete(m.buckets, key)
		}
	}
	bucket := m.buckets[peer]
	if len(m.buckets) >= 256 && bucket.window.IsZero() {
		oldest := ""
		var window time.Time
		for key, item := range m.buckets {
			if oldest == "" || item.window.Before(window) {
				oldest = key
				window = item.window
			}
		}
		delete(m.buckets, oldest)
	}
	if bucket.window.IsZero() {
		bucket.window = now
	}
	bucket.failures++
	m.buckets[peer] = bucket
	slot := time.Now()
	if bucket.failures > 5 {
		delay := time.Duration(min(bucket.failures-5, 10)) * 100 * time.Millisecond
		slot = slot.Add(delay)
	}
	if m.nextHash.After(slot) {
		slot = m.nextHash
	}
	delay := time.Until(slot)
	if delay > 2*time.Second {
		m.mu.Unlock()
		return Session{}, ErrRateLimited
	}
	m.nextHash = slot.Add(100 * time.Millisecond)
	m.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return Session{}, ErrRateLimited
		case <-timer.C:
		}
	}
	select {
	case m.hashing <- struct{}{}:
	case <-ctx.Done():
		return Session{}, ErrRateLimited
	}
	key, err := pbkdf2.Key(sha256.New, password, m.salt, 600000, 32)
	<-m.hashing
	if err != nil {
		return Session{}, ErrUnauthorized
	}
	user := sha256.Sum256([]byte(username))
	if subtle.ConstantTimeCompare(key, m.key) != 1 || subtle.ConstantTimeCompare(user[:], m.username[:]) != 1 {
		return Session{}, ErrUnauthorized
	}
	id, err := randomID()
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomID()
	if err != nil {
		return Session{}, err
	}
	m.mu.Lock()
	now = m.now()
	// The saved file changes when a remembered session is added or removed.
	removed := false
	for key, s := range m.sessions {
		if !now.Before(s.expires) || !now.Before(s.absolute) {
			removed = removed || s.remember
			delete(m.sessions, key)
		}
	}
	if len(m.sessions) >= maxSessions {
		oldest := ""
		var created time.Time
		for key, s := range m.sessions {
			if oldest == "" || s.created.Before(created) {
				oldest = key
				created = s.created
			}
		}
		removed = removed || m.sessions[oldest].remember
		delete(m.sessions, oldest)
	}
	idle, absolute := m.ttl, sessionAbsolute
	if remember {
		idle, absolute = rememberIdle, rememberAbsolute
	}
	session := Session{ID: id, CSRF: csrf, expires: now.Add(idle), created: now, absolute: now.Add(absolute), remember: remember}
	m.sessions[m.sid(id)] = session
	delete(m.buckets, peer)
	retry := m.retryDue(now)
	m.mu.Unlock()
	if remember || removed || retry {
		m.save(removed)
	}
	return session, nil
}

func (m *Manager) Cookie(session Session) *http.Cookie {
	cookie := &http.Cookie{Name: cookieName, Value: session.ID, Path: "/", HttpOnly: true, Secure: m.scheme == "https", SameSite: http.SameSiteStrictMode}
	// Without "keep me signed in" the cookie ends with the browser session.
	if session.remember {
		cookie.Expires = session.absolute
	}
	return cookie
}

func (m *Manager) CheckOrigin(r *http.Request) error {
	for origin := range m.origins {
		u, _ := url.Parse(origin)
		host, err := canonicalOrigin(u.Scheme + "://" + r.Host)
		if err == nil && host == origin {
			return nil
		}
	}
	return ErrOrigin
}
func (m *Manager) CheckRequestOrigin(r *http.Request) error {
	if err := m.CheckOrigin(r); err != nil {
		return err
	}
	origin, err := canonicalOrigin(r.Header.Get("Origin"))
	if err != nil || !m.origins[origin] {
		return ErrCSRF
	}
	u, _ := url.Parse(origin)
	host, err := canonicalOrigin(u.Scheme + "://" + r.Host)
	if err != nil || host != origin {
		return ErrCSRF
	}
	return nil
}

func (m *Manager) Authorize(r *http.Request, mutation bool) (Session, error) {
	return m.authorize(r, mutation, true)
}
func (m *Manager) AuthorizePassive(r *http.Request) (Session, error) {
	return m.authorize(r, false, false)
}
func (m *Manager) authorize(r *http.Request, mutation, touch bool) (Session, error) {
	if err := m.CheckOrigin(r); err != nil {
		return Session{}, err
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return Session{}, ErrUnauthorized
	}
	key := m.sid(cookie.Value)
	m.mu.Lock()
	session, ok := m.sessions[key]
	if ok && (!m.now().Before(session.expires) || !m.now().Before(session.absolute)) {
		delete(m.sessions, key)
		ok = false
	}
	m.mu.Unlock()
	if !ok {
		return Session{}, ErrUnauthorized
	}
	// Saved sessions only know the hash; callers get the cookie value back.
	session.ID = cookie.Value
	if mutation {
		if err := m.CheckRequestOrigin(r); err != nil {
			return Session{}, err
		}
		if subtle.ConstantTimeCompare([]byte(session.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
			return Session{}, ErrCSRF
		}
	}
	m.mu.Lock()
	current, stillPresent := m.sessions[key]
	saveNow, revocation := false, false
	if stillPresent && touch {
		idle := m.ttl
		if current.remember {
			idle = rememberIdle
		}
		now := m.now()
		current.expires = now.Add(idle)
		m.sessions[key] = current
		// Sliding a remembered session is saved now and then, not on every request.
		// After a failed save, the next try waits for retryAt.
		waiting := m.dirty && now.Before(m.retryAt)
		saveNow = m.retryDue(now) || (current.remember && !waiting && now.Sub(m.persistedAt) >= persistEvery)
		revocation = m.pendingRevocation
	}
	m.mu.Unlock()
	if !stillPresent {
		return Session{}, ErrUnauthorized
	}
	if saveNow {
		m.save(revocation)
	}
	return session, nil
}

func (m *Manager) Logout(id string) {
	key := m.sid(id)
	m.mu.Lock()
	removed, found := m.sessions[key]
	delete(m.sessions, key)
	m.mu.Unlock()
	if found && removed.remember {
		m.save(true)
	}
}

func (m *Manager) CookieFor(session Session, r *http.Request) *http.Cookie {
	cookie := m.Cookie(session)
	origin, err := canonicalOrigin(r.Header.Get("Origin"))
	if err == nil && m.origins[origin] {
		cookie.Secure = strings.HasPrefix(origin, "https:")
	}
	return cookie
}
func (m *Manager) AdmitLogin() (func(), error) {
	select {
	case m.admissions <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-m.admissions }) }, nil
	default:
		return nil, ErrRateLimited
	}
}
