// Package sources verifies declared backup mounts before engine filesystem access.
package sources

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
)

var ErrNotReady = errors.New("SOURCE_NOT_READY")
var ErrConfiguration = errors.New("INVALID_SOURCE_CONFIGURATION")

type Reason string

func (r Reason) Error() string        { return string(r) }
func (r Reason) Is(target error) bool { return target == ErrNotReady }

const (
	NotMounted         Reason = "SOURCE_NOT_MOUNTED"
	FilesystemRejected Reason = "SOURCE_FILESYSTEM_REJECTED"
	SentinelMissing    Reason = "SOURCE_SENTINEL_MISSING"
	IdentityChanged    Reason = "SOURCE_IDENTITY_CHANGED"
)

type mountRecord struct{ ID, Device, Root, Point, FS string }
type identity struct {
	Mount         mountRecord
	Device, Inode uint64
}
type Gate struct {
	mu       sync.Mutex
	roots    []string
	sentinel string
	policy   string
	bound    []identity
}

func New(roots []string, sentinel string, policies ...string) (*Gate, error) {
	policy := "local"
	if len(policies) > 1 {
		return nil, ErrConfiguration
	}
	if len(policies) == 1 && policies[0] != "" {
		policy = policies[0]
	}
	if policy != "local" && policy != "mounted" {
		return nil, ErrConfiguration
	}
	if len(roots) == 0 || len(roots) > 128 {
		return nil, ErrConfiguration
	}
	seen := map[string]bool{}
	for _, root := range roots {
		if !filepath.IsAbs(root) || root == "/" || filepath.Clean(root) != root || seen[root] || strings.ContainsAny(root, "\x00\r\n") {
			return nil, ErrConfiguration
		}
		seen[root] = true
	}
	if sentinel != "" && (filepath.Base(sentinel) != sentinel || sentinel == "." || sentinel == ".." || strings.ContainsAny(sentinel, "\x00\r\n")) {
		return nil, ErrConfiguration
	}
	return &Gate{roots: slices.Clone(roots), sentinel: sentinel, policy: policy}, nil
}

func unescape(value string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
}
func parseMount(raw []byte, path string) (mountRecord, error) {
	var selected mountRecord
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), " ")
		separator := slices.Index(fields, "-")
		if len(fields) < 10 || separator < 6 || len(fields) < separator+4 {
			return mountRecord{}, ErrNotReady
		}
		if unescape(fields[4]) != path {
			continue
		}
		if found {
			return mountRecord{}, ErrNotReady
		}
		found = true
		selected = mountRecord{fields[0], fields[2], unescape(fields[3]), path, fields[separator+1]}
	}
	if scanner.Err() != nil || !found {
		return mountRecord{}, NotMounted
	}
	return selected, nil
}

var filesystemMagic = map[string]int64{
	"zfs": 0x2fc12fc1, "ext4": 0xef53, "xfs": 0x58465342, "btrfs": 0x9123683e,
	"nfs": 0x6969, "nfs4": 0x6969, "cifs": 0xff534d42,
}

func admittedMount(raw []byte, path string, remote bool) (mountRecord, error) {
	m, err := parseMount(raw, path)
	if err != nil {
		return m, err
	}
	switch m.FS {
	case "zfs", "ext4", "xfs", "btrfs":
		return m, nil
	case "nfs", "nfs4", "cifs":
		if remote {
			return m, nil
		}
	}
	return mountRecord{}, FilesystemRejected
}
func selectMount(raw []byte, path string) (mountRecord, error) {
	return admittedMount(raw, path, false)
}
func (g *Gate) sourceMount(raw []byte, path string) (mountRecord, error) {
	return admittedMount(raw, path, g.policy == "mounted")
}

func sourceRequirement(mount mountRecord, sentinel string) error {
	if mount.Root != "/" && sentinel == "" {
		return SentinelMissing
	}
	return nil
}

// MountedLocal verifies a local mountpoint without opening its filesystem.
// Config initialization uses the same kernel-field parser as source checks.
func MountedLocal(path string) error {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return NotMounted
	}
	_, err = selectMount(raw, path)
	return err
}
func LocalMountIn(raw []byte, path string) error { _, err := selectMount(raw, path); return err }

func (g *Gate) Roots() []string { return slices.Clone(g.roots) }

// Check reads mountinfo before touching any source. The first ready identities
// remain bound for this daemon lifetime; source replacement requires a restart.
func (g *Gate) Check() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ErrNotReady
	}
	identities := make([]identity, 0, len(g.roots))
	for _, root := range g.roots {
		mount, err := g.sourceMount(raw, root)
		if err != nil {
			return err
		}
		if err := sourceRequirement(mount, g.sentinel); err != nil {
			return err
		}
		fd, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return ErrNotReady
		}
		dir := os.NewFile(uintptr(fd), "source-root")
		var stat syscall.Stat_t
		var fs syscall.Statfs_t
		statErr := syscall.Fstat(fd, &stat)
		fsErr := syscall.Fstatfs(fd, &fs)
		if statErr != nil || fsErr != nil || int64(fs.Type) != filesystemMagic[mount.FS] {
			dir.Close()
			return ErrNotReady
		}
		if g.sentinel != "" {
			sentinelFD, e := syscall.Openat(fd, g.sentinel, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
			if e != nil {
				dir.Close()
				return SentinelMissing
			}
			var sentinelStat syscall.Stat_t
			e = syscall.Fstat(sentinelFD, &sentinelStat)
			syscall.Close(sentinelFD)
			if e != nil || sentinelStat.Mode&syscall.S_IFMT != syscall.S_IFREG {
				dir.Close()
				return SentinelMissing
			}
		}
		_, readErr := dir.Readdirnames(1)
		closeErr := dir.Close()
		if (readErr != nil && !errors.Is(readErr, io.EOF)) || closeErr != nil {
			return IdentityChanged
		}
		identities = append(identities, identity{mount, uint64(stat.Dev), stat.Ino})
	}
	post, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return IdentityChanged
	}
	for i, root := range g.roots {
		m, err := g.sourceMount(post, root)
		if err != nil || m != identities[i].Mount {
			return ErrNotReady
		}
	}
	if g.bound == nil {
		g.bound = identities
	} else if !slices.Equal(g.bound, identities) {
		return ErrNotReady
	}
	return nil
}
