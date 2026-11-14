package app

import (
	"context"
	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"net/http"
)

func (s *Server) activityRoutes(handle func(string, string, bool, handler)) {
	handle("GET /api/v1/backup/summary", "backup.summary", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		data, err := s.native.BackupSummary(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, data)
		return nil
	})
	handle("GET /api/v1/backup/progress", "backup.progress", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		data, err := s.native.BackupProgress(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, data)
		return nil
	})
	handle("GET /api/v1/alerts", "alerts", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		data, err := s.native.Alerts(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, data)
		return nil
	})
	handle("GET /api/v1/history", "history", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		data, err := s.native.History(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, data)
		return nil
	})
	handle("POST /api/v1/alerts/dismiss", "alerts.dismiss", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			ID string `json:"id"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := s.mutation(r.Context(), func(ctx context.Context) error { return s.native.DismissAlert(ctx, in.ID) }); err != nil {
			return err
		}
		writeData(w, 200, map[string]bool{"removed": true})
		return nil
	})
	for _, action := range []string{"pause", "resume"} {
		handle("POST /api/v1/backup/"+action, "backup."+action, true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
			var in engine.BackupControl
			if err := decode(w, r, &in); err != nil {
				return err
			}
			var sets []string
			if err := s.mutation(r.Context(), func(ctx context.Context) error {
				var err error
				sets, err = s.native.BackupControl(ctx, action, in)
				return err
			}); err != nil {
				return err
			}
			writeData(w, 202, map[string]any{"accepted": true, "backupSetIds": sets})
			return nil
		})
	}
}
