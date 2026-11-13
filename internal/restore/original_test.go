package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/statefile"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOriginalBatchCannotPromoteAnIndeterminateChild(t *testing.T) {
	service, _, _, adds, root := serviceFixture(t, true)
	id := "cccccccccccccccccccccccccccccccc"
	sum := sha256.Sum256([]byte("original/" + id + "/0"))
	child := restoreRequest()
	child.RequestID = hex.EncodeToString(sum[:16])
	child.Target.Relative = "."
	child.Items[0].Path = root + "/file"
	if _, err := service.Start(context.Background(), "g1", child); err == nil {
		t.Fatal("fixture did not retain its lost reply")
	}
	in := OriginalRequest{RequestID: id, SourceID: child.SourceID, TargetID: child.TargetID, Existing: child.Existing, Permissions: child.Permissions, Items: child.Items}
	result, err := service.StartOriginal(context.Background(), "g1", in)
	if err == nil || result.Complete || result.Operation.State == "accepted" || adds.Load() != 1 {
		t.Fatal("indeterminate child promoted to accepted batch", result, err, adds.Load())
	}
}

func TestOriginalRestoreGroupsExplicitParentsAndDoesNotReplay(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	destinations := []string{}
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/StatusQuery":
			io.WriteString(w, `{"user":{"userId":17}}`)
		case "/v0/RestorePrivateKeyRequest":
			io.WriteString(w, `{"securityKeyInfoList":[{"sourceGuid":"7","dataKey":{"array":"YQ=="}}]}`)
		case "/v1/Setting/device_engine_writepermission_enforcement_enabled":
			io.WriteString(w, `{"device_engine_writepermission_enforcement_enabled":"false"}`)
		case "/v1/AddRestore":
			var in struct {
				Path *string `json:"restorePath"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Path == nil {
				t.Error("implicit native original-location parking enabled")
				return
			}
			destinations = append(destinations, *in.Path)
			fmt.Fprintf(w, `{"restoreId":"%d"}`, 100+len(destinations))
		case "/v0/RestoreJobsRequest":
			io.WriteString(w, `{"jobs":[{"restoreId":"101","sourceId":"7","targetId":"8","completed":true,"canceled":false,"restoreType":"CLIENT","numFilesToRestore":"1","numFilesRestored":"1","numBytesToRestore":"1","numBytesRestored":"1"},{"restoreId":"102","sourceId":"7","targetId":"8","completed":true,"canceled":false,"restoreType":"CLIENT","numFilesToRestore":"1","numFilesRestored":"1","numBytesToRestore":"1","numBytesRestored":"1"}]}`)
		default:
			t.Error("unexpected native route", r.URL.Path)
		}
	}))
	defer native.Close()
	client, _ := engine.NewClient(native.URL, native.Client(), time.Second)
	client.SetToken("fixture")
	roots, _ := NewDestinations([]string{root})
	defer roots.Close()
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	service, _ := NewService(client, roots, store, "root")
	in := OriginalRequest{RequestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SourceID: "7", TargetID: "8", Existing: "RENAME_ORIGINAL", Permissions: "current", Items: []engine.RestoreSelection{{Path: root + "/one/file", BackupSetID: "1", Timestamp: "0"}, {Path: root + "/two/file", BackupSetID: "1", Timestamp: "0"}}}
	result, err := service.StartOriginal(context.Background(), "g1", in)
	if err != nil || !result.Complete || len(result.Jobs) != 2 || len(destinations) != 2 || destinations[0] != root+"/one" || destinations[1] != root+"/two" {
		t.Fatal("original paths not grouped", result, err, destinations)
	}
	service, _ = NewService(client, roots, store, "root")
	result, err = service.StartOriginal(context.Background(), "g2", in)
	if err != nil || !result.Complete || len(destinations) != 2 {
		t.Fatal("original restore replayed", result, err)
	}
	changed := in
	changed.Existing = "SKIP_ALL"
	if _, err := service.StartOriginal(context.Background(), "g2", changed); err == nil {
		t.Fatal("active batch request ID accepted a different payload")
	}
	if _, err := service.ArchiveOperations(context.Background(), []string{in.RequestID}); err == nil {
		t.Fatal("batch archived without its children")
	}
	ids := []string{in.RequestID}
	for _, job := range result.Jobs {
		ids = append(ids, job.RequestID)
	}
	if count, err := service.ArchiveOperations(context.Background(), ids); err != nil || count != 3 {
		t.Fatal("batch not archived", count, err)
	}
	service, _ = NewService(client, roots, store, "root")
	if replay, err := service.StartOriginal(context.Background(), "g3", in); err != nil || !replay.Complete || len(replay.Jobs) != 2 || len(destinations) != 2 {
		t.Fatal("archived batch replayed", replay, err)
	}
	in.Items[0].Path = root + "/one/changed"
	if _, err := service.StartOriginal(context.Background(), "g2", in); err == nil || len(destinations) != 2 {
		t.Fatal("request ID reused with changed selection")
	}
	in.RequestID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	in.Items[1].Path = "/outside/file"
	if _, err := service.StartOriginal(context.Background(), "g2", in); err == nil || len(destinations) != 2 {
		t.Fatal("partial mutation before validating every original parent")
	}
}
