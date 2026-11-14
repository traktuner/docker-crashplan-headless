package initcfg

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestVersionChangePreservesExistingConfigurationCacheAndHeap(t *testing.T) {
	root := t.TempDir()
	defaults := t.TempDir()
	os.Mkdir(filepath.Join(defaults, "conf"), 0700)
	os.WriteFile(filepath.Join(defaults, "conf", "my.service.xml"), []byte("<service><serviceHost>127.0.0.1</serviceHost></service>"), 0600)
	os.Mkdir(filepath.Join(root, "conf"), 0700)
	os.Mkdir(filepath.Join(root, "cache"), 0700)
	files := map[string][]byte{"conf/my.service.xml": []byte("<service><serviceHost>127.0.0.1</serviceHost><custom>preserved</custom></service>"), "conf/jvm_args": []byte("-Xmx40G\n"), "cache/native.db": []byte("cache-canary"), "machine-id": []byte("stable-machine-id"), "cp_version": []byte("12.0.0\n")}
	for name, data := range files {
		os.WriteFile(filepath.Join(root, name), data, 0600)
	}
	if err := Prepare(Options{Root: root, Defaults: defaults, Version: "12.1.1", MountInfo: mountFixture(root)}); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if name == "cp_version" {
			continue
		}
		after, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(after, data) {
			t.Fatal("version change altered persistent data", name, err)
		}
	}
}

func TestFirstSeedAndExplicitHeapAreIdempotent(t *testing.T) {
	root := t.TempDir()
	defaults := t.TempDir()
	os.Mkdir(filepath.Join(defaults, "conf"), 0700)
	os.WriteFile(filepath.Join(defaults, "conf", "jvm_args"), []byte("-Xmx1024M\n"), 0600)
	opts := Options{Root: root, Defaults: defaults, Version: "12.1.1", Heap: "40G", MountInfo: mountFixture(root)}
	if err := Prepare(opts); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "machine-id"))
	if len(before) != 32 {
		t.Fatal("stable machine ID not seeded")
	}
	if err := Prepare(opts); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "machine-id"))
	if !bytes.Equal(before, after) {
		t.Fatal("machine identity regenerated")
	}
	heap, _ := os.ReadFile(filepath.Join(root, "conf/jvm_args"))
	if string(heap) != "-Xmx40G\n" {
		t.Fatal("explicit heap not applied")
	}
}

func TestUnMountedConfigRefusedBeforeAnyWrite(t *testing.T) {
	root := t.TempDir()
	defaults := t.TempDir()
	if err := os.Mkdir(filepath.Join(defaults, "conf"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(Options{Root: root, Defaults: defaults, Version: "12.1.1", MountInfo: []byte("1 0 0:1 / / rw - ext4 /dev/root rw\n")}); err == nil {
		t.Fatal("ephemeral config allowed")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("unmounted config was modified")
	}
}
func mountFixture(root string) []byte {
	return []byte("31 20 0:59 / " + root + " rw - zfs tank/config rw\n")
}

func TestInitRefusesCredentialSymlinksAndPreservesOutsideFiles(t *testing.T) {
	for _, name := range []string{"cp_version", "machine-id", "conf/jvm_args"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			defaults := t.TempDir()
			os.Mkdir(filepath.Join(root, "conf"), 0700)
			outside := filepath.Join(t.TempDir(), "outside")
			os.WriteFile(outside, []byte("private-canary"), 0600)
			os.Symlink(outside, filepath.Join(root, name))
			if err := Prepare(Options{Root: root, Defaults: defaults, Version: "12.1.1", Heap: "40G", MountInfo: mountFixture(root)}); err == nil {
				t.Fatal("unsafe metadata link accepted")
			}
			data, _ := os.ReadFile(outside)
			if string(data) != "private-canary" {
				t.Fatal("outside file changed")
			}
		})
	}
}

func TestUnusableHeapIsIgnoredAndSavedHeapKept(t *testing.T) {
	for _, heap := range []string{"8192", "40GB", "-1G", "lots", "512M", "999999999G"} {
		t.Run(heap, func(t *testing.T) {
			root := t.TempDir()
			os.Mkdir(filepath.Join(root, "conf"), 0700)
			saved := filepath.Join(root, "conf", "jvm_args")
			os.WriteFile(saved, []byte("-Xmx40G\n"), 0600)
			var logs bytes.Buffer
			err := Prepare(Options{Root: root, Defaults: t.TempDir(), Version: "12.1.1", Heap: heap, MountInfo: mountFixture(root), Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			if err != nil {
				t.Fatal("unusable heap stopped the start", err)
			}
			if after, _ := os.ReadFile(saved); string(after) != "-Xmx40G\n" {
				t.Fatal("ignored heap replaced the saved heap", string(after))
			}
			if !strings.Contains(logs.String(), "msg=heap_setting_ignored") || strings.Contains(logs.String(), heap) {
				t.Fatal("ignored heap not reported safely:", logs.String())
			}
		})
	}
	for _, heap := range []string{"1024M", "1G", "1048576K", "40g", "4096G", "040G"} {
		t.Run(heap, func(t *testing.T) {
			root := t.TempDir()
			os.Mkdir(filepath.Join(root, "conf"), 0700)
			if err := Prepare(Options{Root: root, Defaults: t.TempDir(), Version: "12.1.1", Heap: heap, MountInfo: mountFixture(root)}); err != nil {
				t.Fatal("valid heap refused", err)
			}
			if after, _ := os.ReadFile(filepath.Join(root, "conf", "jvm_args")); string(after) != "-Xmx"+heap+"\n" {
				t.Fatal("valid heap not saved", string(after))
			}
		})
	}
}

func TestFilesOfOtherUsersAndHardLinksAreUsed(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "conf"), 0700)
	os.WriteFile(filepath.Join(root, "machine-id"), []byte("0123456789abcdef0123456789abcdef"), 0644)
	os.Link(filepath.Join(root, "machine-id"), filepath.Join(root, "second-name"))
	os.WriteFile(filepath.Join(root, "conf", "my.service.xml"), []byte("<config><serviceHost>0.0.0.0</serviceHost></config>"), 0644)
	os.Link(filepath.Join(root, "conf", "my.service.xml"), filepath.Join(root, "third-name"))
	os.Mkdir(filepath.Join(root, "var"), 0777)
	os.Chmod(filepath.Join(root, "var"), 0777)
	if err := Prepare(Options{Root: root, Defaults: t.TempDir(), Version: "12.1.1", Heap: "8G", MountInfo: mountFixture(root)}); err != nil {
		t.Fatal("hard-linked configuration refused", err)
	}
	if id, _ := os.ReadFile(filepath.Join(root, "machine-id")); string(id) != "0123456789abcdef0123456789abcdef" {
		t.Fatal("device identity changed")
	}
	if xml, _ := os.ReadFile(filepath.Join(root, "conf", "my.service.xml")); !strings.Contains(string(xml), "<serviceHost>127.0.0.1</serviceHost>") {
		t.Fatal("service host not localized", string(xml))
	}
	if info, _ := os.Stat(filepath.Join(root, "var")); info.Mode().Perm()&0022 != 0 {
		t.Fatal("restore helper directory stays writable by others", info.Mode())
	}
	other := foreignFile{mustStat(t, filepath.Join(root, "machine-id"))}
	if reason := unsafeReason(other); reason != "" {
		t.Fatal("file of another user refused", reason)
	}
}

func TestNonRegularConfigurationItemIsNamedWithoutContentOrPath(t *testing.T) {
	cases := []struct {
		name, item, reason string
		arrange            func(root string)
	}{
		{"linked version marker", "cp_version", "not_regular_file", func(root string) {
			os.WriteFile(filepath.Join(root, "outside"), []byte("private-canary"), 0600)
			os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, "cp_version"))
		}},
		{"directory as identity", "machine-id", "not_regular_file", func(root string) {
			os.Mkdir(filepath.Join(root, "machine-id"), 0700)
		}},
		{"damaged service settings", "conf/my.service.xml", "malformed_xml", func(root string) {
			os.WriteFile(filepath.Join(root, "conf", "my.service.xml"), []byte("<service>private-canary"), 0600)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			os.Mkdir(filepath.Join(root, "conf"), 0700)
			c.arrange(root)
			var logs bytes.Buffer
			err := Prepare(Options{Root: root, Defaults: t.TempDir(), Version: "12.1.1", MountInfo: mountFixture(root), Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			if err != ErrInit {
				t.Fatal("unusable configuration item accepted", err)
			}
			text := logs.String()
			if !strings.Contains(text, "msg=configuration_item_refused") || !strings.Contains(text, "item="+c.item+" ") || !strings.Contains(text, "reason="+c.reason) {
				t.Fatal("refusal does not name its item and reason:", text)
			}
			if strings.Contains(text, "canary") || strings.Contains(text, root) {
				t.Fatal("refusal log exposes content or a host path:", text)
			}
		})
	}
}

type foreignFile struct{ os.FileInfo }

func (f foreignFile) Sys() any {
	stat := *f.FileInfo.Sys().(*syscall.Stat_t)
	stat.Uid++
	return &stat
}

func mustStat(t *testing.T, path string) os.FileInfo {
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestPartialConfigurationReceivesMissingDefaultsOnly(t *testing.T) {
	root := t.TempDir()
	defaults := t.TempDir()
	os.Mkdir(filepath.Join(defaults, "conf"), 0700)
	os.WriteFile(filepath.Join(defaults, "conf", "default.service.xml"), []byte("<config/>"), 0600)
	os.WriteFile(filepath.Join(defaults, "conf", "service.log.xml"), []byte("<default-log/>"), 0600)
	os.Mkdir(filepath.Join(root, "conf"), 0700)
	os.WriteFile(filepath.Join(root, "conf", "service.log.xml"), []byte("<user-log/>"), 0600)
	if err := Prepare(Options{Root: root, Defaults: defaults, Version: "12.1.1", MountInfo: mountFixture(root)}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "conf", "default.service.xml")); string(data) != "<config/>" {
		t.Fatal("missing default not added", string(data))
	}
	if data, _ := os.ReadFile(filepath.Join(root, "conf", "service.log.xml")); string(data) != "<user-log/>" {
		t.Fatal("existing configuration file replaced", string(data))
	}
}
