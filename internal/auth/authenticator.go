package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
)

// Authenticator decides who may use the web API. The password sign-in is one
// implementation; further ones (trusted proxy header, OIDC, ...) implement the
// same interface and are selected in cmd/cphd by CPH_AUTH_MODE.
//
// Every implementation must keep the origin checks: they protect against other
// web sites driving the API from the operator's browser, whatever proves who
// the operator is.
type Authenticator interface {
	// Mode is the CPH_AUTH_MODE value that selects this implementation.
	Mode() string
	// CheckOrigin rejects requests whose Host is not a configured public origin.
	CheckOrigin(r *http.Request) error
	// Authorize admits a request. Mutations also need a matching Origin header
	// and CSRF token. It may extend an idle timeout.
	Authorize(r *http.Request, mutation bool) (Session, error)
	// AuthorizePassive is Authorize for a read that must not extend an idle timeout.
	AuthorizePassive(r *http.Request) (Session, error)
}

// PasswordLogin is implemented by authenticators that sign users in with a
// form and keep a cookie session. The server only mounts the sign-in route
// for these.
type PasswordLogin interface {
	Authenticator
	CheckRequestOrigin(r *http.Request) error
	AdmitLogin() (func(), error)
	// LoginRemember signs in; remember asks for a long-lived session.
	LoginRemember(ctx context.Context, peer, username, password string, remember bool) (Session, error)
	CookieFor(session Session, r *http.Request) *http.Cookie
	Logout(id string)
}

// Mode names.
const (
	ModePassword = "password"
	ModeNone     = "none"
)

// Mode implements Authenticator.
func (m *Manager) Mode() string { return ModePassword }

// Open admits every request that reaches an allowed public origin. It is for
// installations where something in front already controls access (VPN,
// authenticating reverse proxy, private network). It still enforces the
// origin and CSRF checks so that other web sites cannot drive the API.
type Open struct {
	origins map[string]bool
	csrf    string
}

// NewOpen returns the authenticator for CPH_AUTH_MODE=none.
func NewOpen(origins []string) (*Open, error) {
	if len(origins) == 0 || len(origins) > 8 {
		return nil, ErrConfiguration
	}
	token, err := randomID()
	if err != nil {
		return nil, err
	}
	o := &Open{origins: make(map[string]bool), csrf: token}
	for _, value := range origins {
		origin, err := canonicalOrigin(value)
		if err != nil {
			return nil, err
		}
		o.origins[origin] = true
	}
	return o, nil
}

func (o *Open) String() string { return "auth.Open{}" }

// Mode implements Authenticator.
func (o *Open) Mode() string { return ModeNone }

// CheckOrigin implements Authenticator.
func (o *Open) CheckOrigin(r *http.Request) error {
	for origin := range o.origins {
		u, _ := url.Parse(origin)
		host, err := canonicalOrigin(u.Scheme + "://" + r.Host)
		if err == nil && host == origin {
			return nil
		}
	}
	return ErrOrigin
}

func (o *Open) checkRequestOrigin(r *http.Request) error {
	origin, err := canonicalOrigin(r.Header.Get("Origin"))
	if err != nil || !o.origins[origin] {
		return ErrCSRF
	}
	u, _ := url.Parse(origin)
	host, err := canonicalOrigin(u.Scheme + "://" + r.Host)
	if err != nil || host != origin {
		return ErrCSRF
	}
	return nil
}

// Authorize implements Authenticator.
func (o *Open) Authorize(r *http.Request, mutation bool) (Session, error) {
	if err := o.CheckOrigin(r); err != nil {
		return Session{}, err
	}
	// Without a sign-in cookie nothing else tells apart a request that another
	// web page makes in the operator's browser. Browsers label those requests,
	// so refuse them for reads too; same-origin use and typed addresses pass.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return Session{}, ErrCSRF
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if err := o.checkRequestOrigin(r); err != nil {
			return Session{}, err
		}
	}
	session := Session{ID: "open", CSRF: o.csrf, Mode: ModeNone}
	if mutation {
		if err := o.checkRequestOrigin(r); err != nil {
			return Session{}, err
		}
		if subtle.ConstantTimeCompare([]byte(o.csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
			return Session{}, ErrCSRF
		}
	}
	return session, nil
}

// AuthorizePassive implements Authenticator.
func (o *Open) AuthorizePassive(r *http.Request) (Session, error) { return o.Authorize(r, false) }
