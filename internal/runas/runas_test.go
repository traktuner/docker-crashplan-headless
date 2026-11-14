package runas

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
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
