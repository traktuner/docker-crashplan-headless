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

func TestHeapBelowMinimumIsRefusedBeforeSavedHeapChanges(t *testing.T) {
	for _, heap := range []string{"512M", "1023M", "1048575K", "1k"} {
		t.Run(heap, func(t *testing.T) {
			root := t.TempDir()
			defaults := t.TempDir()
			os.Mkdir(filepath.Join(root, "conf"), 0700)
			saved := filepath.Join(root, "conf", "jvm_args")
			os.WriteFile(saved, []byte("-Xmx40G\n"), 0600)
			if err := Prepare(Options{Root: root, Defaults: defaults, Version: "12.1.1", Heap: heap, MountInfo: mountFixture(root)}); err == nil {
				t.Fatal("heap below the launcher minimum accepted")
			}
			after, err := os.ReadFile(saved)
			if err != nil || string(after) != "-Xmx40G\n" {
				t.Fatal("refused heap replaced the saved heap", string(after), err)
			}
			if entries, _ := os.ReadDir(root); len(entries) != 1 {
				t.Fatal("refused heap changed other configuration", len(entries))
			}
		})
	}
	for _, heap := range []string{"1024M", "1G", "1048576K", "40g"} {
		t.Run(heap, func(t *testing.T) {
			root := t.TempDir()
			defaults := t.TempDir()
			os.Mkdir(filepath.Join(root, "conf"), 0700)
			if err := Prepare(Options{Root: root, Defaults: defaults, Version: "12.1.1", Heap: heap, MountInfo: mountFixture(root)}); err != nil {
				t.Fatal("valid heap refused", err)
			}
			after, _ := os.ReadFile(filepath.Join(root, "conf", "jvm_args"))
			if string(after) != "-Xmx"+heap+"\n" {
				t.Fatal("valid heap not saved", string(after))
			}
		})
	}
}

func TestRefusedConfigurationItemIsNamedWithoutContentOrPath(t *testing.T) {
	cases := []struct {
		name, item, reason string
		arrange            func(root string)
		heap               string
	}{
		{"hard-linked identity", "machine-id", "hard_linked", func(root string) {
			os.WriteFile(filepath.Join(root, "machine-id"), []byte("private-canary"), 0600)
			os.Link(filepath.Join(root, "machine-id"), filepath.Join(root, "second-name"))
		}, ""},
		{"linked version marker", "cp_version", "not_regular_file", func(root string) {
			os.WriteFile(filepath.Join(root, "outside"), []byte("private-canary"), 0600)
			os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, "cp_version"))
		}, ""},
		{"hard-linked service settings", "conf/my.service.xml", "hard_linked", func(root string) {
			os.WriteFile(filepath.Join(root, "conf", "my.service.xml"), []byte("<service>private-canary</service>"), 0600)
			os.Link(filepath.Join(root, "conf", "my.service.xml"), filepath.Join(root, "second-name"))
		}, ""},
		{"damaged service settings", "conf/my.service.xml", "malformed_xml", func(root string) {
			os.WriteFile(filepath.Join(root, "conf", "my.service.xml"), []byte("<service>private-canary"), 0600)
		}, ""},
		{"hard-linked saved heap", "conf/jvm_args", "hard_linked", func(root string) {
			os.WriteFile(filepath.Join(root, "conf", "jvm_args"), []byte("-Xmx40G\n"), 0600)
			os.Link(filepath.Join(root, "conf", "jvm_args"), filepath.Join(root, "second-name"))
		}, "8G"},
		{"heap below minimum", "CRASHPLAN_SRV_MAX_MEM", "below_minimum", func(string) {}, "512M"},
		{"heap without unit", "CRASHPLAN_SRV_MAX_MEM", "invalid_format", func(string) {}, "4096"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			os.Mkdir(filepath.Join(root, "conf"), 0700)
			c.arrange(root)
			var logs bytes.Buffer
			err := Prepare(Options{Root: root, Defaults: t.TempDir(), Version: "12.1.1", Heap: c.heap, MountInfo: mountFixture(root), Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			if err != ErrInit {
				t.Fatal("unsafe configuration item accepted", err)
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

func TestForeignOwnerIsReportedAsItsOwnReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine-id")
	os.WriteFile(path, []byte("stable-machine-id"), 0600)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if reason := unsafeReason(info); reason != "" {
		t.Fatal("own private file refused", reason)
	}
	if reason := unsafeReason(foreignFile{info}); reason != "foreign_owner" || safeRegular(foreignFile{info}) {
		t.Fatal("file of another user accepted or misreported", reason)
	}
}
