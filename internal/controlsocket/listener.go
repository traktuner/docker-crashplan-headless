// Package controlsocket recovers only this daemon's private Unix socket.
package controlsocket

import (
	"errors"
	"github.com/traktuner/crashplan-headless/internal/statefile"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type Listener struct {
	net.Listener
	once     sync.Once
	lock     *os.File
	store    *statefile.Store
	path     string
	identity os.FileInfo
}

func Listen(path string) (*Listener, error) {
	store, err := statefile.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	lock, err := store.Lock("daemon.lock")
	if err != nil {
		store.Close()
		return nil, err
	}
	fail := func() (*Listener, error) { lock.Close(); store.Close(); return nil, statefile.ErrState }
	if info, err := os.Lstat(path); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSocket == 0 || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0600 || stat.Nlink != 1 {
			return fail()
		}
		if os.Remove(path) != nil {
			return fail()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail()
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return fail()
	}
	if os.Chmod(path, 0600) != nil {
		listener.Close()
		return fail()
	}
	listener.SetUnlinkOnClose(false)
	identity, err := os.Lstat(path)
	if err != nil {
		listener.Close()
		return fail()
	}
	return &Listener{Listener: listener, lock: lock, store: store, path: path, identity: identity}, nil
}
func (l *Listener) Close() error {
	var err error
	l.once.Do(func() {
		closeErr := l.Listener.Close()
		if current, statErr := os.Lstat(l.path); statErr == nil {
			if os.SameFile(current, l.identity) {
				closeErr = errors.Join(closeErr, os.Remove(l.path))
			} else {
				closeErr = errors.Join(closeErr, statefile.ErrState)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			closeErr = errors.Join(closeErr, statErr)
		}
		if errors.Join(closeErr, l.lock.Close(), l.store.Close()) != nil {
			err = statefile.ErrState
		}
	})
	return err
}
