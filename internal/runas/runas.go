// Package runas selects the user the daemon and the engine run as.
//
// The container starts as root by default. USER_ID, GROUP_ID, SUP_GROUP_IDS
// and UMASK follow the convention of other CrashPlan images: the daemon takes
// ownership of /config for that user and then drops root privileges, so the
// engine runs as that user. A container started directly as a non-root user
// (Docker --user, Compose user:) runs as that user without changes.
package runas

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var ErrPrivileges = errors.New("USER_SWITCH_FAILED")

// Identity is the user the daemon and the engine run as.
type Identity struct {
	UID, GID int
	Groups   []int
	Name     string
	Home     string
	Umask    int // -1 keeps the inherited umask
	// Switch is true when the daemon starts as root and must drop to UID/GID.
	Switch bool
}

func (i Identity) Root() bool { return i.UID == 0 }

// Resolve reads the user settings. Unusable values are reported as warnings
// and replaced by the default; they never stop the container.
func Resolve(get func(string) string, logger *slog.Logger) Identity {
	id := Identity{UID: os.Geteuid(), GID: os.Getegid(), Umask: -1}
	if value := get("UMASK"); value != "" {
		mask, err := strconv.ParseUint(value, 8, 32)
		switch {
		case err != nil || mask > 0777:
			logger.Warn("user_setting_ignored", "item", "UMASK", "reason", "invalid_octal")
		case mask&0700 != 0:
			// The owner must keep full access to its own private files.
			logger.Warn("user_setting_ignored", "item", "UMASK", "reason", "removes_owner_access")
		default:
			id.Umask = int(mask)
		}
	}
	userID, groupID := get("USER_ID"), get("GROUP_ID")
	if id.UID != 0 {
		// The container runtime already selected the user.
		if (userID != "" && userID != strconv.Itoa(id.UID)) || (groupID != "" && groupID != strconv.Itoa(id.GID)) {
			logger.Warn("user_setting_ignored", "item", "USER_ID/GROUP_ID", "reason", "container_runs_as_another_user", "uid", id.UID, "gid", id.GID)
		}
		id.Groups, _ = os.Getgroups()
		id.Name, id.Home = names(id.UID)
		return id
	}
	parse := func(item, value string, fallback int) int {
		if value == "" {
			return fallback
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 || n > 1<<31-2 {
			logger.Warn("user_setting_ignored", "item", item, "reason", "invalid_number")
			return fallback
		}
		return n
	}
	id.UID = parse("USER_ID", userID, 0)
	// GROUP_ID follows USER_ID when only the user is given.
	id.GID = parse("GROUP_ID", groupID, id.UID)
	for _, value := range strings.Split(get("SUP_GROUP_IDS"), ",") {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if g := parse("SUP_GROUP_IDS", value, -1); g >= 0 {
			id.Groups = append(id.Groups, g)
		}
	}
	id.Switch = id.UID != 0 || id.GID != 0 || len(id.Groups) > 0
	id.Name, id.Home = names(id.UID)
	return id
}

// names returns the account name and the home the engine uses. A user without
// an account in the image is named by its number; its home is /config, which
// holds the same .crashplan directory that root reaches through /root.
func names(uid int) (string, string) {
	if uid == 0 {
		return "root", "/root"
	}
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil && u.Username != "" {
		return u.Username, "/config"
	}
	// Java names a user without an account by its number. The engine reports
	// that name for restore jobs, so the daemon must use the same one.
	return strconv.Itoa(uid), "/config"
}

// TakeOwnership gives every entry below root to uid:gid. Entries that cannot
// be changed, for example on a share that maps root to another user, are
// counted and reported once; the engine then decides whether it can use them.
func TakeOwnership(root string, uid, gid int, logger *slog.Logger) {
	changed, failed := 0, 0
	var rootDevice uint64
	if info, err := os.Lstat(root); err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			rootDevice = uint64(stat.Dev)
		}
	}
	filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			failed++
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			failed++
			return nil
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		// A volume mounted below /config is someone else's; leave it alone.
		if uint64(stat.Dev) != rootDevice {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if int(stat.Uid) == uid && int(stat.Gid) == gid {
			return nil
		}
		if os.Lchown(path, uid, gid) != nil {
			failed++
			return nil
		}
		changed++
		return nil
	})
	if changed > 0 {
		logger.Info("configuration_ownership_adjusted", "entries", changed, "uid", uid, "gid", gid)
	}
	if failed > 0 {
		logger.Warn("configuration_ownership_incomplete", "entries", failed, "uid", uid, "gid", gid)
	}
}

// Busy reports whether an engine holds the lock file at path. Only a lock
// conflict counts; any other error is left to the state store's own lock.
// The file is opened for writing because NFS emulates flock with write locks.
func Busy(path string) bool {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer syscall.Close(fd)
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
	}
	syscall.Flock(fd, syscall.LOCK_UN)
	return false
}

// PrepareDir creates a directory for the target user before privileges drop.
func PrepareDir(path string, id Identity) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return os.Lchown(path, id.UID, id.GID)
}

// Apply sets the umask and, when required, drops root privileges for every
// thread of the process. It runs before any other goroutine starts work.
func Apply(id Identity) error {
	if id.Umask >= 0 {
		syscall.Umask(id.Umask)
	}
	if !id.Switch {
		return nil
	}
	groups := id.Groups
	if groups == nil {
		groups = []int{}
	}
	if syscall.Setgroups(groups) != nil || syscall.Setgid(id.GID) != nil || syscall.Setuid(id.UID) != nil {
		return ErrPrivileges
	}
	if os.Geteuid() != id.UID || os.Getegid() != id.GID {
		return ErrPrivileges
	}
	return nil
}
