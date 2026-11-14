// Package config parses the product's environment without logging its values.
package config

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/traktuner/crashplan-headless/internal/sources"
)

var ErrConfiguration = errors.New("INVALID_CONFIGURATION")

// VariableError names the environment variable that has an unusable value.
// The value itself is never included.
type VariableError struct{ Name string }

func (e *VariableError) Error() string        { return "INVALID_CONFIGURATION: " + e.Name }
func (e *VariableError) Is(target error) bool { return target == ErrConfiguration }

func invalid(name string) error { return &VariableError{Name: name} }

// readMountInfo is replaced in tests.
var readMountInfo = func() ([]byte, error) { return os.ReadFile("/proc/self/mountinfo") }

type Config struct {
	Mode, Listen, Socket, StateDir, Launch, WebUsername, WebPassword, TLSCert, TLSKey string
	PublicOrigins, SourceRoots, RestoreRoots                                          []string
	SourceSentinel, SourcePolicy, LogFormat                                           string
	LogLevel                                                                          slog.Level
	SourceRootsDetected                                                               bool
	StopGrace                                                                         time.Duration
	TrustedProxies                                                                    []netip.Prefix
}

func (c Config) String() string   { return "config.Config{private}" }
func (c Config) GoString() string { return c.String() }

func Load(get func(string) string) (Config, error) {
	c := Config{StopGrace: 120 * time.Second, Mode: get("CPH_MODE"), StateDir: "/config/cph", Socket: "/run/cph/control.sock", Launch: "/usr/local/bin/cph-launch", WebUsername: get("CPH_WEB_USERNAME"), SourceSentinel: get("CPH_SOURCE_SENTINEL"), SourcePolicy: get("CPH_SOURCE_POLICY"), LogFormat: get("CPH_LOG_FORMAT"), TLSCert: get("CPH_TLS_CERT_FILE"), TLSKey: get("CPH_TLS_KEY_FILE")}
	if value := get("CPH_STOP_GRACE_SECONDS"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 || seconds > 240 {
			return Config{}, invalid("CPH_STOP_GRACE_SECONDS")
		}
		c.StopGrace = time.Duration(seconds) * time.Second
	}
	// The image's native paths and persistent symlinks are rooted at /config.
	if dir := get("CPH_CONFIG_DIR"); dir != "" && dir != "/config" {
		return Config{}, invalid("CPH_CONFIG_DIR")
	}
	if dir := get("CPH_RUN_DIR"); dir != "" {
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
			return Config{}, invalid("CPH_RUN_DIR")
		}
		c.Socket = filepath.Join(dir, "control.sock")
	}
	if c.Mode == "" {
		c.Mode = "headless"
	}
	if c.Mode != "headless" && c.Mode != "web" {
		return Config{}, invalid("CPH_MODE")
	}
	if c.WebUsername == "" {
		c.WebUsername = "admin"
	}
	if c.LogFormat == "" {
		c.LogFormat = "json"
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		return Config{}, invalid("CPH_LOG_FORMAT")
	}
	level := get("CPH_LOG_LEVEL")
	if level == "" {
		level = "INFO"
	}
	if c.LogLevel.UnmarshalText([]byte(level)) != nil {
		return Config{}, invalid("CPH_LOG_LEVEL")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return Config{}, invalid("CPH_TLS_CERT_FILE")
	}
	if value := get("CPH_SOURCE_ROOTS"); value != "" {
		if json.Unmarshal([]byte(value), &c.SourceRoots) != nil {
			return Config{}, invalid("CPH_SOURCE_ROOTS")
		}
	} else {
		// Without an explicit list, every volume mounted under /storage is a source.
		raw, _ := readMountInfo()
		c.SourceRoots = sources.DetectRoots(raw, sources.DefaultBase)
		c.SourceRootsDetected = true
	}
	if _, err := sources.New(c.SourceRoots, c.SourceSentinel, c.SourcePolicy); err != nil {
		return Config{}, invalid("CPH_SOURCE_ROOTS, CPH_SOURCE_POLICY or CPH_SOURCE_SENTINEL")
	}
	if value := get("CPH_RESTORE_ROOTS"); value != "" {
		if json.Unmarshal([]byte(value), &c.RestoreRoots) != nil {
			return Config{}, invalid("CPH_RESTORE_ROOTS")
		}
	}
	for _, root := range c.RestoreRoots {
		if !filepath.IsAbs(root) || root == "/" || filepath.Clean(root) != root || strings.ContainsAny(root, "\x00\r\n") {
			return Config{}, invalid("CPH_RESTORE_ROOTS")
		}
	}
	if c.Mode == "headless" {
		return c, nil
	}
	if value := get("CPH_TRUSTED_PROXIES"); value != "" {
		var prefixes []string
		if json.Unmarshal([]byte(value), &prefixes) != nil || len(prefixes) > 32 {
			return Config{}, invalid("CPH_TRUSTED_PROXIES")
		}
		for _, value := range prefixes {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return Config{}, invalid("CPH_TRUSTED_PROXIES")
			}
			c.TrustedProxies = append(c.TrustedProxies, prefix.Masked())
		}
	}
	c.Listen = get("CPH_LISTEN")
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return Config{}, invalid("CPH_LISTEN")
	}
	if json.Unmarshal([]byte(get("CPH_PUBLIC_ORIGINS")), &c.PublicOrigins) != nil || len(c.PublicOrigins) == 0 || len(c.PublicOrigins) > 8 {
		return Config{}, invalid("CPH_PUBLIC_ORIGINS")
	}
	for _, origin := range c.PublicOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return Config{}, invalid("CPH_PUBLIC_ORIGINS")
		}
	}
	c.WebPassword = get("CPH_WEB_PASSWORD")
	if path := get("CPH_WEB_PASSWORD_FILE"); path != "" {
		if c.WebPassword != "" {
			return Config{}, invalid("CPH_WEB_PASSWORD_FILE")
		}
		fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		if err != nil {
			return Config{}, invalid("CPH_WEB_PASSWORD_FILE")
		}
		file := os.NewFile(uintptr(fd), "web-password")
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			return Config{}, invalid("CPH_WEB_PASSWORD_FILE")
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, 65537))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(raw) > 65536 {
			return Config{}, invalid("CPH_WEB_PASSWORD_FILE")
		}
		c.WebPassword = strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	}
	if len(c.WebPassword) < 12 || len(c.WebPassword) > 65536 || len(c.WebUsername) > 256 {
		return Config{}, invalid("CPH_WEB_PASSWORD")
	}
	return c, nil
}
