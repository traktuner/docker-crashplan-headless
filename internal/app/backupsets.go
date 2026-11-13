package app

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
)

func (s *Server) backupSetRoutes(handle func(string, string, bool, handler)) {
	handle("GET /api/v1/backup-sets/details", "sets.list", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		sets, err := s.native.BackupSets(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, sets)
		return nil
	})
	handle("GET /api/v1/backup-sets/{id}", "sets.get", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		set, err := s.native.BackupSet(r.Context(), r.PathValue("id"))
		if err != nil {
			return err
		}
		writeData(w, 200, set)
		return nil
	})
	handle("POST /api/v1/backup-sets", "sets.create", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Name string `json:"name"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var set engine.BackupSet
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			set, err = s.native.CreateBackupSet(ctx, in.Name)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 201, set)
		return nil
	})
	handle("PATCH /api/v1/backup-sets/{id}", "sets.patch", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Changes map[string]json.RawMessage `json:"changes"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var result engine.SetResult
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.native.PatchBackupSet(ctx, r.PathValue("id"), in.Changes)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})
	handle("DELETE /api/v1/backup-sets/{id}", "sets.delete", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Confirm string `json:"confirm"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.Confirm != r.PathValue("id") {
			return engine.ErrInvalidRequest
		}
		if err := s.mutation(r.Context(), func(ctx context.Context) error { return s.native.DeleteBackupSet(ctx, in.Confirm) }); err != nil {
			return err
		}
		writeData(w, 200, map[string]bool{"removed": true})
		return nil
	})
	handle("POST /api/v1/backup-sets/{id}/selection/browse", "selection.browse", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in engine.SourceQuery
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var page engine.SourcePage
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			page, err = s.native.BrowseSource(ctx, r.PathValue("id"), in)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 200, page)
		return nil
	})
	handle("PATCH /api/v1/backup-sets/{id}/selection", "selection.patch", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Changes []engine.SelectionPath `json:"changes"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var result engine.SetResult
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.native.ChangeSelection(ctx, r.PathValue("id"), in.Changes)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})
	handle("PUT /api/v1/backup-sets/{id}/excludes", "sets.excludes", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Patterns []engine.Pattern `json:"patterns"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var result engine.SetResult
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.native.SetExcludes(ctx, r.PathValue("id"), in.Patterns)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})
	handle("PUT /api/v1/backup-sets/{id}/destinations/{target}", "sets.destination", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Attached *bool `json:"attached"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.Attached == nil {
			return engine.ErrInvalidRequest
		}
		var result engine.SetResult
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.native.SetDestination(ctx, r.PathValue("id"), r.PathValue("target"), *in.Attached)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})
}
