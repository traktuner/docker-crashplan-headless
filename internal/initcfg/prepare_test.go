package initcfg

import (
	"bytes"
	"os"
	"path/filepath"
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
