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
	"strconv"
	"strings"
	"syscall"
)

var ErrInit = errors.New("CONFIG_INITIALIZATION_FAILED")
var versionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){1,3}(?:[-a-zA-Z0-9.]+)?$`)
var heapPattern = regexp.MustCompile(`^[1-9][0-9]{0,8}[gGmMkK]$`)

// The launcher uses heaps from 1024 MiB to 4 TiB and ignores other values.
const (
	minimumHeapMiB = 1024
	maximumHeapMiB = 4 << 20
)

var hostPattern = regexp.MustCompile(`<serviceHost>[^<]*</serviceHost>`)

type Options struct {
	Root, Defaults, Version, Heap string
	MountInfo                     []byte
	Logger                        *slog.Logger
}

// refuse names the fixed configuration item that stopped initialization.
// Items and reasons are product constants; contents and host paths stay private.
func refuse(logger *slog.Logger, item, reason string) error {
	logger.Error("configuration_item_refused", "item", item, "reason", reason)
	return ErrInit
}

// heapMiB uses the launcher's whole-mebibyte conversion for a validated value.
func heapMiB(heap string) int64 {
	n, _ := strconv.ParseInt(heap[:len(heap)-1], 10, 64)
	switch heap[len(heap)-1] {
	case 'g', 'G':
		return n * 1024
	case 'k', 'K':
		return n / 1024
	}
	return n
}

func Prepare(options Options) error {
	if !filepath.IsAbs(options.Root) || filepath.Clean(options.Root) != options.Root || options.Root == "/" || !versionPattern.MatchString(options.Version) {
		return ErrInit
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	// A heap value that cannot be used is ignored with a warning. The saved
	// value stays unchanged and the launcher falls back to it.
	if options.Heap != "" {
		if !heapPattern.MatchString(options.Heap) {
			options.Logger.Warn("heap_setting_ignored", "item", "CRASHPLAN_SRV_MAX_MEM", "reason", "invalid_format", "expected", "e.g. 4096M or 40G")
			options.Heap = ""
		} else if heapMiB(options.Heap) < minimumHeapMiB || heapMiB(options.Heap) > maximumHeapMiB {
			// Out-of-range values are not saved, so removing the variable later
			// brings back the previous heap.
			options.Logger.Warn("heap_setting_ignored", "item", "CRASHPLAN_SRV_MAX_MEM", "reason", "out_of_range", "minimum", "1024M", "maximum", "4T")
			options.Heap = ""
		}
	}
	raw := options.MountInfo
	if raw == nil {
		var err error
		raw, err = os.ReadFile("/proc/self/mountinfo")
		if err != nil {
			return ErrInit
		}
	}
	if fsType, err := sources.ConfigMountIn(raw, options.Root); err != nil {
		options.Logger.Error("configuration_not_mounted", "hint", "mount a persistent volume at /config")
		return err
	} else if !sources.LocalFilesystem(fsType) {
		options.Logger.Warn("configuration_filesystem_unusual", "filesystem", fsType, "hint", "native databases expect a local filesystem")
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
	} else if err != nil {
		return ErrInit
	} else if !info.IsDir() {
		return refuse(options.Logger, "conf", "not_directory")
	} else if added, err := addMissingDefaults(filepath.Join(options.Defaults, "conf"), conf); err != nil {
		return refuse(options.Logger, "conf", "defaults_not_copied")
	} else if added > 0 {
		// A partial configuration, for example one that holds only jvm_args,
		// would stop the engine at once. Existing files are never replaced.
		options.Logger.Info("configuration_defaults_added", "files", added)
	}
	for _, name := range []string{"log", "cache", "var", "repository", "repository/metadata", ".crashplan", "diag"} {
		path := filepath.Join(options.Root, name)
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return ErrInit
		}
		info, err := os.Lstat(path)
		if err != nil {
			return ErrInit
		} else if !info.IsDir() {
			return refuse(options.Logger, name, "not_directory")
		}
		// The restore helper keeps private pipes in var. Remove group and
		// other write access left by another image's umask.
		if name == "var" && info.Mode().Perm()&0022 != 0 {
			if os.Chmod(path, info.Mode().Perm()&^0022) != nil {
				return refuse(options.Logger, name, "permissions_not_adjustable")
			}
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
	} else if err != nil {
		return ErrInit
	} else if reason := unsafeReason(info); reason != "" {
		return refuse(options.Logger, "machine-id", reason)
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
		saved := filepath.Join(conf, "jvm_args")
		if info, err := os.Lstat(saved); err == nil {
			if reason := unsafeReason(info); reason != "" {
				return refuse(options.Logger, "conf/jvm_args", reason)
			}
		}
		if err := atomicWrite(saved, []byte("-Xmx"+options.Heap+"\n")); err != nil {
			return err
		}
	}
	service := filepath.Join(conf, "my.service.xml")
	if info, err := os.Lstat(service); err == nil {
		if reason := unsafeReason(info); reason != "" {
			return refuse(options.Logger, "conf/my.service.xml", reason)
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
				return refuse(options.Logger, "conf/my.service.xml", "malformed_xml")
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
		if reason := unsafeReason(info); reason != "" {
			return refuse(options.Logger, "cp_version", reason)
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

// addMissingDefaults copies each top-level default file that is absent from
// conf. Existing entries of any type stay untouched.
func addMissingDefaults(source, conf string) (int, error) {
	entries, err := os.ReadDir(source)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, ErrInit
	}
	added := 0
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		target := filepath.Join(conf, entry.Name())
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return added, ErrInit
		}
		if err := atomicWrite(target, data); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
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

// unsafeReason explains why an existing file is not read or replaced. Only a
// regular file is used: a symlink or directory at a configuration file name is
// left alone. Owner and link count do not matter; a replacement is a new file.
func unsafeReason(info os.FileInfo) string {
	if !info.Mode().IsRegular() {
		return "not_regular_file"
	}
	return ""
}
func safeRegular(info os.FileInfo) bool { return unsafeReason(info) == "" }

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
