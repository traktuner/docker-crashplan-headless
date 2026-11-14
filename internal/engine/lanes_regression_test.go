package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Regression tests for the core audit (H2, H3, H5, H6, M1) and the lane
// health output. Each one failed on main before the fix.

const selfComputers = `{"UsersMessage":{"myUserId":"5"},"ComputersMessage":{"computers":[{"peerId":"9","userId":"5","properties":{"ME":"true"}}]}}`

// M1: a received rejection (wrong current password) is an answer. The
// corrected request is sent instead of failing with NATIVE_RESPONSE_PENDING.
func TestRejectedActionDoesNotBlockItsClass(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/GetComputers" {
			io.WriteString(w, selfComputers)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(400)
			io.WriteString(w, `{"errors":[{"id":"INCORRECT_PASSWORD"}]}`)
			return
		}
		io.WriteString(w, `{}`)
	})
	c.SetToken("fixture")
	// Fixture values only, generated so they never look like stored secrets.
	in := ArchivePassword{Action: "change", CurrentPassword: strings.Repeat("w", 12), Password: strings.Repeat("n", 12)}
	if err := c.ArchivePassword(context.Background(), in); !errors.Is(err, ErrRejected) || errors.Is(err, ErrIndeterminate) {
		t.Fatal("rejection not reported as a definite rejection", err)
	}
	in.CurrentPassword = strings.Repeat("r", 12)
	if err := c.ArchivePassword(context.Background(), in); errors.Is(err, ErrLegacyPending) || calls.Load() != 2 {
		t.Fatal("a received rejection blocked the class", err, calls.Load())
	}
	if lanes := c.UncertainLanes(); len(lanes) != 0 {
		t.Fatal("rejection reported as an uncertain lane", lanes)
	}
}

// A lost action answer still blocks its class, and health names the class.
func TestLostActionAnswerKeepsTheStrictBarrier(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/GetComputers" {
			io.WriteString(w, selfComputers)
			return
		}
		calls.Add(1)
		w.WriteHeader(500) // no body: nothing proves what happened
	})
	c.SetToken("fixture")
	in := ArchivePassword{Action: "change", CurrentPassword: strings.Repeat("o", 12), Password: strings.Repeat("n", 12)}
	if err := c.ArchivePassword(context.Background(), in); !errors.Is(err, ErrIndeterminate) {
		t.Fatal("lost answer not indeterminate", err)
	}
	if err := c.ArchivePassword(context.Background(), in); !errors.Is(err, ErrLegacyPending) || calls.Load() != 1 {
		t.Fatal("mutation sent while its class is uncertain", err, calls.Load())
	}
	lanes := c.UncertainLanes()
	if len(lanes) != 1 {
		t.Fatal("health does not name the uncertain class", lanes)
	}
	for class, state := range lanes {
		if class == "" || state != "uncertain" {
			t.Fatal("wrong lane health entry", class, state)
		}
	}
}

// A native SYSTEM or TIMED_OUT answer was received, but the action may run.
func TestEngineTimeoutAnswerIsIndeterminateButNotABarrier(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/GetComputers" {
			io.WriteString(w, selfComputers)
			return
		}
		calls.Add(1)
		w.WriteHeader(500)
		io.WriteString(w, `{"errors":[{"id":"SYSTEM"}]}`)
	})
	c.SetToken("fixture")
	in := ArchivePassword{Action: "change", CurrentPassword: strings.Repeat("o", 12), Password: strings.Repeat("n", 12)}
	if err := c.ArchivePassword(context.Background(), in); !errors.Is(err, ErrIndeterminate) {
		t.Fatal("engine timeout reported as definite", err)
	}
	if err := c.ArchivePassword(context.Background(), in); errors.Is(err, ErrLegacyPending) || calls.Load() != 2 {
		t.Fatal("a received answer set the barrier", err, calls.Load())
	}
}

// H3: a rejected restore control is reported and the next control is sent.
func TestRejectedRestoreControlDoesNotBlockControls(t *testing.T) {
	var controls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/ControlRestore" {
			controls.Add(1)
			w.WriteHeader(400)
			io.WriteString(w, `{"jobs":[],"errors":[{"id":"RESTORE_NOT_FOUND"}]}`)
			return
		}
		io.WriteString(w, `{"jobs":[]}`)
	})
	c.SetToken("fixture")
	if err := c.ControlRestore(context.Background(), "99", "STOP"); !errors.Is(err, ErrRejected) {
		t.Fatal("rejection lost", err)
	}
	if err := c.ControlRestore(context.Background(), "100", "STOP"); !errors.Is(err, ErrRejected) || controls.Load() != 2 {
		t.Fatal("rejected control blocked the next one", err, controls.Load())
	}
}

// H2: one failed job-list read never disables restore controls.
func TestFailedJobListReadDoesNotDisableRestoreControl(t *testing.T) {
	var lists atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/RestoreJobsRequest" {
			if lists.Add(1) == 1 {
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, `{"jobs":[]}`)
			return
		}
		io.WriteString(w, `{"jobs":[],"errors":[]}`)
	})
	c.SetToken("fixture")
	if _, err := c.RestoreJobs(context.Background()); err == nil {
		t.Fatal("failed read succeeded")
	}
	if _, ok := c.UncertainLanes()["RestoreJobsResponseMessage"]; !ok {
		t.Fatal("lost list read not reported")
	}
	if err := c.ControlRestore(context.Background(), "99", "STOP"); err != nil {
		t.Fatal("failed list read disabled restore control", err)
	}
	if _, err := c.RestoreJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lanes := c.UncertainLanes(); len(lanes) != 0 {
		t.Fatal("a good list read did not clear its read uncertainty", lanes)
	}
}

func jobList(id string, resume int64) string {
	return fmt.Sprintf(`{"jobs":[{"restoreId":%q,"sourceId":"7","targetId":"8","completed":false,"canceled":false,"restoreType":"CLIENT","numFilesToRestore":"1","numFilesRestored":"0","numBytesToRestore":"10","numBytesRestored":"0","resumeTime":%d}]}`, id, resume)
}

// H3: a lost control answer is verified with a fresh job list, never resent,
// and does not block later controls.
func TestLostRestoreControlIsVerifiedNotResent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		resume int64
		want   error
	}{
		{"stop took effect", "STOP", math.MaxInt64, nil},
		{"stop not visible", "STOP", 0, ErrIndeterminate},
		{"start took effect", "START", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var controls atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v0/ControlRestore" {
					if controls.Add(1) == 1 {
						// Longer than the fixture's 1 s client timeout.
						select {
						case <-r.Context().Done():
						case <-time.After(1500 * time.Millisecond):
						}
						return
					}
					io.WriteString(w, `{"jobs":[]}`)
					return
				}
				io.WriteString(w, jobList("99", tc.resume))
			})
			c.SetToken("fixture")
			err := c.ControlRestore(context.Background(), "99", tc.action)
			if (tc.want == nil && err != nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatal("wrong verified outcome", err)
			}
			if controls.Load() != 1 {
				t.Fatal("control resent", controls.Load())
			}
			if err := c.ControlRestore(context.Background(), "99", tc.action); err != nil || controls.Load() != 2 {
				t.Fatal("lost control blocked the next one", err, controls.Load())
			}
		})
	}
}

func TestRemoveVerifiedByMissingJob(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/ControlRestore" {
			w.WriteHeader(500) // lost: no parsed answer
			return
		}
		io.WriteString(w, `{"jobs":[]}`)
	})
	c.SetToken("fixture")
	if err := c.ControlRestore(context.Background(), "99", "REMOVE"); err != nil {
		t.Fatal("removed job not verified", err)
	}
}

func silentEvents(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/v0/wsevent" {
		return false
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return true
	}
	defer conn.CloseNow()
	for {
		if _, _, err := conn.Read(r.Context()); err != nil {
			return true
		}
	}
}

// H5: one failed count request without a terminal event releases the count
// lane after the bounded drain instead of blocking until a restart.
func TestFailedCountReleasesAfterDrain(t *testing.T) {
	saved := contentsDrain
	contentsDrain = 150 * time.Millisecond
	defer func() { contentsDrain = saved }()
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if silentEvents(w, r) {
			return
		}
		calls.Add(1)
		w.WriteHeader(500)
	})
	defer c.CloseEvents()
	c.SetToken("fixture")
	q := ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"}
	if _, err := c.ArchiveContents(context.Background(), q); err == nil {
		t.Fatal("failed count succeeded")
	}
	other := q
	other.SourceID = "9"
	if _, err := c.ArchiveContents(context.Background(), other); !errors.Is(err, ErrContentsPending) || calls.Load() != 1 {
		t.Fatal("another query took the lane during the drain", err, calls.Load())
	}
	if _, ok := c.UncertainLanes()[contentsClass]; !ok {
		t.Fatal("unresolved count not reported")
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := c.ArchiveContents(context.Background(), other); errors.Is(err, ErrContentsPending) || calls.Load() != 2 {
		t.Fatal("count lane not released after the drain", err, calls.Load())
	}
}

// A parsed count rejection ends the count at once.
func TestRejectedCountEndsAtOnce(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if silentEvents(w, r) {
			return
		}
		calls.Add(1)
		w.WriteHeader(400)
		io.WriteString(w, `{"errors":[{"id":"NOT_FOUND"}]}`)
	})
	defer c.CloseEvents()
	c.SetToken("fixture")
	q := ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"}
	if _, err := c.ArchiveContents(context.Background(), q); !errors.Is(err, ErrRejected) {
		t.Fatal("rejection lost", err)
	}
	q.SourceID = "9"
	if _, err := c.ArchiveContents(context.Background(), q); !errors.Is(err, ErrRejected) || calls.Load() != 2 {
		t.Fatal("rejected count kept the lane", err, calls.Load())
	}
}

// H6: a parsed native rejection of a search is terminal; the next search
// runs without waiting for a drain or a restart.
func TestRejectedSearchIsTerminal(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if silentEvents(w, r) {
			return
		}
		calls.Add(1)
		var body struct {
			Query archiveQueryWire `json:"backupQueryData"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if calls.Load() == 1 {
			w.WriteHeader(400)
			io.WriteString(w, `{"errors":[{"id":"INVALID_REGEX"}]}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"backupQueryData": body.Query, "set": map[string]any{"fileVersions": []any{}}, "done": true})
	})
	defer c.CloseEvents()
	c.SetToken("fixture")
	in := ArchiveSearch{ArchiveQuery: ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"}, Pattern: "("}
	credentials := RestoreCredentials{verified: true, key: "YQ==", revision: c.SessionRevision()}
	if _, err := c.SearchArchive(context.Background(), credentials, in); !errors.Is(err, ErrRejected) {
		t.Fatal("rejection lost", err)
	}
	in.Pattern = "ok"
	if _, err := c.SearchArchive(context.Background(), credentials, in); err != nil || calls.Load() != 2 {
		t.Fatal("rejected search blocked the next one", err, calls.Load())
	}
}

// H6: search uncertainty after a drain timeout expires instead of latching.
func TestSearchUncertaintyExpires(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {})
	c.markReadUncertain(context.Background(), searchClass, causeDrain, 50*time.Millisecond)
	if !c.readLaneUncertain(searchClass) {
		t.Fatal("search not uncertain after a drain timeout")
	}
	if _, ok := c.UncertainLanes()[searchClass]; !ok {
		t.Fatal("uncertain search not reported")
	}
	time.Sleep(80 * time.Millisecond)
	if c.readLaneUncertain(searchClass) || len(c.UncertainLanes()) != 0 {
		t.Fatal("search uncertainty latched")
	}
}

// Contract: restore job state, rate and remaining time like the desktop app.
func TestRestoreJobStateFollowsTheDesktopModel(t *testing.T) {
	now := time.Now()
	jobs, err := decodeRestoreJobs([]byte(`{"jobs":[
		{"restoreId":"1","sourceId":"7","targetId":"8","completed":true,"canceled":false,"restoreType":"CLIENT","numFilesToRestore":"1","numFilesRestored":"1","numBytesToRestore":"10","numBytesRestored":"10"},
		{"restoreId":"2","sourceId":"7","targetId":"8","completed":false,"canceled":false,"restoreType":"CLIENT","numFilesToRestore":"1","numFilesRestored":"0","numBytesToRestore":"10","numBytesRestored":"0","resumeTime":9223372036854775807},
		{"restoreId":"3","sourceId":"7","targetId":"8","completed":false,"canceled":false,"restoreType":"CLIENT","numFilesToRestore":"1","numFilesRestored":"0","numBytesToRestore":"10","numBytesRestored":"0","resumeTime":` + strconv.FormatInt(now.Add(time.Minute).UnixMilli(), 10) + `},
		{"restoreId":"4","sourceId":"7","targetId":"8","completed":false,"canceled":false,"restoreType":"CLIENT","numFilesToRestore":"4","numFilesRestored":"0","numBytesToRestore":"9007199254740993","numBytesRestored":"0","resumeTime":0},
		{"restoreId":"5","sourceId":"7","targetId":"8","completed":false,"canceled":true,"restoreType":"CLIENT","numFilesToRestore":"1","numFilesRestored":"0","numBytesToRestore":"10","numBytesRestored":"0"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ state, blocked string }{{"completed", ""}, {"stopped", "STOPPED"}, {"stopped", "RETRY_DELAYED"}, {"waiting", ""}, {"canceled", ""}}
	for i, job := range jobs {
		if job.State != want[i].state || job.BlockedReason != want[i].blocked || job.RateBytesPerSecond != "0" || job.RemainingMs != "-1" {
			t.Errorf("job %s: %+v", job.ID, job)
		}
	}
	stats, ok := ParseRestoreStats([]byte(`{"sourceId":7,"targetId":8,"restoreStats":{"restoreId":4,"restoring":true,"stopped":false,"completed":false,"selectedForRestore":false,"receiveRateInBytesPerSec":1048576.75,"numFilesToRestore":4,"numFilesRestored":1,"numBytesToRestore":10485760,"numBytesRestored":2097152}}`))
	if !ok {
		t.Fatal("stats not parsed")
	}
	running := jobs[3]
	running.ApplyStats(&stats, now)
	if running.State != "running" || running.RateBytesPerSecond != "1048576" || running.BytesRestored != "2097152" || running.NumBytes != "10485760" {
		t.Fatalf("running job: %+v", running)
	}
	if ms, err := strconv.ParseInt(running.RemainingMs, 10, 64); err != nil || ms < 7999 || ms > 8001 {
		t.Fatal("remaining time", running.RemainingMs)
	}
	slow := stats
	slow.Rate = 100
	slow.Selected = true
	queued := jobs[3]
	queued.ApplyStats(&slow, now)
	if queued.State != "waiting" || queued.RemainingMs != "-1" {
		t.Fatalf("queued job: %+v", queued)
	}
	foreign := stats
	foreign.RestoreID = "1"
	other := jobs[3]
	other.ApplyStats(&foreign, now)
	if other.State != "waiting" {
		t.Fatal("statistics of another job applied", other.State)
	}
	if _, ok := ParseRestoreStats([]byte(`{"restoreStats":{"restoreId":"x","restoring":true,"stopped":false,"completed":false}}`)); ok {
		t.Fatal("invalid restore id accepted")
	}
}
