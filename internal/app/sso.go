package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"net/http"
	"time"
)

type ssoOperation struct {
	owner   string
	expires time.Time
	cancel  context.CancelFunc
	attempt *engine.SSOAttempt
}

func (s *Server) beginSSO(w http.ResponseWriter, r *http.Request, principal auth.Session) error {
	var info engine.LoginInfo
	if err := decode(w, r, &info); err != nil {
		return err
	}
	s.ssoMu.Lock()
	for key, op := range s.sso {
		if time.Now().After(op.expires) && op.attempt.Status().State != "completing" {
			op.cancel()
			op.attempt.Close()
			delete(s.sso, key)
		}
	}
	full := len(s.sso) >= 8
	s.ssoMu.Unlock()
	if full {
		return engine.ErrLoginInProgress
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return engine.ErrUnavailable
	}
	id := hex.EncodeToString(random[:])
	lifetime, cancel := context.WithTimeout(s.options.Context, 10*time.Minute)
	var attempt *engine.SSOAttempt
	err := s.accountMutation(r.Context(), func(ctx context.Context) error {
		var err error
		attempt, err = s.native.BeginSSO(ctx, info)
		if err != nil {
			return err
		}
		op := &ssoOperation{owner: principal.ID, expires: time.Now().Add(10 * time.Minute), cancel: cancel, attempt: attempt}
		s.ssoMu.Lock()
		s.sso[id] = op
		s.ssoMu.Unlock()
		go func() { attempt.Watch(lifetime, s.Notify); s.Notify() }()
		return nil
	})
	if err != nil {
		cancel()
		return err
	}
	writeData(w, 202, map[string]any{"id": id, "providers": attempt.Providers})
	return nil
}
func (s *Server) ownedSSO(id string, principal auth.Session) (*ssoOperation, error) {
	s.ssoMu.Lock()
	defer s.ssoMu.Unlock()
	op, ok := s.sso[id]
	if !ok || op.owner != principal.ID {
		return nil, engine.ErrInvalidRequest
	}
	return op, nil
}
func (s *Server) readSSO(w http.ResponseWriter, r *http.Request, principal auth.Session) error {
	op, err := s.ownedSSO(r.PathValue("id"), principal)
	if err != nil {
		return err
	}
	if err := op.attempt.Failure(); err != nil {
		return err
	}
	writeData(w, 200, op.attempt.Status())
	return nil
}
func (s *Server) confirmSSO(w http.ResponseWriter, r *http.Request, principal auth.Session) error {
	var in struct {
		AssertionID string `json:"assertionId"`
		Username    string `json:"username"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	op, err := s.ownedSSO(r.PathValue("id"), principal)
	if err != nil {
		return err
	}
	var result engine.LoginResult
	err = s.accountMutation(r.Context(), func(ctx context.Context) error {
		var err error
		result, err = op.attempt.Complete(ctx, in.AssertionID, in.Username)
		return err
	})
	if err != nil {
		return err
	}
	writeData(w, 200, result)
	s.Notify()
	return nil
}
func (s *Server) discardSSO(w http.ResponseWriter, r *http.Request, principal auth.Session) error {
	var in struct {
		AssertionID string `json:"assertionId"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	op, err := s.ownedSSO(r.PathValue("id"), principal)
	if err != nil {
		return err
	}
	if err := op.attempt.Discard(in.AssertionID); err != nil {
		return err
	}
	writeData(w, 200, map[string]bool{"discarded": true})
	s.Notify()
	return nil
}
func (s *Server) cancelSSO(w http.ResponseWriter, r *http.Request, principal auth.Session) error {
	op, err := s.ownedSSO(r.PathValue("id"), principal)
	if err != nil {
		return err
	}
	op.cancel()
	op.attempt.Close()
	s.ssoMu.Lock()
	delete(s.sso, r.PathValue("id"))
	s.ssoMu.Unlock()
	writeData(w, 200, map[string]bool{"canceled": true})
	return nil
}
