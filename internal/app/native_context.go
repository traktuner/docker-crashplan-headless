package app

import (
	"context"
	"net/http"
	"strconv"

	"github.com/traktuner/crashplan-headless/internal/engine"
)

const nativeContextHeader = "X-CPH-Engine-Context"

type webRevisionKey struct{}

// A process nonce prevents a page from matching a reused revision after restart.
// This public precondition is not an authentication credential.
func (s *Server) nativeContext(revision uint64) string {
	return s.contextID + ":" + strconv.FormatUint(revision, 10)
}

func (s *Server) bindWebMutation(wait context.Context, fn func(context.Context) error) func(context.Context) error {
	revision, bound := wait.Value(webRevisionKey{}).(uint64)
	if !bound {
		return fn
	}
	return func(ctx context.Context) error {
		ctx, err := s.native.BindSession(ctx, revision)
		if err != nil {
			return err
		}
		return fn(ctx)
	}
}

type contextWriter struct {
	http.ResponseWriter
	context string
	wrote   bool
}

func (w *contextWriter) WriteHeader(status int) {
	if !w.wrote {
		w.Header().Set(nativeContextHeader, w.context)
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *contextWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func (w *contextWriter) FlushError() error {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *contextWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) webContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&contextWriter{ResponseWriter: w, context: s.nativeContext(s.native.SessionRevision())}, r)
	})
}

func (s *Server) admitWebContext(r *http.Request) (*http.Request, error) {
	revision := s.native.SessionRevision()
	if r.Header.Get(nativeContextHeader) != s.nativeContext(revision) {
		return nil, engine.ErrSessionChanged
	}
	return r.WithContext(context.WithValue(r.Context(), webRevisionKey{}, revision)), nil
}
