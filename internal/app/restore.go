package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/restore"
)

func (s *Server) restoreRoutes(handle func(string, string, bool, handler)) {
	handle("POST /api/v1/restore/operations/archive", "restore.history.archive", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Restores == nil {
			return restore.ErrJournal
		}
		var in struct {
			IDs     []string `json:"requestIds"`
			Confirm string   `json:"confirm"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.Confirm != "ARCHIVE" {
			return engine.ErrInvalidRequest
		}
		count := 0
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			count, err = s.options.Restores.ArchiveOperations(ctx, in.IDs)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 200, map[string]int{"archived": count})
		return nil
	})
	handle("POST /api/v1/restore/original", "restore.original", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Restores == nil {
			return restore.ErrJournal
		}
		var in restore.OriginalRequest
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var result restore.OriginalResult
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.options.Restores.StartOriginal(ctx, s.options.Lifecycle.Snapshot().Generation, in)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 202, result)
		return nil
	})
	handle("POST /api/v1/restore/search", "restore.search", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in engine.ArchiveSearch
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := in.ArchiveQuery.Validate(); err != nil {
			return err
		}
		var page engine.ArchivePage
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			credentials, err := s.native.RestoreContext(ctx, in.SourceID)
			if err != nil {
				return err
			}
			page, err = s.native.SearchArchive(ctx, credentials, in)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 200, page)
		return nil
	})
	handle("POST /api/v1/restore/sets", "restore.sets", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Source string `json:"sourceId"`
			Target string `json:"targetId"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		sets, err := s.native.ArchiveSets(r.Context(), in.Source, in.Target)
		if err != nil {
			return err
		}
		writeData(w, 200, sets)
		return nil
	})
	handle("GET /api/v1/restore/roots", "restore.roots", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Restores == nil {
			return restore.ErrJournal
		}
		writeData(w, 200, s.options.Restores.Roots())
		return nil
	})
	handle("GET /api/v1/restore/operations", "restore.operations", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Restores == nil {
			return restore.ErrJournal
		}
		writeData(w, 200, s.options.Restores.Operations())
		return nil
	})
	handle("GET /api/v1/restore/helper", "restore.helper", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.RestoreHelpers == nil {
			return restore.ErrHelper
		}
		writeData(w, 200, s.options.RestoreHelpers.Snapshot())
		return nil
	})
	handle("GET /api/v1/restore/jobs", "restore.jobs", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Restores == nil {
			return restore.ErrJournal
		}
		jobs, err := s.options.Restores.Jobs(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, jobs)
		return nil
	})
	handle("POST /api/v1/restore/jobs", "restore.create", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Restores == nil {
			return restore.ErrJournal
		}
		var in restore.StartRequest
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var op restore.Operation
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			op, err = s.options.Restores.Start(ctx, s.options.Lifecycle.Snapshot().Generation, in)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, http.StatusAccepted, op)
		return nil
	})
	control := func(action string) handler {
		return func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
			if s.options.Restores == nil {
				return restore.ErrJournal
			}
			var in struct{}
			if r.Method != "DELETE" {
				if err := decode(w, r, &in); err != nil {
					return err
				}
			}
			id := r.PathValue("id")
			err := s.mutation(r.Context(), func(ctx context.Context) error {
				jobs, err := s.options.Restores.Jobs(ctx)
				if err != nil {
					return err
				}
				for _, job := range jobs {
					if job.ID == id {
						if action == "START" && !job.Admissible {
							return restore.ErrPendingJob
						}
						return s.native.ControlRestore(ctx, id, action)
					}
				}
				return engine.ErrInvalidRequest
			})
			if err != nil {
				return err
			}
			writeData(w, 202, map[string]bool{"accepted": true})
			return nil
		}
	}
	for _, action := range []string{"START", "STOP"} {
		handle("POST /api/v1/restore/jobs/{id}/"+strings.ToLower(action), "restore."+strings.ToLower(action), true, control(action))
	}
	handle("DELETE /api/v1/restore/jobs/{id}", "restore.remove", true, control("REMOVE"))
	for _, kind := range []string{"browse", "versions", "contents"} {
		handle("POST /api/v1/restore/"+kind, "restore."+kind, true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
			var in engine.ArchiveQuery
			if err := decode(w, r, &in); err != nil {
				return err
			}
			if err := in.Validate(); err != nil {
				return err
			}
			var data any
			err := s.mutation(r.Context(), func(ctx context.Context) error {
				if kind == "contents" {
					var err error
					data, err = s.native.ArchiveContents(ctx, in)
					return err
				}
				if kind == "versions" {
					var err error
					data, err = s.native.ArchiveVersions(ctx, in)
					return err
				}
				credentials, err := s.native.RestoreContext(ctx, in.SourceID)
				if err != nil {
					return err
				}
				data, err = s.native.BrowseArchive(ctx, credentials, in)
				return err
			})
			if err != nil {
				return err
			}
			writeData(w, 200, data)
			return nil
		})
	}
}
