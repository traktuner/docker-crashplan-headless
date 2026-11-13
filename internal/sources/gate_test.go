package sources

import (
	"errors"
	"testing"
)

func TestLocalMountRootsAcceptZFSAndRejectRemoteOrParentOnly(t *testing.T) {
	for _, item := range []struct {
		fs, point string
		allowed   bool
	}{{"zfs", "/data", true}, {"ext4", "/data", true}, {"nfs4", "/data", false}, {"cifs", "/data", false}, {"fuse.sshfs", "/data", false}, {"autofs", "/data", false}, {"overlay", "/data", false}, {"zfs", "/", false}} {
		body := "31 20 0:59 /tank/backup " + item.point + " ro,relatime - " + item.fs + " tank/backup ro\n"
		_, err := selectMount([]byte(body), "/data")
		if (err == nil) != item.allowed {
			t.Errorf("%s %s: %v", item.fs, item.point, err)
		}
	}
}

func TestMountParserRetainsNamespaceRowsAndDecodesEscapedPaths(t *testing.T) {
	body := []byte("70 20 0:4 net:[4026531840] /run/docker/netns/a rw - nsfs nsfs rw\n31 20 0:59 /dataset /data\\040set ro - zfs tank/data ro\n")
	m, err := selectMount(body, "/data set")
	if err != nil || m.ID != "31" || m.FS != "zfs" {
		t.Fatal("valid local source rejected", err)
	}
	if _, err := selectMount(body, "/missing"); !errors.Is(err, ErrNotReady) {
		t.Fatal("missing source accepted", err)
	}
}

func TestSourceGateRefusesMissingMountBeforeOpeningPath(t *testing.T) {
	g, err := New([]string{"/this-source-is-deliberately-absent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Check(); !errors.Is(err, ErrNotReady) {
		t.Fatal("unmounted source passed", err)
	}
	for _, root := range []string{"/", "relative", "/data/../else", "/data/", ""} {
		if _, err := New([]string{root}, ""); err == nil {
			t.Fatal("ambiguous source accepted")
		}
	}
}

func TestMissingDatasetParentDirectoryNeedsExplicitSentinel(t *testing.T) {
	for _, item := range []struct {
		root, sentinel string
		ready          bool
	}{{"/", "", true}, {"/unmounted-child", "", false}, {"/subdirectory", ".backup-source", true}} {
		if err := sourceRequirement(mountRecord{Root: item.root}, item.sentinel); (err == nil) != item.ready {
			t.Fatal("missing dataset accepted or explicit subdirectory rejected", err)
		}
	}
}

func TestMountFieldsUseOnlyASCIISpaces(t *testing.T) {
	path := "/data\u00a0set"
	if _, err := selectMount([]byte("31 20 0:59 / "+path+" ro - zfs tank/data ro\n"), path); err != nil {
		t.Fatal("non-ASCII whitespace in a valid mountpoint was split", err)
	}
}

func TestMountedSourcePolicyDoesNotRelaxLocalConfig(t *testing.T) {
	for _, fs := range []string{"zfs", "ext4", "nfs", "nfs4", "cifs", "autofs", "overlay", "fuse.sshfs"} {
		raw := []byte("31 20 0:59 / /data ro - " + fs + " source ro\n")
		for _, policy := range []string{"local", "mounted"} {
			g, err := New([]string{"/data"}, "", policy)
			if err != nil {
				t.Fatal(err)
			}
			_, err = g.sourceMount(raw, "/data")
			want := fs == "zfs" || fs == "ext4" || (policy == "mounted" && (fs == "nfs" || fs == "nfs4" || fs == "cifs"))
			if (err == nil) != want {
				t.Fatalf("%s %s: %v", policy, fs, err)
			}
		}
		err := LocalMountIn(raw, "/data")
		if (err == nil) != (fs == "zfs" || fs == "ext4") {
			t.Fatalf("config policy relaxed for %s", fs)
		}
	}
	if _, err := New([]string{"/data"}, "", "anything"); err == nil {
		t.Fatal("unknown policy accepted")
	}
}
