// Package app owns the product API. Browser and local Unix access are separate
// trust boundaries; neither exposes an arbitrary native proxy or native token.
package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/destinations"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
	"github.com/traktuner/crashplan-headless/internal/restore"
	"github.com/traktuner/crashplan-headless/internal/sources"
	"github.com/traktuner/crashplan-headless/internal/statefile"
	"github.com/traktuner/crashplan-headless/internal/supervise"
)

type Options struct {
	Logger         *slog.Logger
	Context        context.Context
	Sources        supervise.SourceGate
	Scans          *engine.Scans
	Runtime        *supervise.Supervisor
	Assets         http.Handler
	Lifecycle      *lifecycle.Owner
	Restores       *restore.Service
	RestoreHelpers *restore.Helpers
	LocalArchives  *destinations.Service
	TrustedProxies []netip.Prefix
}

type Server struct {
	contextID string
	native    *engine.Client
	sessions  *auth.Manager
	options   Options
	ssoMu     sync.Mutex
	sso       map[string]*ssoOperation
	events    *notifications
}

func New(native *engine.Client, sessions *auth.Manager, options Options) *Server {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	return &Server{contextID: rand.Text(), native: native, sessions: sessions, options: options, sso: make(map[string]*ssoOperation), events: newNotifications()}
}

func (s *Server) WebHandler() http.Handler { return s.webContext(s.handler(false)) }

// UnixHandler must be bound only to an owner-private Unix socket. No request
// header, cookie, query parameter, or user body can select this trust level.
func (s *Server) UnixHandler() http.Handler { return s.handler(true) }

type handler func(http.ResponseWriter, *http.Request, auth.Session) error

func (s *Server) handler(local bool) http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern, name string, mutation bool, fn handler) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			principal := auth.Session{ID: "unix"}
			var err error
			if !local {
				if s.sessions == nil {
					err = auth.ErrUnauthorized
				} else {
					if !mutation && name != "session" {
						principal, err = s.sessions.AuthorizePassive(r)
					} else {
						principal, err = s.sessions.Authorize(r, mutation)
					}
				}
			}
			if err == nil && !local && mutation && name != "logout" {
				var admitted *http.Request
				admitted, err = s.admitWebContext(r)
				if err == nil {
					r = admitted
				}
			}
			if err == nil {
				err = fn(w, r, principal)
			}
			if err != nil {
				writeError(w, err)
			}
			// Names are compile-time operation names. Never log URL, headers,
			// query, bodies, account identity, paths or error response contents.
			code := "OK"
			if err != nil {
				code = errorCode(err)
			}
			level := slog.LevelDebug
			if mutation {
				level = slog.LevelInfo
			}
			s.options.Logger.Log(r.Context(), level, "request", "operation", name, "outcome", code, "duration_ms", time.Since(started).Milliseconds())
		})
	}
	if !local && s.sessions != nil {
		mux.HandleFunc("POST /api/v1/session", func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			outcome := "REJECTED"
			defer func() {
				s.options.Logger.Info("request", "operation", "web.login", "outcome", outcome, "duration_ms", time.Since(started).Milliseconds())
			}()
			release, err := s.sessions.AdmitLogin()
			if err != nil {
				outcome = errorCode(err)
				writeError(w, err)
				return
			}
			defer release()
			r.Body = http.MaxBytesReader(w, r.Body, 70<<10)
			if err := s.sessions.CheckRequestOrigin(r); err != nil {
				writeError(w, err)
				return
			}
			var in struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := decode(w, r, &in); err != nil {
				writeError(w, err)
				return
			}
			peer, splitErr := auth.ClientIP(r, s.options.TrustedProxies)
			if splitErr != nil {
				writeError(w, auth.ErrUnauthorized)
				return
			}
			session, err := s.sessions.LoginFrom(r.Context(), peer, in.Username, in.Password)
			if err != nil {
				writeError(w, err)
				return
			}
			outcome = "OK"
			http.SetCookie(w, s.sessions.CookieFor(session, r))
			writeData(w, 200, session)
		})
	}
	handle("GET /api/v1/session", "session", false, func(w http.ResponseWriter, r *http.Request, p auth.Session) error { writeData(w, 200, p); return nil })
	handle("GET /api/v1/session/status", "session.status", false, func(w http.ResponseWriter, r *http.Request, p auth.Session) error { writeData(w, 200, p); return nil })
	handle("DELETE /api/v1/session", "logout", true, func(w http.ResponseWriter, r *http.Request, p auth.Session) error {
		if !local {
			s.sessions.Logout(p.ID)
			cookie := s.sessions.CookieFor(auth.Session{}, r)
			cookie.MaxAge = -1
			http.SetCookie(w, cookie)
		}
		writeData(w, 200, map[string]bool{"signedOut": true})
		return nil
	})
	handle("GET /api/v1/state", "status", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		result, err := s.native.ApplicationStatus(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})
	handle("GET /api/v1/capabilities", "capabilities", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		writeData(w, 200, map[string]any{"nativeSession": s.native.Authorized(), "discovery": "scan", "productionAcceptance": "incomplete"})
		return nil
	})
	handle("GET /api/v1/events", "events", false, s.streamEvents)
	s.restoreRoutes(handle)
	s.localArchiveRoutes(handle)
	s.settingsRoutes(handle)
	s.backupSetRoutes(handle)
	s.accountRoutes(handle)
	s.securityRoutes(handle)
	s.activityRoutes(handle)
	handle("POST /api/v1/account/sso", "account.sso", true, s.beginSSO)
	handle("GET /api/v1/account/sso/{id}", "account.sso.status", false, s.readSSO)
	handle("DELETE /api/v1/account/sso/{id}", "account.sso.cancel", true, s.cancelSSO)
	handle("POST /api/v1/account/sso/{id}/confirm", "account.sso.confirm", true, s.confirmSSO)
	handle("POST /api/v1/account/sso/{id}/discard", "account.sso.discard", true, s.discardSSO)
	handle("POST /api/v1/account/login", "account.login", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			engine.LoginInfo
			Password string `json:"password"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var result engine.LoginResult
		err := s.accountMutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.native.Login(ctx, engine.Credentials{Username: in.Username, Password: in.Password, ServerAddress: in.ServerAddress, RegistrationKey: in.RegistrationKey, Register: in.Register, FirstName: in.FirstName, LastName: in.LastName})
			return err
		})
		if err != nil {
			return err
		}
		if result.Complete && !result.Persisted {
			s.options.Logger.Warn("native_session_not_persisted")
		}
		writeData(w, 200, result)
		return nil
	})
	handle("POST /api/v1/backup-sets/{id}/scan", "scan", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct{}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		id := r.PathValue("id")
		if err := s.native.CheckScan(id); err != nil {
			return err
		}
		if s.options.Sources == nil {
			return sources.ErrNotReady
		}
		if s.options.Scans == nil {
			return engine.ErrScanJournal
		}
		var op engine.ScanOperation
		err := s.mutation(r.Context(), func(ctx context.Context) error {
			if err := s.options.Sources.Check(); err != nil {
				return err
			}
			var err error
			op, err = s.options.Scans.Start(ctx, id)
			return err
		})
		if err != nil {
			return err
		}
		writeData(w, http.StatusAccepted, op)
		return nil
	})
	handle("GET /api/v1/scans/{id}", "scan.get", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Scans == nil {
			return engine.ErrScanJournal
		}
		op, err := s.options.Scans.Get(r.PathValue("id"))
		if err != nil {
			return err
		}
		writeData(w, 200, op)
		return nil
	})
	handle("POST /api/v1/scans/{id}/acknowledge", "scan.acknowledge", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Scans == nil {
			return engine.ErrScanJournal
		}
		var in struct{}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := s.options.Scans.Acknowledge(r.PathValue("id")); err != nil {
			return err
		}
		writeData(w, 200, map[string]bool{"acknowledged": true})
		return nil
	})
	handle("GET /api/v1/scans", "scan.list", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Scans == nil {
			return engine.ErrScanJournal
		}
		writeData(w, 200, s.options.Scans.List())
		return nil
	})
	handle("GET /api/v1/backup-sets", "backup-sets", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		result, err := s.native.Call(r.Context(), "backupSets.basic", engine.OperationRequest{})
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})
	handle("GET /api/v1/account/setup", "account.setup", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		result, err := s.native.LoginSetup(r.Context())
		if err != nil {
			return err
		}
		writeData(w, 200, result)
		return nil
	})
	handle("POST /api/v1/account/authority", "account.authority", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in struct {
			Username string `json:"username"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		address, err := s.native.DiscoverAuthority(r.Context(), in.Username)
		if err != nil {
			return err
		}
		writeData(w, 200, map[string]string{"serverAddress": address})
		return nil
	})
	handle("POST /api/v1/account/configuration", "account.configuration", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		var in engine.LoginInfo
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var result engine.LoginConfiguration
		if err := s.accountMutation(r.Context(), func(ctx context.Context) error {
			var err error
			result, err = s.native.LoginConfiguration(ctx, in)
			return err
		}); err != nil {
			return err
		}
		writeData(w, 200, map[string]string{"loginType": result.Type})
		return nil
	})
	handle("POST /api/v1/engine/restart", "engine.restart", true, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Runtime == nil {
			return engine.ErrUnavailable
		}
		if err := s.options.Runtime.Restart(); err != nil {
			return err
		}
		writeData(w, 202, map[string]bool{"accepted": true})
		return nil
	})
	handle("GET /api/v1/health", "health", false, func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if s.options.Runtime == nil {
			return engine.ErrUnavailable
		}
		state := struct {
			supervise.State
			Native lifecycle.State `json:"native"`
		}{State: s.options.Runtime.Snapshot()}
		if s.options.Lifecycle != nil {
			state.Native = s.options.Lifecycle.Snapshot()
		}
		status := 200
		if state.Status != "running" || !state.Native.NativeReady || !state.Native.EventsReady {
			status = 503
		}
		writeData(w, status, state)
		return nil
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve the embedded SPA to browsers only. Unknown API paths and every
		// non-API path on the Unix socket are not found.
		if !local && s.options.Assets != nil && r.Method == "GET" && !strings.HasPrefix(r.URL.Path, "/api/") {
			s.options.Assets.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if !local && s.sessions != nil {
			if err := s.sessions.CheckOrigin(r); err != nil {
				writeError(w, err)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) mutation(ctx context.Context, fn func(context.Context) error) error {
	if s.options.Lifecycle == nil {
		return engine.ErrUnavailable
	}
	return s.options.Lifecycle.Do(ctx, s.bindWebMutation(ctx, fn))
}

func (s *Server) accountMutation(ctx context.Context, fn func(context.Context) error) error {
	if s.options.Lifecycle == nil {
		return engine.ErrUnavailable
	}
	return s.options.Lifecycle.DoAccount(ctx, s.bindWebMutation(ctx, fn))
}

func decode(w http.ResponseWriter, r *http.Request, out any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		return engine.ErrInvalidRequest
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return engine.ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return engine.ErrInvalidRequest
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return engine.ErrInvalidRequest
	}
	return nil
}

func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	// Encode before headers: a schema error must not leave a false success.
	encoded, err := json.Marshal(struct {
		Data any `json:"data"`
	}{data})
	if err != nil {
		writeError(w, engine.ErrProtocol)
		return
	}
	w.WriteHeader(status)
	if _, err := w.Write(append(encoded, '\n')); err != nil {
		slog.Warn("response_write_failed")
	}
}

func errorCode(err error) string {
	var loginReason *engine.LoginRejection
	if errors.As(err, &loginReason) {
		switch loginReason.Reason() {
		case "USE_BROWSER_SSO":
			return "NATIVE_MFA_BROWSER_REQUIRED"
		case "RATE_LIMITED":
			return "NATIVE_LOGIN_THROTTLED"
		case "SERVER_UNAVAILABLE":
			return "NATIVE_AUTHORITY_UNAVAILABLE"
		case "AUTHORITY_REQUIRED":
			return "NATIVE_AUTHORITY_REQUIRED"
		case "ACCOUNT_NOT_FOUND":
			return "NATIVE_ACCOUNT_NOT_FOUND"
		case "REGISTRATION_KEY_REQUIRED":
			return "NATIVE_REGISTRATION_KEY_REQUIRED"
		case "REGISTRATION_REJECTED":
			return "NATIVE_REGISTRATION_REJECTED"
		}
	}
	var sourceReason sources.Reason
	if errors.As(err, &sourceReason) {
		switch sourceReason {
		case sources.NotMounted, sources.FilesystemRejected, sources.SentinelMissing, sources.IdentityChanged:
			return string(sourceReason)
		}
	}
	switch {
	case errors.Is(err, engine.ErrIndeterminate):
		return "NATIVE_OPERATION_INDETERMINATE"
	case errors.Is(err, engine.ErrSettingLocked):
		return "SETTING_LOCKED"
	case errors.Is(err, destinations.ErrJournal):
		return "LOCAL_ARCHIVE_JOURNAL_UNAVAILABLE"
	case errors.Is(err, restore.ErrJournal):
		return "RESTORE_JOURNAL_UNAVAILABLE"
	case errors.Is(err, restore.ErrJournalFull):
		return "RESTORE_JOURNAL_FULL"
	case errors.Is(err, restore.ErrDestination), errors.Is(err, engine.ErrRestorePath):
		return "RESTORE_DESTINATION_REJECTED"
	case errors.Is(err, engine.ErrRestoreDuplicateNames):
		return "RESTORE_DUPLICATE_NAMES"
	case errors.Is(err, restore.ErrMarker), errors.Is(err, engine.ErrRestoreMarker):
		return "RESTORE_MARKER_REJECTED"
	case errors.Is(err, restore.ErrPendingJob):
		return "RESTORE_PENDING_JOB_OUTSIDE_ROOTS"
	case errors.Is(err, engine.ErrArchiveKey):
		return "ARCHIVE_UNLOCK_REQUIRED"
	case errors.Is(err, engine.ErrContentsPending):
		return "ARCHIVE_STATISTICS_PENDING"
	case errors.Is(err, engine.ErrSearchPending):
		return "ARCHIVE_SEARCH_PENDING"
	case errors.Is(err, engine.ErrSearchScope):
		return "ARCHIVE_SEARCH_SCOPE_UNSUPPORTED"
	case errors.Is(err, engine.ErrRestoreSpace):
		return "RESTORE_NO_SPACE"
	case errors.Is(err, engine.ErrRestoreReadOnly):
		return "RESTORE_DESTINATION_READ_ONLY"
	case errors.Is(err, lifecycle.ErrBusy):
		return "CONTROL_BUSY"
	case errors.Is(err, supervise.ErrNotRunning):
		return "ENGINE_NOT_RUNNING"
	case errors.Is(err, engine.ErrAlreadyCompleted):
		return "LOGIN_ATTEMPT_ALREADY_COMPLETED"
	case errors.Is(err, statefile.ErrState):
		return "PRIVATE_STATE_UNAVAILABLE"
	case errors.Is(err, engine.ErrScanJournal):
		return "SCAN_JOURNAL_UNAVAILABLE"
	case errors.Is(err, engine.ErrSessionChanged):
		return "NATIVE_SESSION_CHANGED"
	case errors.Is(err, engine.ErrNotDispatched):
		return "NATIVE_OPERATION_NOT_DISPATCHED"
	case errors.Is(err, engine.ErrLegacyPending):
		return "NATIVE_RESPONSE_PENDING"
	case errors.Is(err, auth.ErrUnauthorized):
		return "WEB_AUTHORIZATION_REQUIRED"
	case errors.Is(err, auth.ErrCSRF):
		return "CSRF_REJECTED"
	case errors.Is(err, auth.ErrOrigin):
		return "PUBLIC_ORIGIN_MISMATCH"
	case errors.Is(err, auth.ErrRateLimited):
		return "WEB_LOGIN_RATE_LIMITED"
	case errors.Is(err, sources.ErrNotReady):
		return "SOURCE_NOT_READY"
	case errors.Is(err, engine.ErrScanInProgress):
		return "SCAN_ALREADY_PENDING"
	case errors.Is(err, engine.ErrUnauthorized):
		return "NATIVE_AUTHORIZATION_REQUIRED"
	case errors.Is(err, engine.ErrLoginRejected):
		return "NATIVE_LOGIN_REJECTED"
	case errors.Is(err, engine.ErrInvalidRequest):
		return "INVALID_REQUEST"
	case errors.Is(err, engine.ErrRejected):
		return "NATIVE_OPERATION_REJECTED"
	case errors.Is(err, engine.ErrProtocol):
		return "NATIVE_PROTOCOL_MISMATCH"
	case errors.Is(err, engine.ErrLoginInProgress):
		return "NATIVE_LOGIN_IN_PROGRESS"
	default:
		return "NATIVE_ENGINE_UNAVAILABLE"
	}
}

func writeError(w http.ResponseWriter, err error) {
	code := errorCode(err)
	status := http.StatusServiceUnavailable
	message := "The native engine is unavailable."
	switch code {
	case "ARCHIVE_STATISTICS_PENDING":
		status = http.StatusConflict
		message = "A native archive count is still running or unresolved. Request its progress before starting another count."
	case "NATIVE_RESPONSE_PENDING":
		status = http.StatusConflict
		message = "A previous native response is unresolved. Restart the engine before repeating this operation."
	case "NATIVE_MFA_BROWSER_REQUIRED":
		status = 409
		message = "This account requires additional verification. Use browser SSO or the account console; the native password login has no MFA code field."
	case "NATIVE_SESSION_CHANGED":
		status = 409
		message = "The CrashPlan session changed. Review the current account before submitting this action again."
	case "NATIVE_LOGIN_THROTTLED":
		status = 429
		message = "CrashPlan temporarily limited sign-in attempts. Try again later."
	case "NATIVE_AUTHORITY_UNAVAILABLE":
		status = 503
		message = "The account authority is unavailable. Try sign-in again later."
	case "NATIVE_AUTHORITY_REQUIRED":
		status = 422
		message = "The server for this account could not be determined. Enter the server address."
	case "NATIVE_ACCOUNT_NOT_FOUND":
		status = 422
		message = "CrashPlan found no account with this email address or username."
	case "NATIVE_REGISTRATION_KEY_REQUIRED":
		status = 422
		message = "The authority supplied no registration key. Enter a valid registration key before creating the account."
	case "NATIVE_REGISTRATION_REJECTED":
		status = 422
		message = "The registration key is invalid or account registration is blocked."
	case "SETTING_LOCKED":
		status = 409
		message = "This setting is locked by the native policy. No settings were changed."
	case "LOCAL_ARCHIVE_JOURNAL_UNAVAILABLE":
		status = 503
		message = "The local archive journal is unavailable. No new local archive operation can be submitted."
	case "RESTORE_JOURNAL_UNAVAILABLE":
		status = 503
		message = "The restore journal is unavailable. No new restore can be submitted."
	case "RESTORE_JOURNAL_FULL":
		status = 409
		message = "The restore journal has reached its limit. Existing restores remain available."
	case "RESTORE_DESTINATION_REJECTED", "RESTORE_DESTINATION_READ_ONLY":
		status = 422
		message = "Choose an existing writable directory under a configured restore root."
	case "RESTORE_DUPLICATE_NAMES":
		status = 422
		message = "Restore selections with the same top-level name in separate jobs, or use original-location restore."
	case "RESTORE_MARKER_REJECTED":
		status = 409
		message = "The native restore permission check failed. Inspect restore jobs before submitting again."
	case "RESTORE_PENDING_JOB_OUTSIDE_ROOTS":
		status = 409
		message = "This restore job is outside the configured writable roots. It cannot be started."
	case "ARCHIVE_UNLOCK_REQUIRED":
		status = 409
		message = "Unlock this archive in CrashPlan before browsing or restoring it."
	case "ARCHIVE_SEARCH_PENDING":
		status = 409
		message = "An archive search is active or its outcome is unknown. An uncertain search remains blocked until the engine restarts."
	case "ARCHIVE_SEARCH_SCOPE_UNSUPPORTED":
		status = 422
		message = "This archive supports archive-wide search. Search from the archive root."
	case "RESTORE_NO_SPACE":
		status = 422
		message = "The restore destination does not have enough free space."
	case "WEB_AUTHORIZATION_REQUIRED":
		status = 401
		message = "Sign in to the web interface."
	case "CSRF_REJECTED", "PUBLIC_ORIGIN_MISMATCH":
		status = 403
		message = "The request origin or session verification failed."
	case "WEB_LOGIN_RATE_LIMITED":
		status = 429
		message = "Too many login attempts. Try again later."
		w.Header().Set("Retry-After", "30")
	case "INVALID_REQUEST":
		status = 400
		message = "The request is invalid."
	case "NATIVE_AUTHORIZATION_REQUIRED":
		status = 409
		message = "Sign in to CrashPlan to perform this action."
	case "SOURCE_NOT_READY":
		status = 503
		message = "A configured local source is unavailable. The scan was not started."
	case "SOURCE_NOT_MOUNTED", "SOURCE_FILESYSTEM_REJECTED", "SOURCE_SENTINEL_MISSING", "SOURCE_IDENTITY_CHANGED":
		status = 503
		message = "A configured local source failed validation. The scan was not started."
	case "NATIVE_OPERATION_NOT_DISPATCHED":
		status = 503
		message = "The native action was not sent because the connection failed."
	case "SCAN_JOURNAL_UNAVAILABLE":
		status = 503
		message = "The scan journal is unavailable. Backup supervision continues."
	case "PRIVATE_STATE_UNAVAILABLE":
		status = 503
		message = "The private application state is unavailable."
	case "CONTROL_BUSY":
		status = 503
		message = "The control service is busy. No action was queued."
	case "ENGINE_NOT_RUNNING":
		status = 409
		message = "The engine is not running."
	case "LOGIN_ATTEMPT_ALREADY_COMPLETED":
		status = 409
		message = "This sign-in attempt has ended."
	case "SCAN_ALREADY_PENDING":
		status = 409
		message = "A scan for this backup set is already pending. Check its status."
	case "NATIVE_LOGIN_IN_PROGRESS":
		status = 409
		message = "A native sign-in is already in progress."
	case "NATIVE_LOGIN_REJECTED":
		status = 422
		message = "CrashPlan rejected the sign-in. Check your account details or server availability."
	case "NATIVE_OPERATION_INDETERMINATE":
		status = 409
		message = "The native action may have started. Do not automatically repeat it."
	case "NATIVE_OPERATION_REJECTED":
		status = 422
		message = "CrashPlan rejected the action."
	case "NATIVE_PROTOCOL_MISMATCH":
		status = 502
		message = "The native engine returned an incompatible response."
	}
	w.Header().Set("Content-Type", "application/json")
	encoded, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	w.WriteHeader(status)
	if _, err := w.Write(append(encoded, '\n')); err != nil {
		slog.Warn("error_response_write_failed")
	}
}
