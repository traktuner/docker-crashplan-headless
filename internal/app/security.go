package app

import (
	"context"
	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"net/http"
)

func (s *Server) securityRoutes(handle func(string, string, bool, handler)) {
	handle("POST /api/v1/security/custom-key", "security.key.replace", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in engine.CustomKeyChange
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var state engine.SecurityState
		if err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			state, err = s.native.ReplaceCustomKey(ctx, in)
			return err
		}); err != nil {
			return err
		}
		writeData(w, 200, state)
		return nil
	})
	handle("POST /api/v1/security/derived-key/export", "security.key.derive.export", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in engine.DerivedKeyInput
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var key engine.KeyExport
		if err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			key, err = s.native.ExportDerivedKey(ctx, in)
			return err
		}); err != nil {
			return err
		}
		s.writeKeyAttachment(w, key)
		return nil
	})

	handle("POST /api/v1/security/export-key", "security.key.export", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct{}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var key engine.KeyExport
		if err := s.mutation(r.Context(), func(ctx context.Context) error { var err error; key, err = s.native.ExportArchiveKey(ctx); return err }); err != nil {
			return err
		}
		s.writeKeyAttachment(w, key)
		return nil
	})
	handle("GET /api/v1/security", "security.status", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		state, err := s.native.Security(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, state)
		return nil
	})
	handle("POST /api/v1/security/unlock", "security.unlock", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in engine.ArchiveUnlock
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := s.mutation(r.Context(), func(ctx context.Context) error { return s.native.UnlockArchive(ctx, in) }); err != nil {
			return err
		}
		writeData(w, 200, map[string]bool{"unlocked": true})
		return nil
	})
	handle("POST /api/v1/security/backup-unlock", "security.backup.unlock", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Secret string `json:"secret"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := s.mutation(r.Context(), func(ctx context.Context) error { return s.native.UnlockBackup(ctx, in.Secret) }); err != nil {
			return err
		}
		writeData(w, 200, map[string]bool{"accepted": true})
		return nil
	})
	handle("POST /api/v1/security/password", "security.password", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in engine.ArchivePassword
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := s.mutation(r.Context(), func(ctx context.Context) error { return s.native.ArchivePassword(ctx, in) }); err != nil {
			return err
		}
		writeData(w, 200, map[string]bool{"accepted": true})
		return nil
	})
	handle("POST /api/v1/security/key-type", "security.upgrade", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in engine.SecurityUpgrade
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var state engine.SecurityState
		if err := s.mutation(r.Context(), func(ctx context.Context) error {
			var err error
			state, err = s.native.UpgradeSecurity(ctx, in)
			return err
		}); err != nil {
			return err
		}
		writeData(w, 200, state)
		return nil
	})
}

func (s *Server) writeKeyAttachment(w http.ResponseWriter, key engine.KeyExport) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="crashplan-archive-key.txt"`)
	if _, err := key.WriteTo(w); err != nil {
		s.options.Logger.Warn("archive_key_download_interrupted")
	}
}
