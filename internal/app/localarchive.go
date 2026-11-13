package app

import (
	"context"
	"net/http"

	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/destinations"
	"github.com/traktuner/crashplan-headless/internal/engine"
)

func (s *Server) localArchiveRoutes(handle func(string, string, bool, handler)) {
	handle("GET /api/v1/destinations/local/policy", "destinations.local.policy", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		policy, err := s.native.LocalArchivePolicy(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, policy)
		return nil
	})
	handle("GET /api/v1/destinations/local/operations/{id}", "destinations.local.status", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.LocalArchives == nil {
			return destinations.ErrJournal
		}
		result, err := s.options.LocalArchives.Status(r.PathValue("id"))
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})

	handle("POST /api/v1/destinations/local", "destinations.local", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.LocalArchives == nil {
			return destinations.ErrJournal
		}
		var in destinations.Request
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.Action == "rename" || in.Action == "remove" || in.Action == "maintenance" {
			return engine.ErrInvalidRequest
		}
		var result destinations.Operation
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.options.LocalArchives.Start(ctx, in)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 202, result)
		return nil
	})
}
