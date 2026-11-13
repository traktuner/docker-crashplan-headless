package app

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/traktuner/crashplan-headless/internal/auth"
)

type notifications struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
}

func newNotifications() *notifications {
	return &notifications{subscribers: make(map[chan struct{}]struct{})}
}

// Notify coalesces invalidations. The SSE payload never contains vendor event
// bodies; authenticated typed reads provide the current application data.
func (s *Server) Notify() {
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	for ch := range s.events.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, principal auth.Session) error {
	ch := make(chan struct{}, 1)
	s.events.mu.Lock()
	s.events.subscribers[ch] = struct{}{}
	s.events.mu.Unlock()
	defer func() { s.events.mu.Lock(); delete(s.events.subscribers, ch); s.events.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func() error {
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if _, err := fmt.Fprint(w, "event: update\ndata: {}\n\n"); err != nil {
			return err
		}
		return controller.Flush()
	}
	if err := send(); err != nil {
		return nil
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-s.options.Context.Done():
			return nil
		case <-ticker.C:
			// The outer handler selected the Unix trust boundary. A web stream
			// must end after session expiry/logout, just like a normal API read.
			if s.sessions != nil && principal.ID != "unix" {
				if _, err := s.sessions.AuthorizePassive(r); err != nil {
					return nil
				}
			}
			if err := send(); err != nil {
				return nil
			}
		case <-ch:
			if err := send(); err != nil {
				return nil
			}
		}
	}
}
