// Package restore owns product restore admission and the native helper lifecycle.
package restore

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var ErrDestination = errors.New("RESTORE_DESTINATION_REJECTED")
var ErrMarker = errors.New("RESTORE_MARKER_REJECTED")

type root struct {
	path     string
	file     *os.File
	identity os.FileInfo
}
type Destinations struct {
	mu     sync.Mutex
	roots  []root
	closed bool
}

func NewDestinations(paths []string) (*Destinations, error) {
	if len(paths) > 128 {
		return nil, ErrDestination
	}
	d := &Destinations{}
	seen := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsAny(path, "\x00\r\n") || seen[path] {
			return nil, ErrDestination
		}
		seen[path] = true
		d.roots = append(d.roots, root{path: path})
	}
	return d, nil
}
func (d *Destinations) Paths() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.roots))
	for i, r := range d.roots {
		out[i] = r.path
	}
	return out
}
func openDirectory(path string) (*os.File, error) {
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrDestination
	}
	current := os.NewFile(uintptr(fd), "restore-directory")
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if component == "" {
			continue
		}
		next, e := syscall.Openat(int(current.Fd()), component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		current.Close()
		if e != nil {
			return nil, ErrDestination
		}
		current = os.NewFile(uintptr(next), "restore-directory")
	}
	return current, nil
}
func openRelative(parent *os.File, relative string) (*os.File, error) {
	fd, err := syscall.Dup(int(parent.Fd()))
	if err != nil {
		return nil, ErrDestination
	}
	syscall.CloseOnExec(fd)
	current := os.NewFile(uintptr(fd), "restore-destination")
	if relative == "" || relative == "." {
		return current, nil
	}
	for _, component := range strings.Split(relative, "/") {
		next, e := syscall.Openat(int(current.Fd()), component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		current.Close()
		if e != nil {
			return nil, ErrDestination
		}
		current = os.NewFile(uintptr(next), "restore-destination")
	}
	return current, nil
}

type Destination struct {
	path, root, relative   string
	file                   *os.File
	identity, rootIdentity os.FileInfo
}

func (d *Destination) Path() string { return d.path }
func (d *Destination) Close() error { return d.file.Close() }
func (d *Destination) Validate() error {
	r, err := openDirectory(d.root)
	if err != nil {
		return ErrDestination
	}
	defer r.Close()
	info, err := r.Stat()
	if err != nil || !os.SameFile(info, d.rootIdentity) {
		return ErrDestination
	}
	f, err := openRelative(r, d.relative)
	if err != nil {
		return ErrDestination
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !os.SameFile(info, d.identity) {
		return ErrDestination
	}
	return nil
}
func (d *Destination) Writable() bool {
	var fs syscall.Statfs_t
	return syscall.Fstatfs(int(d.file.Fd()), &fs) == nil && fs.Flags&1 == 0
}
func (d *Destinations) Resolve(id, relative string) (*Destination, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	index, err := strconv.Atoi(id)
	if err != nil || strconv.Itoa(index) != id || index < 0 || index >= len(d.roots) || d.closed || (relative != "" && relative != "." && (filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, "../"))) || strings.ContainsAny(relative, "\x00\r\n") {
		return nil, ErrDestination
	}
	r := &d.roots[index]
	named, err := openDirectory(r.path)
	if err != nil {
		return nil, ErrDestination
	}
	info, err := named.Stat()
	if err != nil {
		named.Close()
		return nil, ErrDestination
	}
	if r.file == nil {
		r.file = named
		r.identity = info
	} else {
		named.Close()
		if !os.SameFile(info, r.identity) {
			return nil, ErrDestination
		}
	}
	f, err := openRelative(r.file, relative)
	if err != nil {
		return nil, ErrDestination
	}
	info, err = f.Stat()
	if err != nil {
		f.Close()
		return nil, ErrDestination
	}
	return &Destination{path: filepath.Join(r.path, relative), root: r.path, relative: relative, file: f, identity: info, rootIdentity: r.identity}, nil
}
func (d *Destinations) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	var errs []error
	for i := range d.roots {
		if d.roots[i].file != nil {
			errs = append(errs, d.roots[i].file.Close())
			d.roots[i].file = nil
		}
	}
	return errors.Join(errs...)
}

var markerName = regexp.MustCompile(`^[0-9]{1,20}\.txt$`)

type Marker struct {
	mu           sync.Mutex
	parent, file *os.File
	name         string
	identity     os.FileInfo
	closed       bool
	closeErr     error
}

func CreateMarker(path string) (*Marker, error) {
	if filepath.Dir(path) != "/tmp" || !markerName.MatchString(filepath.Base(path)) {
		return nil, ErrMarker
	}
	return createMarker("/tmp", filepath.Base(path))
}
func createMarker(parent, name string) (*Marker, error) {
	if !markerName.MatchString(name) {
		return nil, ErrMarker
	}
	fd, err := syscall.Open(parent, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrMarker
	}
	directory := os.NewFile(uintptr(fd), "restore-marker-parent")
	child, err := syscall.Openat(fd, name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		directory.Close()
		return nil, ErrMarker
	}
	file := os.NewFile(uintptr(child), "restore-marker")
	info, err := file.Stat()
	if err != nil || !privateMarker(info) {
		file.Close()
		directory.Close()
		return nil, ErrMarker
	}
	return &Marker{parent: directory, file: file, name: name, identity: info}, nil
}
func privateMarker(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm() == 0600 && st.Uid == uint32(os.Geteuid()) && st.Nlink == 1
}
func (m *Marker) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.closeErr
	}
	m.closed = true
	fd, err := syscall.Openat(int(m.parent.Fd()), m.name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err == nil {
		current := os.NewFile(uintptr(fd), "restore-marker-check")
		info, e := current.Stat()
		current.Close()
		if e != nil || !privateMarker(info) || !os.SameFile(info, m.identity) {
			err = ErrMarker
		} else {
			err = syscall.Unlinkat(int(m.parent.Fd()), m.name)
		}
	} else if errors.Is(err, syscall.ENOENT) {
		err = nil
	}
	if errors.Join(err, m.file.Close(), m.parent.Close()) != nil {
		m.closeErr = ErrMarker
	}
	return m.closeErr
}

func (d *Destinations) ResolvePath(path string) (*Destination, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrDestination
	}
	for i, root := range d.Paths() {
		if path == root || strings.HasPrefix(path, root+"/") {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return nil, ErrDestination
			}
			return d.Resolve(strconv.Itoa(i), relative)
		}
	}
	return nil, ErrDestination
}

// DirectoryCandidate admits one archive child beneath an already pinned parent.
// The child may be absent initially. Existing children must be real directories.
// Once a child exists, its identity remains pinned for subsequent validation.
type DirectoryCandidate struct {
	mu       sync.Mutex
	parent   *Destination
	name     string
	file     *os.File
	identity os.FileInfo
	closed   bool
}

func (d *Destination) ChildDirectory(name string) (*DirectoryCandidate, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "\x00\r\n") {
		return nil, ErrDestination
	}
	c := &DirectoryCandidate{parent: d, name: name}
	if err := c.Validate(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
func (c *DirectoryCandidate) Validate() error { return c.check(false) }

// RequireMissing never adopts a newly appearing child. It protects the native
// source-missing relocation branch from silently becoming a filesystem move.
func (c *DirectoryCandidate) RequireMissing() error { return c.check(true) }
func (c *DirectoryCandidate) check(missingOnly bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.parent.Validate() != nil {
		return ErrDestination
	}
	fd, err := syscall.Openat(int(c.parent.file.Fd()), c.name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if errors.Is(err, syscall.ENOENT) && c.identity == nil {
		return nil
	}
	if err != nil {
		return ErrDestination
	}
	current := os.NewFile(uintptr(fd), "archive-child")
	info, err := current.Stat()
	if err != nil || missingOnly || !info.IsDir() || (c.identity != nil && !os.SameFile(info, c.identity)) {
		current.Close()
		return ErrDestination
	}
	if c.file == nil {
		c.file = current
		c.identity = info
	} else {
		current.Close()
	}
	return nil
}
func (c *DirectoryCandidate) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.file != nil {
		return c.file.Close()
	}
	return nil
}
