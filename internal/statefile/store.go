// Package statefile stores small private control records. It never traverses
// a state-file symlink, overwrites an unsafe file, or logs record contents.
package statefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"sync"
	"syscall"
)

var ErrState = errors.New("PRIVATE_STATE_UNAVAILABLE")
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)

const maxRecord = 8 << 20

type Store struct {
	mu  sync.RWMutex
	dir *os.File
}

func privateDirectory(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && info.Mode().Perm() == 0700 && stat.Uid == uint32(os.Geteuid())
}
func privateRecord(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm() == 0600 && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}
func IsPrivateRecord(info os.FileInfo) bool { return privateRecord(info) }

func (s *Store) String() string { return "statefile.Store{private}" }

func Open(path string) (*Store, error) {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, ErrState
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrState
	}
	dir := os.NewFile(uintptr(fd), "private-state")
	info, err := dir.Stat()
	if err != nil || !privateDirectory(info) {
		dir.Close()
		return nil, ErrState
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == nil {
		return nil
	}
	err := s.dir.Close()
	s.dir = nil
	if err != nil {
		return ErrState
	}
	return nil
}

func (s *Store) file(name string) (*os.File, error) {
	fd, err := syscall.Openat(int(s.dir.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if errors.Is(err, syscall.ENOENT) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, ErrState
	}
	f := os.NewFile(uintptr(fd), "private-record")
	info, err := f.Stat()
	if err != nil || !privateRecord(info) {
		f.Close()
		return nil, ErrState
	}
	return f, nil
}

func (s *Store) Read(name string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !namePattern.MatchString(name) || s.dir == nil {
		return nil, ErrState
	}
	f, err := s.file(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxRecord+1))
	if err != nil || len(raw) > maxRecord {
		return nil, ErrState
	}
	return raw, nil
}

func (s *Store) Write(name string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !namePattern.MatchString(name) || s.dir == nil || len(raw) > maxRecord {
		return ErrState
	}
	if old, err := s.file(name); err == nil {
		old.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrState
	}
	tmp := ".record-" + hex.EncodeToString(random[:])
	parent := int(s.dir.Fd())
	fd, err := syscall.Openat(parent, tmp, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return ErrState
	}
	f := os.NewFile(uintptr(fd), "private-record")
	defer syscall.Unlinkat(parent, tmp)
	_, writeErr := f.Write(raw)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return ErrState
	}
	if syscall.Renameat(parent, tmp, parent, name) != nil || s.dir.Sync() != nil {
		return ErrState
	}
	return nil
}

// Lock holds a process-wide advisory lock until the returned descriptor closes.
// The record is never unlinked; all daemon instances lock the same inode.
func (s *Store) Lock(name string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == nil || !namePattern.MatchString(name) {
		return nil, ErrState
	}
	fd, err := syscall.Openat(int(s.dir.Fd()), name, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, ErrState
	}
	f := os.NewFile(uintptr(fd), "private-lock")
	info, err := f.Stat()
	if err != nil || !privateRecord(info) {
		f.Close()
		return nil, ErrState
	}
	if syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, ErrState
	}
	return f, nil
}
