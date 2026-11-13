package statefile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPrivateAtomicStateSurvivesReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Write("native-session", []byte("fixture-token")); err != nil {
		t.Fatal(err)
	}
	if err := s.Write("native-session", []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	raw, err := reopened.Read("native-session")
	if err != nil || string(raw) != "replacement" {
		t.Fatal("state lost", err)
	}
	info, err := os.Stat(filepath.Join(dir, "native-session"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("state not private", err)
	}
}

type metadataInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (i metadataInfo) Sys() any { return &i.stat }

func TestPrivateRecordAndParentRejectForeignOwner(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	parent, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "record")
	os.WriteFile(file, []byte("fixture"), 0600)
	record, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		info  os.FileInfo
		check func(os.FileInfo) bool
	}{{parent, privateDirectory}, {record, privateRecord}} {
		stat := *item.info.Sys().(*syscall.Stat_t)
		stat.Uid++
		if item.check(metadataInfo{item.info, stat}) {
			t.Fatal("foreign-owned private state accepted")
		}
	}
}

func TestClosedStoreAndOversizeRecordsRemainErrors(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write("record", make([]byte, (8<<20)+1)); err == nil {
		t.Fatal("oversize state accepted")
	}
	oversize := filepath.Join(t.TempDir(), "private")
	large, err := Open(oversize)
	if err != nil {
		t.Fatal(err)
	}
	defer large.Close()
	if err := os.WriteFile(filepath.Join(oversize, "record"), make([]byte, (8<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := large.Read("record"); err == nil {
		t.Fatal("oversize existing state accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("record"); err == nil {
		t.Fatal("closed state read accepted")
	}
	if err := s.Write("record", nil); err == nil {
		t.Fatal("closed state write accepted")
	}
}

func TestPrivateLockRejectsUnsafeExistingFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state")
			store, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			outside := filepath.Join(t.TempDir(), "outside")
			os.WriteFile(outside, []byte("fixture"), 0600)
			name := filepath.Join(dir, "engine.lock")
			switch kind {
			case "symlink":
				os.Symlink(outside, name)
			case "hardlink":
				os.Link(outside, name)
			case "permissions":
				os.WriteFile(name, []byte("fixture"), 0600)
				os.Chmod(name, 0644)
			}
			if lock, err := store.Lock("engine.lock"); err == nil {
				lock.Close()
				t.Fatal("unsafe lock accepted")
			}
		})
	}
}

func TestStoreRejectsSymlinksHardlinksAndNonPrivateFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "permissions", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			outside := filepath.Join(t.TempDir(), "outside")
			os.WriteFile(outside, []byte("private-canary"), 0600)
			name := filepath.Join(dir, "native-session")
			switch kind {
			case "symlink":
				os.Symlink(outside, name)
			case "hardlink":
				os.Link(outside, name)
			case "permissions":
				os.WriteFile(name, []byte("fixture"), 0644)
				os.Chmod(name, 0644)
			case "directory":
				os.Mkdir(name, 0700)
			case "fifo":
				if err := syscall.Mkfifo(name, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Read("native-session"); err == nil {
				t.Fatal("unsafe read accepted")
			}
			if err := s.Write("native-session", []byte("replacement")); err == nil {
				t.Fatal("unsafe replacement accepted")
			}
			raw, _ := os.ReadFile(outside)
			if string(raw) != "private-canary" {
				t.Fatal("outside file changed")
			}
		})
	}
}

func TestStoreRejectsUnsafeNamesAndDirectory(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"", "../outside", "/outside", "a/b", "..", "."} {
		if err := s.Write(name, []byte("x")); err == nil {
			t.Fatal("unsafe state name accepted")
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(dir, link)
	if bad, err := Open(link); err == nil {
		bad.Close()
		t.Fatal("symlink parent accepted")
	}
	loose := t.TempDir()
	os.Chmod(loose, 0755)
	if bad, err := Open(loose); err == nil {
		bad.Close()
		t.Fatal("nonprivate parent accepted")
	}
}
