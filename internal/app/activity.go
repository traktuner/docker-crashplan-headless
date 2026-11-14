package app

import (
	"context"
	"net/http"
	"strconv"

	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
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
		limit, err := historyLimit(r.URL.Query().Get("limit"))
		if err != nil {
			return err
		}
		data, err := s.native.History(r.Context(), limit)
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
			var in backupControlInput
			if err := decode(w, r, &in); err != nil {
				return err
			}
			control, err := in.control()
			if err != nil {
				return err
			}
			var sets []string
			if err := s.mutation(r.Context(), func(ctx context.Context) error {
				var err error
				sets, err = s.native.BackupControl(ctx, action, control)
				return err
			}); err != nil {
				return err
			}
			writeData(w, 202, map[string]any{"accepted": true, "backupSetIds": sets})
			return nil
		})
	}
	// "Back up now": the stock per-destination ResumeBackupRequest with
	// backupSetId + destinationGuid (both optional).
	handle("POST /api/v1/backup/run", "backup.run", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			BackupSetID   string `json:"backupSetId"`
			DestinationID string `json:"destinationId"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := s.mutation(r.Context(), func(ctx context.Context) error {
			_, err := s.native.BackupControl(ctx, "run", engine.BackupControl{BackupSetID: in.BackupSetID, DestinationID: in.DestinationID})
			return err
		}); err != nil {
			return err
		}
		writeData(w, 202, map[string]bool{"accepted": true})
		return nil
	})
	handle("POST /api/v1/destinations/{id}/reconnect", "destinations.reconnect", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		id := r.PathValue("id")
		if err := s.mutation(r.Context(), func(ctx context.Context) error { return s.native.Reconnect(ctx, id) }); err != nil {
			return err
		}
		writeData(w, 202, map[string]bool{"accepted": true})
		return nil
	})
}

// backupControlInput accepts the contract's destinationId and the older
// destinationGuid for the same value.
type backupControlInput struct {
	BackupSetID     string `json:"backupSetId"`
	PlanID          string `json:"planUid"`
	DestinationID   string `json:"destinationId"`
	DestinationGUID string `json:"destinationGuid"`
	DurationMinutes string `json:"durationMinutes"`
}

func (in backupControlInput) control() (engine.BackupControl, error) {
	destination := in.DestinationID
	if in.DestinationGUID != "" {
		if destination != "" && destination != in.DestinationGUID {
			return engine.BackupControl{}, engine.ErrInvalidRequest
		}
		destination = in.DestinationGUID
	}
	return engine.BackupControl{BackupSetID: in.BackupSetID, PlanID: in.PlanID, DestinationID: destination, DurationMinutes: in.DurationMinutes}, nil
}

// historyLimit parses ?limit=N (1..50000); empty means all entries.
func historyLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > engine.HistoryLimitMax || strconv.Itoa(n) != raw {
		return 0, engine.ErrInvalidRequest
	}
	return n, nil
}
