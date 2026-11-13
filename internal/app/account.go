package app

import (
	"context"
	"net/http"

	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
)

func (s *Server) accountRoutes(handle func(string, string, bool, handler)) {
	handle("POST /api/v1/account/replace-device/{action}", "account.replacement.complete", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Confirm string `json:"confirm"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var state engine.ReplacementState
		if err := s.accountMutation(r.Context(), func(ctx context.Context) error {
			var err error
			state, err = s.native.CompleteReplacement(ctx, r.PathValue("action"), in.Confirm)
			return err
		}); err != nil {
			return err
		}
		writeData(w, 200, state)
		return nil
	})
	handle("GET /api/v1/account/migration", "account.migration", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		writeData(w, 200, s.native.Migration())
		return nil
	})
	handle("POST /api/v1/account/migration/selection", "account.migration.select", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Selected string `json:"selectedComputerGuid"`
			Confirm  string `json:"confirm"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.Selected != "" && in.Selected != in.Confirm {
			return engine.ErrInvalidRequest
		}
		var result engine.LoginResult
		err := s.accountMutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.native.SelectMigration(ctx, in.Selected)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})
	handle("GET /api/v1/account/migration/candidates", "account.migration.candidates", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		state, err := s.native.MigrationCandidates(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, state)
		return nil
	})
	handle("POST /api/v1/account/migration/authorized", "account.migration.authorize", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Source  string `json:"sourceComputerGuid"`
			Confirm string `json:"confirm"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := s.accountMutation(r.Context(), func(ctx context.Context) error { return s.native.AuthorizeMigration(ctx, in.Source, in.Confirm) }); err != nil {
			return err
		}
		writeData(w, 202, map[string]bool{"accepted": true})
		return nil
	})
	handle("GET /api/v1/account/replace-device", "account.replacement", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		state, err := s.native.Replacement(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, state)
		return nil
	})
	handle("POST /api/v1/account/replace-device", "account.replace", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Source  string `json:"sourceComputerGuid"`
			Confirm string `json:"confirm"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := s.accountMutation(r.Context(), func(ctx context.Context) error { return s.native.ReplaceDevice(ctx, in.Source, in.Confirm) }); err != nil {
			return err
		}
		writeData(w, 202, map[string]bool{"accepted": true})
		return nil
	})
}
