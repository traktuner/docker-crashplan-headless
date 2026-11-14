package runas

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"testing"
)

func resolve(t *testing.T, env map[string]string) (Identity, string) {
	t.Helper()
	var logs bytes.Buffer
	id := Resolve(func(k string) string { return env[k] }, slog.New(slog.NewTextHandler(&logs, nil)))
	return id, logs.String()
}

func TestRootDefaultsAndUserSettings(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("user selection from USER_ID applies only when the container starts as root")
	}
	id, _ := resolve(t, nil)
	if id.UID != 0 || id.GID != 0 || id.Switch || id.Name != "root" || id.Home != "/root" || id.Umask != -1 {
		t.Fatal("root default changed", id)
	}
	id, _ = resolve(t, map[string]string{"USER_ID": "1000"})
	if id.UID != 1000 || id.GID != 1000 || !id.Switch || id.Home != "/config" {
		t.Fatal("GROUP_ID does not follow USER_ID", id)
	}
	id, _ = resolve(t, map[string]string{"USER_ID": "568", "GROUP_ID": "100", "SUP_GROUP_IDS": "44, 3003", "UMASK": "0002"})
	if id.UID != 568 || id.GID != 100 || len(id.Groups) != 2 || id.Groups[1] != 3003 || id.Umask != 0o002 {
		t.Fatal("user settings not applied", id)
	}
	if id, _ := resolve(t, map[string]string{"UMASK": "0277"}); id.Umask != -1 {
		t.Fatal("umask that removes owner access accepted", id.Umask)
	}
	id, logs := resolve(t, map[string]string{"USER_ID": "abc", "UMASK": "999"})
	if id.UID != 0 || id.Switch || id.Umask != -1 || !strings.Contains(logs, "item=USER_ID") || !strings.Contains(logs, "item=UMASK") {
		t.Fatal("invalid values must fall back with a warning", id, logs)
	}
}

func TestContainerUserWinsOverUserID(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("needs a non-root test process")
	}
	id, logs := resolve(t, map[string]string{"USER_ID": "4242"})
	if id.UID != os.Geteuid() || id.Switch || !strings.Contains(logs, "container_runs_as_another_user") {
		t.Fatal("non-root container user was not kept", id, logs)
	}
	if id.Home != "/config" {
		t.Fatal("non-root engine home", id.Home)
	}
}

func TestApplyWithoutSwitchKeepsUser(t *testing.T) {
	id := Identity{UID: os.Geteuid(), GID: os.Getegid(), Umask: -1}
	if err := Apply(id); err != nil || os.Geteuid() != id.UID {
		t.Fatal(err)
	}
}

func TestUserWithoutAccountIsNamedByNumber(t *testing.T) {
	name, home := names(4242)
	if name != "4242" || home != "/config" {
		t.Fatal(name, home)
	}
	if name, home := names(0); name != "root" || home != "/root" {
		t.Fatal(name, home)
	}
}

func TestBusyDetectsAHeldLock(t *testing.T) {
	path := t.TempDir() + "/engine.lock"
	if Busy(path) {
		t.Fatal("absent lock reported busy")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if Busy(path) {
		t.Fatal("free lock reported busy")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if !Busy(path) {
		t.Fatal("held lock not detected")
	}
	// Errors other than a lock conflict are left to the state store's lock.
	link := t.TempDir() + "/engine.lock"
	os.Symlink(path, link)
	if Busy(link) {
		t.Fatal("unopenable lock reported busy")
	}
}

func TestTakeOwnershipLeavesOwnedTreeUnchanged(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(root+"/a/b", 0700)
	os.WriteFile(root+"/a/b/f", nil, 0600)
	os.Symlink("/etc/passwd", root+"/a/link")
	var logs bytes.Buffer
	TakeOwnership(root, os.Geteuid(), os.Getegid(), slog.New(slog.NewTextHandler(&logs, nil)))
	if logs.Len() != 0 {
		t.Fatal("owned tree reported changes", logs.String())
	}
}
