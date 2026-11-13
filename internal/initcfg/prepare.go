// Package initcfg prepares persistent configuration without replacing existing
// settings or deleting cache data during an image update.
package initcfg

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"github.com/traktuner/crashplan-headless/internal/sources"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

var ErrInit = errors.New("CONFIG_INITIALIZATION_FAILED")
var versionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){1,3}(?:[-a-zA-Z0-9.]+)?$`)
var heapPattern = regexp.MustCompile(`^[1-9][0-9]{0,8}[gGmMkK]$`)
var hostPattern = regexp.MustCompile(`<serviceHost>[^<]*</serviceHost>`)

type Options struct {
	Root, Defaults, Version, Heap string
	MountInfo                     []byte
	Logger                        *slog.Logger
}

func Prepare(options Options) error {
	if !filepath.IsAbs(options.Root) || filepath.Clean(options.Root) != options.Root || options.Root == "/" || !versionPattern.MatchString(options.Version) || (options.Heap != "" && !heapPattern.MatchString(options.Heap)) {
		return ErrInit
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	raw := options.MountInfo
	if raw == nil {
		var err error
		raw, err = os.ReadFile("/proc/self/mountinfo")
		if err != nil {
			return ErrInit
		}
	}
	if err := sources.LocalMountIn(raw, options.Root); err != nil {
		return err
	}
	info, err := os.Lstat(options.Root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInit
	}
	conf := filepath.Join(options.Root, "conf")
	if info, err := os.Lstat(conf); errors.Is(err, os.ErrNotExist) {
		stage, err := os.MkdirTemp(options.Root, ".cph-conf-stage-")
		if err != nil {
			return ErrInit
		}
		if err := copyDefaults(filepath.Join(options.Defaults, "conf"), stage); err != nil {
			os.RemoveAll(stage)
			return err
		}
		if _, err := os.Lstat(conf); !errors.Is(err, os.ErrNotExist) {
			os.RemoveAll(stage)
			return ErrInit
		}
		if err := os.Rename(stage, conf); err != nil {
			os.RemoveAll(stage)
			return ErrInit
		}
		options.Logger.Info("configuration_seeded")
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInit
	}
	for _, name := range []string{"log", "cache", "var", "repository", "repository/metadata", ".crashplan", "diag"} {
		path := filepath.Join(options.Root, name)
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return ErrInit
		}
		if info, err := os.Lstat(path); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInit
		}
	}
	identity := filepath.Join(options.Root, "machine-id")
	if info, err := os.Lstat(identity); errors.Is(err, os.ErrNotExist) {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return ErrInit
		}
		if err := atomicWrite(identity, []byte(hex.EncodeToString(id[:]))); err != nil {
			return err
		}
	} else if err != nil || !safeRegular(info) {
		return ErrInit
	}
	logLink := filepath.Join(options.Root, ".crashplan/log")
	if _, err := os.Lstat(logLink); errors.Is(err, os.ErrNotExist) {
		if os.Symlink(filepath.Join(options.Root, "log"), logLink) != nil {
			return ErrInit
		}
	} else if err != nil {
		return ErrInit
	}
	if options.Heap != "" {
		if err := atomicWrite(filepath.Join(conf, "jvm_args"), []byte("-Xmx"+options.Heap+"\n")); err != nil {
			return err
		}
	}
	service := filepath.Join(conf, "my.service.xml")
	if info, err := os.Lstat(service); err == nil {
		if !safeRegular(info) {
			return ErrInit
		}
		data, err := os.ReadFile(service)
		if err != nil {
			return ErrInit
		}
		decoder := xml.NewDecoder(bytes.NewReader(data))
		for {
			_, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return ErrInit
			}
		}
		updated := hostPattern.ReplaceAll(data, []byte("<serviceHost>127.0.0.1</serviceHost>"))
		if !bytes.Equal(data, updated) {
			if err := atomicWrite(service, updated); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrInit
	}
	marker := filepath.Join(options.Root, "cp_version")
	if info, err := os.Lstat(marker); err == nil {
		if !safeRegular(info) {
			return ErrInit
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrInit
	}
	previous, err := os.ReadFile(marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrInit
	}
	if strings.TrimSpace(string(previous)) != options.Version {
		if err := atomicWrite(marker, []byte(options.Version+"\n")); err != nil {
			return err
		}
		old := strings.TrimSpace(string(previous))
		if !versionPattern.MatchString(old) {
			old = "unknown"
		}
		options.Logger.Info("configuration_version_changed", "previous_version", old, "version", options.Version)
	}
	return nil
}

func copyDefaults(source, destination string) error {
	if info, err := os.Lstat(source); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInit
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return ErrInit
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return ErrInit
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			if os.Mkdir(target, 0700) != nil {
				return ErrInit
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return ErrInit
		}
		input, err := os.Open(path)
		if err != nil {
			return ErrInit
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			input.Close()
			return ErrInit
		}
		_, copyErr := io.Copy(output, input)
		err = errors.Join(copyErr, input.Close(), output.Sync(), output.Close())
		if err != nil {
			return ErrInit
		}
		return nil
	})
}
func safeRegular(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}
func atomicWrite(path string, data []byte) error {
	before, err := os.Lstat(path)
	absent := errors.Is(err, os.ErrNotExist)
	if !absent && (err != nil || !safeRegular(before)) {
		return ErrInit
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".cph-init-")
	if err != nil {
		return ErrInit
	}
	name := file.Name()
	defer os.Remove(name)
	_, writeErr := file.Write(data)
	if errors.Join(writeErr, file.Sync(), file.Close()) != nil {
		return ErrInit
	}
	after, err := os.Lstat(path)
	if absent {
		if !errors.Is(err, os.ErrNotExist) {
			return ErrInit
		}
	} else if err != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || before.Size() != after.Size() {
		return ErrInit
	}
	if os.Rename(name, path) != nil {
		return ErrInit
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return ErrInit
	}
	err = errors.Join(parent.Sync(), parent.Close())
	if err != nil {
		return ErrInit
	}
	return nil
}
