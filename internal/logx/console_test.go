package logx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestVendorConsoleIsPrivateAndRotatedWithoutParsingBodies(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "console")
	writer, err := OpenConsole(dir, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("0123456789abcdefgh")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	var joined []byte
	for _, name := range []string{"engine.log.2", "engine.log.1", "engine.log"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		joined = append(joined, raw...)
		info, _ := os.Stat(filepath.Join(dir, name))
		if info.Mode().Perm() != 0600 {
			t.Fatal("private native log exposed")
		}
	}
	if !bytes.Equal(joined, []byte("0123456789abcdefgh")) {
		t.Fatal("console bytes lost or parsed")
	}
}
func TestVendorConsoleRejectsSymlinkAndHardlinkedTargets(t *testing.T) {
	for _, link := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "console")
		os.Mkdir(dir, 0700)
		outside := filepath.Join(t.TempDir(), "outside")
		os.WriteFile(outside, []byte("private-canary"), 0600)
		name := filepath.Join(dir, "engine.log")
		if link {
			os.Symlink(outside, name)
		} else {
			os.Link(outside, name)
		}
		if writer, err := OpenConsole(dir, 8, 2); err == nil {
			writer.Close()
			t.Fatal("unsafe native log opened")
		}
		raw, _ := os.ReadFile(outside)
		if string(raw) != "private-canary" {
			t.Fatal("unowned file changed")
		}
	}
}

func TestRotationDoesNotUnlinkAnUnsafeExistingBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "console")
	writer, err := OpenConsole(dir, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.Write([]byte("12345678"))
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("private-canary"), 0600)
	backup := filepath.Join(dir, "engine.log.2")
	os.Symlink(outside, backup)
	if _, err := writer.Write([]byte("9")); err == nil {
		t.Fatal("rotation accepted unowned link")
	}
	if _, err := os.Lstat(backup); err != nil {
		t.Fatal("rotation removed unsafe existing path")
	}
}
