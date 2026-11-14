// Package auth protects this product's web surface. It does not bypass, issue,
// or accept native CrashPlan authentication tokens.
package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
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
)

const cookieName = "cph_session"

type Session struct {
	ID       string `json:"-"`
	CSRF     string `json:"csrf"`
	expires  time.Time
	created  time.Time
	absolute time.Time
}

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
	sessions   map[string]Session
	buckets    map[string]attemptBucket
	nextHash   time.Time
	hashing    chan struct{}
	admissions chan struct{}
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
	return &Manager{username: sha256.Sum256([]byte(username)), salt: salt, key: key, scheme: strings.SplitN(origin, ":", 2)[0], ttl: ttl, now: time.Now, sessions: make(map[string]Session), buckets: make(map[string]attemptBucket), hashing: make(chan struct{}, 2), admissions: make(chan struct{}, 8)}, nil
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
func (m *Manager) Login(username, password string) (Session, error) {
	return m.LoginFrom(context.Background(), "local", username, password)
}

// The caller supplies the peer IP from RemoteAddr. Forwarded headers do not
// influence throttling. Password hashing never holds the session mutex.
func (m *Manager) LoginFrom(ctx context.Context, peer, username, password string) (Session, error) {
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
	defer m.mu.Unlock()
	now = m.now()
	for id, s := range m.sessions {
		if !now.Before(s.expires) || !now.Before(s.absolute) {
			delete(m.sessions, id)
		}
	}
	if len(m.sessions) >= 32 {
		oldest := ""
		var created time.Time
		for id, s := range m.sessions {
			if oldest == "" || s.created.Before(created) {
				oldest = id
				created = s.created
			}
		}
		delete(m.sessions, oldest)
	}
	session := Session{ID: id, CSRF: csrf, expires: now.Add(m.ttl), created: now, absolute: now.Add(12 * time.Hour)}
	m.sessions[id] = session
	delete(m.buckets, peer)
	return session, nil
}

func (m *Manager) Cookie(session Session) *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: session.ID, Path: "/", HttpOnly: true, Secure: m.scheme == "https", SameSite: http.SameSiteStrictMode, Expires: session.absolute}
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
	m.mu.Lock()
	session, ok := m.sessions[cookie.Value]
	if ok && (!m.now().Before(session.expires) || !m.now().Before(session.absolute)) {
		delete(m.sessions, cookie.Value)
		ok = false
	}
	m.mu.Unlock()
	if !ok {
		return Session{}, ErrUnauthorized
	}
	if mutation {
		if err := m.CheckRequestOrigin(r); err != nil {
			return Session{}, err
		}
		if subtle.ConstantTimeCompare([]byte(session.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
			return Session{}, ErrCSRF
		}
	}
	m.mu.Lock()
	current, stillPresent := m.sessions[session.ID]
	if stillPresent && touch {
		current.expires = m.now().Add(m.ttl)
		m.sessions[session.ID] = current
	}
	m.mu.Unlock()
	if !stillPresent {
		return Session{}, ErrUnauthorized
	}
	return session, nil
}

func (m *Manager) Logout(id string) { m.mu.Lock(); delete(m.sessions, id); m.mu.Unlock() }

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
