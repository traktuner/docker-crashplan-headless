package restore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreDestinationRefusesTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "folder"), 0700)
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "escape"))
	roots, err := NewDestinations([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	defer roots.Close()
	for _, relative := range []string{"../outside", "/etc", "folder/../folder", "escape", "escape/new"} {
		if d, err := roots.Resolve("0", relative); !errors.Is(err, ErrDestination) {
			if d != nil {
				d.Close()
			}
			t.Fatalf("unsafe restore accepted: %s %v", relative, err)
		}
	}
	d, err := roots.Resolve("0", "folder")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Path() != filepath.Join(root, "folder") || d.Validate() != nil {
		t.Fatal("valid destination lost")
	}
	os.Rename(filepath.Join(root, "folder"), filepath.Join(root, "old"))
	os.Mkdir(filepath.Join(root, "folder"), 0700)
	if !errors.Is(d.Validate(), ErrDestination) {
		t.Fatal("replacement restore directory accepted")
	}
}
func TestRestoreMarkerNeverReusesOrDeletesAReplacement(t *testing.T) {
	parent := t.TempDir()
	marker, err := createMarker(parent, "123.txt")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(parent, "123.txt"))
	if err != nil || info.Mode().Perm() != 0600 || info.Size() != 0 {
		t.Fatal("unsafe marker")
	}
	if _, err := createMarker(parent, "123.txt"); !errors.Is(err, ErrMarker) {
		t.Fatal("existing marker reused")
	}
	os.Rename(filepath.Join(parent, "123.txt"), filepath.Join(parent, "old"))
	os.WriteFile(filepath.Join(parent, "123.txt"), []byte("keep"), 0600)
	if !errors.Is(marker.Close(), ErrMarker) {
		t.Fatal("marker replacement accepted for deletion")
	}
	data, _ := os.ReadFile(filepath.Join(parent, "123.txt"))
	if string(data) != "keep" {
		t.Fatal("replacement marker removed")
	}
}
func TestConsumedMarkerAndForeignMarkerAreHandledWithoutFollowingLinks(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(t.TempDir(), "foreign")
	os.WriteFile(outside, []byte("keep"), 0600)
	os.Symlink(outside, filepath.Join(parent, "456.txt"))
	if _, err := createMarker(parent, "456.txt"); !errors.Is(err, ErrMarker) {
		t.Fatal("marker symlink accepted")
	}
	m, err := createMarker(parent, "789.txt")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(parent, "789.txt"))
	if err := m.Close(); err != nil {
		t.Fatal("native marker consumption became cleanup failure", err)
	}
	for _, path := range []string{"/etc/1.txt", "/tmp/../1.txt", "/tmp/a.txt", "/tmp/1.txt/other"} {
		if _, err := CreateMarker(path); !errors.Is(err, ErrMarker) {
			t.Fatal("bad native marker accepted")
		}
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "keep" {
		t.Fatal("foreign file changed")
	}
}

func TestConfiguredRestoreRootReplacementAndAliasAreRefused(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	os.Mkdir(root, 0700)
	roots, _ := NewDestinations([]string{root})
	defer roots.Close()
	d, err := roots.Resolve("0", "")
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	os.Rename(root, filepath.Join(base, "old"))
	os.Mkdir(root, 0700)
	if d, err := roots.Resolve("0", ""); !errors.Is(err, ErrDestination) {
		if d != nil {
			d.Close()
		}
		t.Fatal("replacement root accepted")
	}
	alias := filepath.Join(base, "alias")
	os.Symlink(root, alias)
	links, _ := NewDestinations([]string{alias})
	defer links.Close()
	if d, err := links.Resolve("0", ""); !errors.Is(err, ErrDestination) {
		if d != nil {
			d.Close()
		}
		t.Fatal("symlink root accepted")
	}
}

func TestArchiveChildPinsIdentityAndNeverAdoptsDuringMissingCheck(t *testing.T) {
	root := t.TempDir()
	roots, _ := NewDestinations([]string{root})
	defer roots.Close()
	p, err := roots.Resolve("0", "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c, err := p.ChildDirectory("7")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.RequireMissing() != nil {
		t.Fatal("absent child rejected")
	}
	if err := os.Mkdir(filepath.Join(root, "7"), 0700); err != nil {
		t.Fatal(err)
	}
	if c.RequireMissing() == nil {
		t.Fatal("appearing child accepted as absent")
	}
	if c.Validate() != nil {
		t.Fatal("new real child not adopted")
	}
	if err := os.Rename(filepath.Join(root, "7"), filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "7"), 0700); err != nil {
		t.Fatal(err)
	}
	if c.Validate() == nil {
		t.Fatal("replacement child accepted")
	}
	if err := os.Remove(filepath.Join(root, "7")); err != nil {
		t.Fatal(err)
	}
	if c.Validate() == nil {
		t.Fatal("disappearing pinned child accepted")
	}
}
