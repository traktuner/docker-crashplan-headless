package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/traktuner/crashplan-headless/internal/app"
	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/config"
	"github.com/traktuner/crashplan-headless/internal/controlsocket"
	"github.com/traktuner/crashplan-headless/internal/destinations"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/initcfg"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
	"github.com/traktuner/crashplan-headless/internal/logx"
	"github.com/traktuner/crashplan-headless/internal/restore"
	"github.com/traktuner/crashplan-headless/internal/runas"
	"github.com/traktuner/crashplan-headless/internal/sources"
	"github.com/traktuner/crashplan-headless/internal/statefile"
	"github.com/traktuner/crashplan-headless/internal/supervise"
	"github.com/traktuner/crashplan-headless/internal/ui"
)

func main() {
	if err := run(); err != nil {
		var variable *config.VariableError
		if errors.As(err, &variable) {
			slog.Error("service_failed", "category", safeError(err), "variable", variable.Name)
		} else {
			slog.Error("service_failed", "category", safeError(err))
		}
		os.Exit(1)
	}
}
func safeError(err error) string {
	var sourceReason sources.Reason
	if errors.As(err, &sourceReason) {
		return string(sourceReason)
	}
	switch {
	case errors.Is(err, restore.ErrHelper):
		return "RESTORE_HELPER_UNAVAILABLE"
	case errors.Is(err, initcfg.ErrInit):
		return "CONFIG_INITIALIZATION_FAILED"
	case errors.Is(err, lifecycle.ErrVersionDrift):
		return "ENGINE_VERSION_DRIFT"
	case errors.Is(err, runas.ErrPrivileges):
		return "USER_SWITCH_FAILED"
	case errors.Is(err, config.ErrConfiguration):
		return "INVALID_CONFIGURATION"
	case errors.Is(err, statefile.ErrState):
		return "PRIVATE_STATE_UNAVAILABLE"
	case errors.Is(err, supervise.ErrEngineExit):
		return "ENGINE_EXITED"
	default:
		return "SERVICE_UNAVAILABLE"
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	options := &slog.HandlerOptions{Level: cfg.LogLevel}
	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, options)
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stdout, options)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	if cfg.SourceRootsDetected {
		logger.Info("sources_detected", "roots", cfg.SourceRoots)
	}
	identity := runas.Resolve(os.Getenv, logger)
	configRoot := filepath.Dir(cfg.StateDir)
	if identity.Switch || os.Geteuid() == 0 {
		// Files left by an image that ran as another user become usable.
		runas.TakeOwnership(configRoot, identity.UID, identity.GID, logger)
		if err := runas.PrepareDir(filepath.Dir(cfg.Socket), identity); err != nil {
			return statefile.ErrState
		}
	}
	if err := runas.Apply(identity); err != nil {
		logger.Error("user_switch_failed", "uid", identity.UID, "gid", identity.GID, "hint", "the container needs the SETUID and SETGID capabilities, or start it directly as that user")
		return err
	}
	logger.Info("engine_user", "uid", identity.UID, "gid", identity.GID, "name", identity.Name)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	store, err := statefile.Open(cfg.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	configLock, err := store.Lock("engine.lock")
	if err != nil {
		return err
	}
	defer configLock.Close()
	versionBytes, err := os.ReadFile("/defaults/cp_version")
	if err != nil {
		return initcfg.ErrInit
	}
	version := strings.TrimSpace(string(versionBytes))
	buildBytes, err := os.ReadFile("/defaults/cp_build")
	if err != nil {
		return initcfg.ErrInit
	}
	build := strings.TrimSpace(string(buildBytes))
	if n, e := strconv.ParseUint(build, 10, 32); e != nil || n == 0 || strconv.FormatUint(n, 10) != build {
		return initcfg.ErrInit
	}
	if err := initcfg.Prepare(initcfg.Options{Root: filepath.Dir(cfg.StateDir), Defaults: "/defaults", Version: version, Heap: os.Getenv("CRASHPLAN_SRV_MAX_MEM"), Logger: logger}); err != nil {
		return err
	}
	// Native CrashPlan serves a self-signed certificate inside this container's
	// private loopback namespace. NewClient forbids non-loopback URLs, DNS,
	// redirects and proxies. No browser or host port can reach this transport.
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, Proxy: nil}
	defer transport.CloseIdleConnections()
	native, err := engine.NewClient("https://127.0.0.1:4243", &http.Client{Transport: transport}, 30*time.Second)
	if err != nil {
		return err
	}
	native.UseSessionStore(store)
	native.SetUserHome(identity.Home)
	defer native.CloseEvents()
	scans, err := engine.NewScans(native, store)
	if err != nil {
		logger.Warn("scan_journal_unavailable")
		scans = nil
	}
	gate, err := sources.New(cfg.SourceRoots, cfg.SourceSentinel, cfg.SourcePolicy)
	if err != nil {
		return config.ErrConfiguration
	}
	console, err := logx.OpenConsole(filepath.Join(filepath.Dir(cfg.StateDir), "log/cph-console"), 10<<20, 3)
	if err != nil {
		return err
	}
	defer console.Close()
	restoreRoots, err := restore.NewDestinations(cfg.RestoreRoots)
	if err != nil {
		return config.ErrConfiguration
	}
	defer restoreRoots.Close()
	helperHash, err := os.ReadFile("/defaults/restore-tool.sha256")
	if err != nil {
		return initcfg.ErrInit
	}
	helpers, err := restore.NewHelpers(restore.HelperOptions{Native: native, Destinations: restoreRoots, Binary: "/usr/local/crashplan/bin/restore-tool", ExpectedSHA: strings.TrimSpace(string(helperHash)), DataDir: filepath.Join(filepath.Dir(cfg.StateDir), "var"), LogDir: filepath.Join(filepath.Dir(cfg.StateDir), "log"), UserName: identity.Name, Console: console, Logger: logger})
	if err != nil {
		return err
	}
	owner := lifecycle.New(lifecycle.Options{Native: native, Scans: scans, Restores: helpers, Logger: logger, Version: version, Build: build})
	restores, err := restore.NewService(native, restoreRoots, store, identity.Name)
	if err != nil {
		logger.Warn("restore_journal_unavailable")
		restores = nil
	}
	localArchives, err := destinations.NewService(native, restoreRoots, store)
	if err != nil {
		return err
	}
	runtime, err := supervise.New(supervise.Options{Command: []string{cfg.Launch}, Environment: config.NativeEnvironment(os.Environ(), identity.Name, identity.Home, cfg.RestoreRoots...), Sources: gate, Generations: owner, Failures: owner.Failures(), Logger: logger, Stdout: console, Stderr: console, StopGrace: cfg.StopGrace})
	if err != nil {
		return err
	}
	var sessions *auth.Manager
	if cfg.Mode == "web" {
		sessions, err = auth.NewOrigins(cfg.WebUsername, cfg.WebPassword, cfg.PublicOrigins, 30*time.Minute)
		if err != nil {
			return config.ErrConfiguration
		}
	}
	assets, err := ui.Handler()
	if err != nil {
		return err
	}
	server := app.New(native, sessions, app.Options{Logger: logger, Context: ctx, Sources: gate, Scans: scans, Runtime: runtime, Assets: assets, Lifecycle: owner, Restores: restores, RestoreHelpers: helpers, LocalArchives: localArchives, TrustedProxies: cfg.TrustedProxies})
	owner.SetNotify(server.Notify)
	listener, err := controlsocket.Listen(cfg.Socket)
	if err != nil {
		return statefile.ErrState
	}
	var wg sync.WaitGroup
	serve := func(listener net.Listener, api http.Handler, tls bool) {
		httpServer := &http.Server{Handler: api, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError)}
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-ctx.Done()
			shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			if httpServer.Shutdown(shutdown) != nil {
				httpServer.Close()
			}
		}()
		go func() {
			defer wg.Done()
			var err error
			if tls {
				err = httpServer.ServeTLS(listener, cfg.TLSCert, cfg.TLSKey)
			} else {
				err = httpServer.Serve(listener)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("control_listener_failed")
				cancel()
			}
		}()
	}
	serve(listener, server.UnixHandler(), false)
	if cfg.Mode == "web" {
		web, err := net.Listen("tcp", cfg.Listen)
		if err != nil {
			cancel()
			wg.Wait()
			return config.ErrConfiguration
		}
		serve(web, server.WebHandler(), cfg.TLSCert != "")
	}
	logger.Info("control_ready", "mode", cfg.Mode)
	err = runtime.Run(ctx)
	cancel()
	wg.Wait()
	return err
}
