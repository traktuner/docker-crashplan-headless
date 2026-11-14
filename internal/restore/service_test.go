package restore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/statefile"
)

// controls counts native ControlRestore requests of the latest fixture.
var controls *atomic.Int32

func serviceFixture(t *testing.T, loseReply bool, stages ...string) (*Service, *statefile.Store, *atomic.Int32, *atomic.Int32, string) {
	t.Helper()
	root := t.TempDir()
	checks, adds := &atomic.Int32{}, &atomic.Int32{}
	stage := ""
	if len(stages) > 0 {
		stage = stages[0]
	}
	has := func(name string) bool {
		for _, s := range stages {
			if s == name {
				return true
			}
		}
		return false
	}
	var journal *statefile.Store
	replaceRoot := func() {
		if err := os.Rename(root, root+"-old"); err != nil {
			t.Error(err)
		}
		if err := os.Mkdir(root, 0700); err != nil {
			t.Error(err)
		}
	}
	controls = &atomic.Int32{}
	marker := fmt.Sprintf("/tmp/%d.txt", time.Now().UnixNano())
	t.Cleanup(func() { os.Remove(marker) })
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token private-token" {
			t.Error("missing native authorization")
		}
		switch r.URL.Path {
		case "/v0/StatusQuery":
			io.WriteString(w, `{"user":{"userId":17}}`)
		case "/v0/RestorePrivateKeyRequest":
			io.WriteString(w, `{"securityKeyInfoList":[{"sourceGuid":"7","dataKey":{"array":"cHJpdmF0ZS1rZXk="}}]}`)
		case "/v1/Setting/device_engine_writepermission_enforcement_enabled":
			if stage == "after-credentials" {
				replaceRoot()
			}
			io.WriteString(w, `{"device_engine_writepermission_enforcement_enabled":"true"}`)
		case "/v0/RestoreCheckRequest":
			checks.Add(1)
			data, err := journal.Read("restore-operations.json")
			if err != nil || !strings.Contains(string(data), `"state":"indeterminate"`) {
				t.Error("uncertainty was not durable before native check")
			}
			if stage == "after-check" {
				replaceRoot()
			}
			io.WriteString(w, `{"restoreId":"99","fileName":"`+marker+`","errors":[]}`)
		case "/v1/AddRestore":
			adds.Add(1)
			var in struct {
				Path string `json:"restorePath"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Path != root {
				t.Error("wrong restore destination")
			}
			info, err := os.Lstat(marker)
			if err != nil || !privateMarker(info) {
				t.Error("native marker is missing or unsafe")
			}
			if err := os.Remove(marker); err != nil {
				t.Error(err)
			}
			if stage == "final-save" {
				journal.Close()
			}
			if loseReply {
				w.WriteHeader(500)
				io.WriteString(w, `{"password":"private-canary"}`)
				return
			}
			if has("add-no-space") {
				w.WriteHeader(400)
				io.WriteString(w, `{"errors":[{"id":"NO_SPACE"}]}`)
				return
			}
			if has("add-timed-out") {
				w.WriteHeader(500)
				io.WriteString(w, `{"errors":[{"id":"TIMED_OUT"}]}`)
				return
			}
			io.WriteString(w, `{"restoreId":"99","errors":[]}`)
		case "/v0/ControlRestore":
			var in map[string]string
			if json.NewDecoder(r.Body).Decode(&in) != nil || in["action"] != "REMOVE" || in["restoreId"] != "99" {
				t.Error("wrong native control", in)
			}
			controls.Add(1)
			io.WriteString(w, `{"jobs":[]}`)
		case "/v0/RestoreJobsRequest":
			user := `"root"`
			if stage == "direct-job" {
				user = "null"
			}
			if stage == "wrong-user" {
				user = `"other-user"`
			}
			fmt.Fprintf(w, `{"jobs":[{"restoreId":"99","sourceId":"7","targetId":"8","completed":%t,"canceled":false,"userName":%s,"restoreType":"CLIENT","restorePath":%q,"numFilesToRestore":1,"numFilesRestored":0,"numBytesToRestore":"10","numBytesRestored":"0","privateKey":"private-canary"}]}`, has("completed-job"), user, root)
		default:
			t.Error("unexpected native request", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(native.Close)
	client, err := engine.NewClient(native.URL, native.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("private-token")
	roots, err := NewDestinations([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { roots.Close() })
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	journal = store
	service, err := NewService(client, roots, store, "root")
	if err != nil {
		t.Fatal(err)
	}
	return service, store, checks, adds, root
}

func restoreRequest() StartRequest {
	return StartRequest{RequestID: strings.Repeat("a", 32), SourceID: "7", TargetID: "8", Target: Target{RootID: "0"}, Existing: "RENAME_ORIGINAL", Permissions: "current", Items: []engine.RestoreSelection{{Path: "/data/file", BackupSetID: "1", Timestamp: "0"}}}
}

func TestRestoreRootReplacementIsRejectedAtEachMutationBoundary(t *testing.T) {
	for _, stage := range []string{"after-credentials", "after-check"} {
		t.Run(stage, func(t *testing.T) {
			s, _, checks, adds, _ := serviceFixture(t, false, stage)
			if _, err := s.Start(context.Background(), "g1", restoreRequest()); !errors.Is(err, ErrDestination) || adds.Load() != 0 {
				t.Fatal("replaced root accepted", err, adds.Load())
			}
			if stage == "after-credentials" && checks.Load() != 0 {
				t.Fatal("native permission check issued for replaced root")
			}
		})
	}
}
func TestRestoreFinalJournalFailurePreservesUncertainty(t *testing.T) {
	s, _, _, adds, _ := serviceFixture(t, false, "final-save")
	if _, err := s.Start(context.Background(), "g1", restoreRequest()); !errors.Is(err, engine.ErrIndeterminate) || adds.Load() != 1 {
		t.Fatal("lost accepted mutation", err, adds.Load())
	}
}

func TestRestoreJournalLimitDoesNotEvictReplayBarriers(t *testing.T) {
	s, _, checks, adds, _ := serviceFixture(t, false)
	s.records = make([]record, 1024)
	if _, err := s.Start(context.Background(), "g1", restoreRequest()); !errors.Is(err, ErrJournalFull) || checks.Load() != 0 || adds.Load() != 0 {
		t.Fatal("journal limit bypassed", err)
	}
}

func TestRestoreCorruptJournalIsNotDiscarded(t *testing.T) {
	s, store, _, _, _ := serviceFixture(t, false)
	if err := store.Write("restore-operations.json", []byte(`[{"requestId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","generation":"g1","state":"accepted","created":"2026-11-12T00:00:00Z","requestDigest":"not-a-digest"}]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(s.native, s.roots, store, "root"); !errors.Is(err, ErrJournal) {
		t.Fatal("corrupt restore journal ignored", err)
	}
}

func TestRestoreSubmissionPersistsAndNeverReplaysAcrossRestart(t *testing.T) {
	s, store, checks, adds, _ := serviceFixture(t, false)
	req := restoreRequest()
	op, err := s.Start(context.Background(), "generation-1", req)
	if err != nil || op.State != "accepted" || op.RestoreID != "99" {
		t.Fatal(op, err)
	}
	reloaded, err := NewService(s.native, s.roots, store, "root")
	if err != nil {
		t.Fatal(err)
	}
	previous, err := reloaded.Start(context.Background(), "generation-2", req)
	if err != nil || !reflect.DeepEqual(previous, op) || checks.Load() != 1 || adds.Load() != 1 {
		t.Fatal("restore replayed", previous, err, checks.Load(), adds.Load())
	}
	req.Items[0].Path = "/different"
	if _, err := reloaded.Start(context.Background(), "generation-2", req); !errors.Is(err, engine.ErrInvalidRequest) {
		t.Fatal("request ID reused with different content", err)
	}
	jobs, err := reloaded.Jobs(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].Imported || !jobs[0].Admissible {
		t.Fatal(jobs, err)
	}
	raw, _ := json.Marshal(jobs)
	if strings.Contains(string(raw), "private") {
		t.Fatal("native key leaked")
	}
}

func TestRestoreLostReplyRemainsIndeterminateWithoutReplay(t *testing.T) {
	s, _, checks, adds, _ := serviceFixture(t, true)
	op, err := s.Start(context.Background(), "g1", restoreRequest())
	if !errors.Is(err, engine.ErrIndeterminate) || op.State != "indeterminate" {
		t.Fatal(op, err)
	}
	op, err = s.Start(context.Background(), "g1", restoreRequest())
	if err != nil || op.State != "indeterminate" || checks.Load() != 1 || adds.Load() != 1 {
		t.Fatal("ambiguous restore replayed", op, err)
	}
}

func TestRestoreValidatesAllInputsAndDestinationBeforeNativeCheck(t *testing.T) {
	for _, invalid := range []string{"root", "escape", "symlink", "conflict", "selection", "request-id"} {
		t.Run(invalid, func(t *testing.T) {
			s, _, checks, adds, root := serviceFixture(t, false)
			req := restoreRequest()
			switch invalid {
			case "root":
				req.Target.RootID = "1"
			case "escape":
				req.Target.Relative = "../outside"
			case "symlink":
				os.Symlink(t.TempDir(), filepath.Join(root, "link"))
				req.Target.Relative = "link"
			case "conflict":
				req.Existing = "UNKNOWN"
			case "selection":
				req.Items = nil
			case "request-id":
				req.RequestID = "invalid"
			}
			if _, err := s.Start(context.Background(), "g1", req); err == nil || checks.Load() != 0 || adds.Load() != 0 {
				t.Fatal("invalid restore reached native mutation", err)
			}
		})
	}
}

func TestRestoreJournalFailurePreventsNativeMutation(t *testing.T) {
	s, store, checks, adds, _ := serviceFixture(t, false)
	store.Close()
	if _, err := s.Start(context.Background(), "g1", restoreRequest()); err == nil || checks.Load() != 0 || adds.Load() != 0 {
		t.Fatal("unrecorded restore dispatched", err)
	}
}

func TestRestoreReportsImportedJobsWithoutAdoptingThem(t *testing.T) {
	s, _, _, _, _ := serviceFixture(t, false)
	jobs, err := s.Jobs(context.Background())
	if err != nil || len(jobs) != 1 || !jobs[0].Imported || !jobs[0].Admissible {
		t.Fatal(jobs, err)
	}
}

func TestRestoreDirectJobsNeedDestinationAdmissionWithoutHelperUser(t *testing.T) {
	for _, stage := range []string{"direct-job", "wrong-user"} {
		t.Run(stage, func(t *testing.T) {
			s, _, _, _, _ := serviceFixture(t, false, stage)
			jobs, err := s.Jobs(context.Background())
			if err != nil || len(jobs) != 1 {
				t.Fatal(jobs, err)
			}
			if jobs[0].Admissible != (stage == "direct-job") {
				t.Fatal("wrong helper/direct admission", stage, jobs[0].Admissible)
			}
		})
	}
}

// Audit M7: a parsed native rejection is a definite outcome. It is journaled
// as rejected and can be archived, so it no longer fills the history.
func TestRestoreParsedRejectionIsRecordedAsRejected(t *testing.T) {
	s, _, _, adds, _ := serviceFixture(t, false, "add-no-space")
	req := restoreRequest()
	op, err := s.Start(context.Background(), "g1", req)
	if !errors.Is(err, engine.ErrRestoreSpace) || op.State != "rejected" || adds.Load() != 1 {
		t.Fatal("parsed rejection not recorded as rejected", op, err)
	}
	if n, err := s.ArchiveOperations(context.Background(), []string{req.RequestID}); err != nil || n != 1 {
		t.Fatal("rejected submission cannot be archived", n, err)
	}
	again, err := s.Start(context.Background(), "g1", req)
	if err != nil || again.State != "rejected" || adds.Load() != 1 {
		t.Fatal("archived rejection is not a replay barrier", again, err, adds.Load())
	}
}

// An engine timeout answer stays indeterminate. After review it can be
// acknowledged, which keeps the replay barrier and allows archiving.
func TestRestoreIndeterminateSubmissionCanBeAcknowledged(t *testing.T) {
	s, store, _, adds, _ := serviceFixture(t, false, "add-timed-out")
	req := restoreRequest()
	op, err := s.Start(context.Background(), "g1", req)
	if !errors.Is(err, engine.ErrIndeterminate) || op.State != "indeterminate" {
		t.Fatal("engine timeout recorded as definite", op, err)
	}
	if _, err := s.ArchiveOperations(context.Background(), []string{req.RequestID}); !errors.Is(err, engine.ErrIndeterminate) {
		t.Fatal("indeterminate submission archived without review", err)
	}
	acknowledged, err := s.AcknowledgeOperation(req.RequestID)
	if err != nil || acknowledged.State != "acknowledged" {
		t.Fatal(acknowledged, err)
	}
	if _, err := s.AcknowledgeOperation(req.RequestID); !errors.Is(err, engine.ErrInvalidRequest) {
		t.Fatal("acknowledged twice", err)
	}
	reloaded, err := NewService(s.native, s.roots, store, "root")
	if err != nil {
		t.Fatal("acknowledged journal not loadable", err)
	}
	// The native job 99 may stem from it and is still active.
	if _, err := reloaded.ArchiveOperations(context.Background(), []string{req.RequestID}); !errors.Is(err, engine.ErrInvalidRequest) {
		t.Fatal("acknowledged submission archived while its job runs", err)
	}
	again, err := reloaded.Start(context.Background(), "g2", req)
	if err != nil || again.State != "acknowledged" || adds.Load() != 1 {
		t.Fatal("acknowledged submission replayed", again, err, adds.Load())
	}
}

func TestAcknowledgedSubmissionArchivesOnceItsJobEnded(t *testing.T) {
	s, _, _, _, _ := serviceFixture(t, false, "add-timed-out", "completed-job")
	req := restoreRequest()
	if op, _ := s.Start(context.Background(), "g1", req); op.State != "indeterminate" {
		t.Fatal(op)
	}
	if _, err := s.AcknowledgeOperation(req.RequestID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ArchiveOperations(context.Background(), []string{req.RequestID}); err != nil || n != 1 {
		t.Fatal("acknowledged submission cannot be archived", n, err)
	}
}

func TestClearCompletedRemovesOnlyFinishedJobs(t *testing.T) {
	s, _, _, _, _ := serviceFixture(t, false, "completed-job")
	n, err := s.ClearCompleted(context.Background())
	if err != nil || n != 1 || controls.Load() != 1 {
		t.Fatal("completed job not removed", n, err, controls.Load())
	}
	running, _, _, _, _ := serviceFixture(t, false)
	n, err = running.ClearCompleted(context.Background())
	if err != nil || n != 0 || controls.Load() != 0 {
		t.Fatal("unfinished job removed", n, err, controls.Load())
	}
}

func TestJobsCarryLiveRestoreStatistics(t *testing.T) {
	s, _, _, _, _ := serviceFixture(t, false)
	jobs, err := s.Jobs(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].State != "waiting" || jobs[0].RemainingMs != "-1" {
		t.Fatal("job without statistics", jobs, err)
	}
	s.ObserveRestoreStats([]byte(`{"restoreStats":{"restoreId":99,"restoring":true,"stopped":false,"completed":false,"selectedForRestore":false,"receiveRateInBytesPerSec":20480,"numFilesToRestore":1,"numFilesRestored":0,"numBytesToRestore":40960,"numBytesRestored":20480}}`))
	jobs, err = s.Jobs(context.Background())
	if err != nil || jobs[0].State != "running" || jobs[0].RateBytesPerSecond != "20480" || jobs[0].RemainingMs != "1000" {
		t.Fatal("live statistics not applied", jobs, err)
	}
	raw, _ := json.Marshal(jobs[0])
	for _, field := range []string{`"state":"running"`, `"rateBytesPerSecond":"20480"`, `"remainingMs":"1000"`} {
		if !strings.Contains(string(raw), field) {
			t.Fatal("contract field missing", field, string(raw))
		}
	}
	s.ResetRestoreStats()
	jobs, _ = s.Jobs(context.Background())
	if jobs[0].State != "waiting" {
		t.Fatal("statistics survived a new generation")
	}
}
