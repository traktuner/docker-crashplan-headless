// Package sources verifies declared backup mounts before engine filesystem access.
package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
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
	SourceEmpty        Reason = "SOURCE_EMPTY"
	SourceChanged      Reason = "SOURCE_CHANGED"
)

// IdentityRecord is the state record that remembers what each source was.
const IdentityRecord = "source-identities.json"

// PinStore keeps the remembered source identities across restarts.
type PinStore interface {
	Read(name string) ([]byte, error)
	Write(name string, raw []byte) error
}

// pin is what must stay the same for a source between starts: the filesystem
// type and the path inside that filesystem. A host share that is not mounted
// leaves its parent filesystem behind, which differs in one of the two.
// Device numbers are left out because they change across reboots.
type pin struct {
	FS   string `json:"filesystem"`
	Root string `json:"root"`
}

// DefaultBase is where sources are discovered when CPH_SOURCE_ROOTS is unset.
const DefaultBase = "/storage"

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
	pins     PinStore
	changed  string
}

// UsePins remembers each source's identity in store and refuses a later
// start in which a source comes from a different filesystem or path.
func (g *Gate) UsePins(store PinStore) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pins = store
}

// checkPins compares the current mounts with the remembered ones and records
// sources seen for the first time. A record that cannot be read or written
// does not stop the engine; it only disables this extra check.
func (g *Gate) checkPins(identities []identity) error {
	if g.pins == nil {
		return nil
	}
	known := map[string]pin{}
	if raw, err := g.pins.Read(IdentityRecord); err == nil {
		if json.Unmarshal(raw, &known) != nil {
			known = map[string]pin{}
		}
	}
	changed := false
	for i, root := range g.roots {
		current := pin{FS: filesystemFamily(identities[i].Mount.FS), Root: identities[i].Mount.Root}
		if saved, ok := known[root]; ok {
			if saved != current {
				g.changed = root
				return SourceChanged
			}
			continue
		}
		known[root] = current
		changed = true
	}
	if changed {
		if raw, err := json.Marshal(known); err == nil {
			_ = g.pins.Write(IdentityRecord, raw)
		}
	}
	return nil
}

// filesystemFamily treats NFS versions as one filesystem, so a protocol
// upgrade of the same share is not a different source.
func filesystemFamily(fs string) string {
	if fs == "nfs4" {
		return "nfs"
	}
	return fs
}

// Changed names the source whose identity differs from the remembered one.
func (g *Gate) Changed() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.changed
}

func New(roots []string, sentinel string, policies ...string) (*Gate, error) {
	policy := "any"
	if len(policies) > 1 {
		return nil, ErrConfiguration
	}
	if len(policies) == 1 && policies[0] != "" {
		policy = policies[0]
	}
	if policy != "local" && policy != "mounted" && policy != "any" {
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

// Pseudo and in-memory filesystems never hold backup data. A source with one
// of these types is a missing bind, not a real source.
var pseudoFilesystems = map[string]bool{
	"proc": true, "sysfs": true, "tmpfs": true, "devtmpfs": true, "devpts": true, "ramfs": true,
	"overlay": true, "autofs": true, "cgroup": true, "cgroup2": true, "mqueue": true, "nsfs": true,
	"securityfs": true, "debugfs": true, "tracefs": true, "bpf": true, "configfs": true, "fusectl": true,
	"hugetlbfs": true, "pstore": true, "binfmt_misc": true, "efivarfs": true, "rpc_pipefs": true,
}

func LocalFilesystem(fs string) bool {
	switch fs {
	case "zfs", "ext4", "xfs", "btrfs":
		return true
	}
	return false
}

func admittedMount(raw []byte, path, policy string) (mountRecord, error) {
	m, err := parseMount(raw, path)
	if err != nil {
		return m, err
	}
	switch {
	case LocalFilesystem(m.FS):
		return m, nil
	case m.FS == "nfs" || m.FS == "nfs4" || m.FS == "cifs":
		if policy != "local" {
			return m, nil
		}
	case policy == "any" && !pseudoFilesystems[m.FS]:
		return m, nil
	}
	return mountRecord{}, FilesystemRejected
}
func selectMount(raw []byte, path string) (mountRecord, error) {
	return admittedMount(raw, path, "local")
}
func (g *Gate) sourceMount(raw []byte, path string) (mountRecord, error) {
	return admittedMount(raw, path, g.policy)
}

// requiresContent reports whether a source must be non-empty to be ready. A
// subdirectory bind without a sentinel is accepted when it has content: a
// missing host mount leaves an empty directory behind.
func requiresContent(mount mountRecord, sentinel string) bool {
	return mount.Root != "/" && sentinel == ""
}

// ConfigMountIn reports the filesystem of the configuration mount. The path
// must be its own mount, so container removal cannot discard the device identity.
func ConfigMountIn(raw []byte, path string) (string, error) {
	m, err := parseMount(raw, path)
	if err != nil {
		return "", err
	}
	if pseudoFilesystems[m.FS] {
		return m.FS, FilesystemRejected
	}
	return m.FS, nil
}

// DetectRoots returns every real mount at or below base, sorted. Nested mounts
// are listed too, so each one is checked before the engine starts.
// Without such a mount it returns base itself, which the gate then reports as
// not mounted until a volume appears there.
func DetectRoots(raw []byte, base string) []string {
	roots, _ := DetectRootsReport(raw, base)
	return roots
}

// DetectRootsReport also returns the mounts below base that are not used:
// pseudo filesystems and mounts beyond the 128-source limit.
func DetectRootsReport(raw []byte, base string) ([]string, []string) {
	var skipped []string
	var found []string
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), " ")
		separator := slices.Index(fields, "-")
		if len(fields) < 10 || separator < 6 || len(fields) < separator+4 {
			continue
		}
		point := unescape(fields[4])
		if pseudoFilesystems[fields[separator+1]] {
			if point == base || strings.HasPrefix(point, base+"/") {
				skipped = append(skipped, point)
			}
			continue
		}
		if (point == base || strings.HasPrefix(point, base+"/")) && !slices.Contains(found, point) && !strings.ContainsAny(point, "\x00\r\n") {
			found = append(found, point)
		}
	}
	slices.Sort(found)
	if len(found) == 0 {
		return []string{base}, skipped
	}
	if len(found) > 128 {
		// The outermost mounts come first in sorted order of their parents;
		// keep the limit and report the rest instead of dropping every check.
		skipped = append(skipped, found[128:]...)
		found = found[:128]
	}
	return found, skipped
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
//
// A definite state is a Reason (not mounted, wrong filesystem, missing marker,
// empty, changed identity or path). A plain ErrNotReady is an ambiguous I/O
// failure on a mounted source (for example ESTALE or EIO on NFS), which the
// supervisor may debounce.
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
		fd, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return ErrNotReady
		}
		dir := os.NewFile(uintptr(fd), "source-root")
		var stat syscall.Stat_t
		var fs syscall.Statfs_t
		statErr := syscall.Fstat(fd, &stat)
		fsErr := syscall.Fstatfs(fd, &fs)
		magic, known := filesystemMagic[mount.FS]
		if statErr != nil || fsErr != nil {
			dir.Close()
			return ErrNotReady
		}
		if known && int64(fs.Type) != magic {
			dir.Close()
			return FilesystemRejected
		}
		if g.sentinel != "" {
			sentinelFD, e := syscall.Openat(fd, g.sentinel, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
			if e != nil {
				dir.Close()
				if e == syscall.ENOENT || e == syscall.ELOOP || e == syscall.ENOTDIR {
					return SentinelMissing
				}
				return ErrNotReady
			}
			var sentinelStat syscall.Stat_t
			e = syscall.Fstat(sentinelFD, &sentinelStat)
			syscall.Close(sentinelFD)
			if e != nil || sentinelStat.Mode&syscall.S_IFMT != syscall.S_IFREG {
				dir.Close()
				return SentinelMissing
			}
		}
		names, readErr := dir.Readdirnames(1)
		closeErr := dir.Close()
		if (readErr != nil && !errors.Is(readErr, io.EOF)) || closeErr != nil {
			return ErrNotReady
		}
		if requiresContent(mount, g.sentinel) && len(names) == 0 {
			return SourceEmpty
		}
		identities = append(identities, identity{mount, uint64(stat.Dev), stat.Ino})
	}
	post, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ErrNotReady
	}
	for i, root := range g.roots {
		m, err := g.sourceMount(post, root)
		if err != nil {
			return err
		}
		if m != identities[i].Mount {
			return IdentityChanged
		}
	}
	if err := g.checkPins(identities); err != nil {
		return err
	}
	if g.bound == nil {
		g.bound = identities
	} else if !slices.Equal(g.bound, identities) {
		return IdentityChanged
	}
	return nil
}
