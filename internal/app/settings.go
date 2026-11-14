package app

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/destinations"
	"github.com/traktuner/crashplan-headless/internal/engine"
)

func (s *Server) settingsRoutes(handle func(string, string, bool, handler)) {
	handle("POST /api/v1/devices/operations", "devices.update", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.LocalArchives == nil {
			return destinations.ErrJournal
		}
		var in destinations.Request
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.Action != "rename" && in.Action != "remove" && in.Action != "maintenance" {
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
	handle("GET /api/v1/devices/operations/{id}", "devices.operation", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.LocalArchives == nil {
			return destinations.ErrJournal
		}
		result, err := s.options.LocalArchives.Status(r.PathValue("id"))
		if err != nil {
			return err
		}
		// Device and local-archive operations share one permanent submission ledger.
		writeData(w, 200, result)
		return nil
	})
	handle("GET /api/v1/devices", "devices", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		devices, err := s.native.DeviceDetails(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, devices)
		return nil
	})
	handle("GET /api/v1/settings/{area}", "settings.get", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		fields, err := s.native.Settings(r.Context(), r.PathValue("area"))
		if err != nil {
			return err
		}
		writeData(w, 200, fields)
		return nil
	})
	handle("PATCH /api/v1/settings/{area}", "settings.patch", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Changes map[string]json.RawMessage `json:"changes"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var result engine.SettingsResult
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.native.PatchSettings(ctx, r.PathValue("area"), in.Changes)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})
}
