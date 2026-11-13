package controlsocket

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestStaleOwnedSocketRecoversButSecondDaemonCannotReplaceLiveSocket(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "control.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := os.Chmod(name, 0600); err != nil {
		t.Fatal(err)
	}
	stale.Close()
	listener, err := Listen(name)
	if err != nil {
		t.Fatal("stale socket prevented restart", err)
	}
	defer listener.Close()
	before, _ := os.Lstat(name)
	if other, err := Listen(name); err == nil {
		other.Close()
		t.Fatal("second daemon acquired running socket")
	}
	after, _ := os.Lstat(name)
	if !os.SameFile(before, after) {
		t.Fatal("live socket replaced")
	}
}

func TestSocketRecoveryNeverRemovesRegularFileOrSymlink(t *testing.T) {
	for _, link := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "run")
		os.Mkdir(dir, 0700)
		name := filepath.Join(dir, "control.sock")
		outside := filepath.Join(t.TempDir(), "outside")
		os.WriteFile(outside, []byte("fixture"), 0600)
		if link {
			os.Symlink(outside, name)
		} else {
			os.WriteFile(name, []byte("fixture"), 0600)
		}
		if listener, err := Listen(name); err == nil {
			listener.Close()
			t.Fatal("unsafe socket path replaced")
		}
		if _, err := os.Lstat(name); err != nil {
			t.Fatal("unowned path removed")
		}
	}
}

func TestCloseDoesNotRemoveAReplacementFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	name := filepath.Join(dir, "control.sock")
	listener, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement")
	os.WriteFile(replacement, []byte("preserved"), 0600)
	if err := os.Rename(replacement, name); err != nil {
		t.Fatal(err)
	}
	listener.Close()
	raw, err := os.ReadFile(name)
	if err != nil || string(raw) != "preserved" {
		t.Fatal("listener removed unrelated replacement", err)
	}
}

func TestHardlinkedSocketIsNotOwnedForRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	os.Mkdir(dir, 0700)
	name := filepath.Join(dir, "control.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	os.Chmod(name, 0600)
	stale.Close()
	if err := os.Link(name, filepath.Join(t.TempDir(), "alias")); err != nil {
		t.Fatal(err)
	}
	if listener, err := Listen(name); err == nil {
		listener.Close()
		t.Fatal("socket with another reference was removed")
	}
}
