// Package logx keeps native console bodies private. Product logs use slog.
package logx

import (
	"errors"
	"github.com/traktuner/crashplan-headless/internal/statefile"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

var ErrConsole = errors.New("NATIVE_CONSOLE_UNAVAILABLE")

type Console struct {
	mu          sync.Mutex
	dir         string
	store       *statefile.Store
	file        *os.File
	size, limit int64
	backups     int
	failed      bool
}

func OpenConsole(dir string, limit int64, backups int) (*Console, error) {
	if limit < 1 || backups < 1 || backups > 10 {
		return nil, ErrConsole
	}
	store, err := statefile.Open(dir)
	if err != nil {
		return nil, ErrConsole
	}
	c := &Console{dir: dir, limit: limit, backups: backups, store: store}
	if err := c.open(); err != nil {
		store.Close()
		return nil, err
	}
	return c, nil
}
func (c *Console) path(index int) string {
	name := "engine.log"
	if index > 0 {
		name += "." + strconv.Itoa(index)
	}
	return filepath.Join(c.dir, name)
}
func (c *Console) open() error {
	file, err := os.OpenFile(c.path(0), os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrConsole
	}
	info, err := file.Stat()
	if err != nil || !statefile.IsPrivateRecord(info) {
		file.Close()
		return ErrConsole
	}
	c.file = file
	c.size = info.Size()
	return nil
}
func (c *Console) rotate() error {
	for index := 0; index <= c.backups; index++ {
		info, err := os.Lstat(c.path(index))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !statefile.IsPrivateRecord(info) {
			return ErrConsole
		}
	}
	if err := c.file.Close(); err != nil {
		return ErrConsole
	}
	c.file = nil
	if err := os.Remove(c.path(c.backups)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrConsole
	}
	for index := c.backups - 1; index >= 0; index-- {
		if err := os.Rename(c.path(index), c.path(index+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrConsole
		}
	}
	return c.open()
}
func (c *Console) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file == nil || c.failed {
		return 0, ErrConsole
	}
	written := 0
	fail := func() (int, error) {
		if !c.failed {
			slog.Error("native_console_write_failed")
		}
		c.failed = true
		return written, ErrConsole
	}
	for len(data) > 0 {
		if c.size >= c.limit {
			if err := c.rotate(); err != nil {
				return fail()
			}
		}
		count := min(int64(len(data)), c.limit-c.size)
		n, err := c.file.Write(data[:int(count)])
		written += n
		c.size += int64(n)
		data = data[n:]
		if err != nil || int64(n) != count {
			return fail()
		}
	}
	return written, nil
}
func (c *Console) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if c.file != nil {
		err = c.file.Close()
		c.file = nil
	}
	if errors.Join(err, c.store.Close()) != nil {
		return ErrConsole
	}
	return nil
}

var _ io.Writer = (*Console)(nil)
