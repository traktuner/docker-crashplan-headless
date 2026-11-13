package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/statefile"
)

func TestScanTracksNativeCompletionAndNeverReplays(t *testing.T) {
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	completed := time.Now().Add(-time.Hour).UnixMilli()
	scanning := false
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/BackupSet/7" {
			io.WriteString(w, `{"id":"7"}`)
			return
		}

		switch r.URL.Path {
		case "/v0/GetScanStats":
			fmt.Fprintf(w, `{"backupSetId":"7","scanning":%t,"lastCompletedScanDate":"%d","unamendedLastCompletedScanDate":"%d","scanDurationInMs":"1000"}`, scanning, completed, completed)
		case "/v0/Command":
			calls++
			io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
		default:
			t.Error("unexpected scan route")
		}
	})
	c.SetToken("fixture")
	tracker, err := NewScans(c, store)
	if err != nil {
		t.Fatal(err)
	}
	op, err := tracker.Start(context.Background(), "7")
	if err != nil || op.State != "accepted" {
		t.Fatal("scan not accepted", err)
	}
	if _, err := tracker.Start(context.Background(), "7"); !errors.Is(err, ErrScanInProgress) {
		t.Fatal("duplicate scan allowed", err)
	}
	if err := tracker.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := tracker.Get(op.ID)
	if unchanged.State == "completed" {
		t.Fatal("idle incorrectly became completed")
	}
	scanning = true
	tracker.Observe(context.Background())
	running, _ := tracker.Get(op.ID)
	if running.State != "running" {
		t.Fatal("scan start lost")
	}
	scanning = false
	completed = time.Now().Add(time.Second).UnixMilli()
	tracker.Observe(context.Background())
	done, _ := tracker.Get(op.ID)
	if done.State != "completed" || done.Completion != completed {
		t.Fatal("native completion not tracked")
	}
	if calls != 1 {
		t.Fatal("scan replayed")
	}
}

func TestScanJournalPreservesAmbiguityAcrossRestart(t *testing.T) {
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/BackupSet/7" {
			io.WriteString(w, `{"id":"7"}`)
			return
		}

		if r.URL.Path == "/v0/GetScanStats" {
			io.WriteString(w, `{"backupSetId":7,"scanning":false,"lastCompletedScanDate":"100","unamendedLastCompletedScanDate":"100","scanDurationInMs":"0"}`)
			return
		}
		calls++
		panic(http.ErrAbortHandler)
	})
	c.SetToken("fixture")
	tracker, err := NewScans(c, store)
	if err != nil {
		t.Fatal(err)
	}
	op, err := tracker.Start(context.Background(), "7")
	if !errors.Is(err, ErrIndeterminate) || op.State != "indeterminate" {
		t.Fatal("dispatch uncertainty lost", err)
	}
	restarted, err := NewScans(c, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Start(context.Background(), "7"); !errors.Is(err, ErrScanInProgress) {
		t.Fatal("uncertain dispatch replayed after restart", err)
	}
	if calls != 1 {
		t.Fatal("native replay")
	}
}

func TestScanRequiresBaselineBeforeDispatch(t *testing.T) {
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	for _, body := range []string{`{}`, `{"scanning":false}`, `{"backupSetId":8,"scanning":false,"lastCompletedScanDate":0}`, `{"backupSetId":7,"scanning":true,"lastCompletedScanDate":0}`} {
		calls := 0
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/BackupSet/7" {
				io.WriteString(w, `{"id":"7"}`)
				return
			}

			if r.URL.Path == "/v0/Command" {
				calls++
			}
			io.WriteString(w, body)
		})
		c.SetToken("fixture")
		s, err := NewScans(c, store)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Start(context.Background(), "7"); err == nil || calls != 0 {
			t.Fatal("scan started without idle matching baseline")
		}
	}
}

func TestScanCannotClaimAnotherGenerationCompletionAndCanBeAcknowledged(t *testing.T) {
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/BackupSet/7" {
			io.WriteString(w, `{"id":"7"}`)
			return
		}

		if r.URL.Path == "/v0/GetScanStats" {
			io.WriteString(w, `{"backupSetId":7,"scanning":false,"lastCompletedScanDate":"101","unamendedLastCompletedScanDate":"101","scanDurationInMs":"0"}`)
		} else {
			panic(http.ErrAbortHandler)
		}
	})
	c.SetToken("fixture")
	s, _ := NewScans(c, store)
	s.BeginGeneration("generation-one")
	op, err := s.Start(context.Background(), "7")
	if !errors.Is(err, ErrIndeterminate) {
		t.Fatal(err)
	}
	if err := s.BeginGeneration("generation-two"); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(op.ID)
	if current.State != "interrupted" {
		t.Fatal("old generation not interrupted", current.State)
	}
	second, err := s.Start(context.Background(), "7")
	if !errors.Is(err, ErrIndeterminate) {
		t.Fatal(err)
	}
	if err := s.Acknowledge(second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(context.Background(), "7"); errors.Is(err, ErrScanInProgress) {
		t.Fatal("acknowledged uncertainty still blocks explicit new action")
	}
}

func TestScanPersistsUncertaintyBeforeNativeDispatch(t *testing.T) {
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/BackupSet/7" {
			io.WriteString(w, `{"id":"7"}`)
			return
		}

		if r.URL.Path == "/v0/GetScanStats" {
			io.WriteString(w, `{"backupSetId":7,"scanning":false,"lastCompletedScanDate":"100","unamendedLastCompletedScanDate":"100","scanDurationInMs":"0"}`)
			return
		}
		raw, err := store.Read("operations.json")
		var operations []ScanOperation
		if err != nil || json.Unmarshal(raw, &operations) != nil || len(operations) != 1 || operations[0].State != "indeterminate" {
			t.Error("native command arrived before durable uncertainty")
		}
		io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
	})
	c.SetToken("fixture")
	scans, _ := NewScans(c, store)
	if _, err := scans.Start(context.Background(), "7"); err != nil {
		t.Fatal(err)
	}
}

// Native longs are strings. V3 can advance its timestamp after a failed scan
// while leaving duration=-1; that reset must never become a completed scan.
func TestNativeScanWireStringsAndCompletionReset(t *testing.T) {
	for _, variant := range []string{"v1", "v3"} {
		t.Run(variant, func(t *testing.T) {
			store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
			defer store.Close()
			field := "unamendedLastCompletedScanDate"
			if variant == "v3" {
				field = "trueLastCompletedScanDate"
			}
			completed := time.Now().Add(-time.Hour).UnixMilli()
			duration := int64(1000)
			scanning := false
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/BackupSet/7":
					io.WriteString(w, `{"id":"7"}`)
				case "/v0/GetScanStats":
					fmt.Fprintf(w, `{"backupSetId":"7","scanning":%t,"lastCompletedScanDate":"%d","%s":"%d","scanDurationInMs":"%d","totalFiles":50,"totalSize":"123456"}`, scanning, completed, field, completed, duration)
				case "/v0/Command":
					io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
				}
			})
			c.SetToken("fixture")
			s, _ := NewScans(c, store)
			op, err := s.Start(context.Background(), "7")
			if err != nil || op.State != "accepted" {
				t.Fatal("native string baseline rejected", err)
			}
			scanning = true
			if err := s.Observe(context.Background()); err != nil {
				t.Fatal(err)
			}
			scanning = false
			completed = time.Now().UnixMilli()
			if variant == "v3" {
				duration = -1
			}
			if err := s.Observe(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := s.Get(op.ID)
			want := "completed"
			if variant == "v3" {
				want = "failed"
			}
			if got.State != want {
				t.Fatalf("native %s result: got %s want %s", variant, got.State, want)
			}
		})
	}
}

func TestNativeScanStatsRequireVariantAndRejectMalformedLongs(t *testing.T) {
	for _, value := range []string{`null`, `"1e3"`, `1.5`, `true`, `"9223372036854775808"`} {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"backupSetId":"7","scanning":false,"lastCompletedScanDate":"0","trueLastCompletedScanDate":%s,"scanDurationInMs":"-1"}`, value)
		})
		if _, err := c.ScanStats(context.Background(), "7"); !errors.Is(err, ErrProtocol) {
			t.Fatalf("bad native timestamp accepted: %s", value)
		}
	}
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/BackupSet/7" {
			io.WriteString(w, `{"id":"7"}`)
			return
		}

		io.WriteString(w, `{"backupSetId":"7","scanning":false,"lastCompletedScanDate":"0","scanDurationInMs":"-1"}`)
	})
	if _, err := c.ScanStats(context.Background(), "7"); !errors.Is(err, ErrProtocol) {
		t.Fatal("unidentified scan variant accepted")
	}
}

func TestScanUnknownSetNeverDispatches(t *testing.T) {
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	commands := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/BackupSet/7" {
			w.WriteHeader(400)
			io.WriteString(w, `{"errors":[]}`)
			return
		}
		commands++
		io.WriteString(w, `{}`)
	})
	c.SetToken("fixture")
	s, _ := NewScans(c, store)
	if _, err := s.Start(context.Background(), "7"); err == nil || commands != 0 {
		t.Fatal("unknown set dispatched", err, commands)
	}
}

func TestScanCompletionMustBelongToThisRequestAndV3HasNoDurationStall(t *testing.T) {
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	old := time.Now().Add(-2 * time.Hour).UnixMilli()
	completed := old
	scanning := false
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/BackupSet/7":
			io.WriteString(w, `{"id":"7"}`)
		case "/v0/GetScanStats":
			fmt.Fprintf(w, `{"backupSetId":"7","scanning":%t,"lastCompletedScanDate":"%d","trueLastCompletedScanDate":"%d","scanDurationInMs":"1000"}`, scanning, completed, completed)
		case "/v0/Command":
			io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
		default:
			t.Error("unexpected route")
		}
	})
	c.SetToken("fixture")
	s, _ := NewScans(c, store)
	op, err := s.Start(context.Background(), "7")
	if err != nil {
		t.Fatal(err)
	}
	completed = old + 1000
	if err := s.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(op.ID)
	if got.State == "completed" {
		t.Fatal("stale native timestamp became completion")
	}
	scanning = true
	s.mu.Lock()
	s.operations[0].Updated = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()
	if err := s.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(op.ID)
	if got.State != "running" {
		t.Fatal("healthy long V3 scan declared stalled", got.State)
	}
}

func TestUnchangedRecentCompletionIsNotThisScan(t *testing.T) {
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	now := time.Now().UnixMilli()
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/BackupSet/7":
			io.WriteString(w, `{"id":"7"}`)
		case "/v0/GetScanStats":
			fmt.Fprintf(w, `{"backupSetId":"7","scanning":false,"lastCompletedScanDate":"%d","unamendedLastCompletedScanDate":"%d","scanDurationInMs":"0"}`, now, now)
		case "/v0/Command":
			io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
		default:
			t.Error("unexpected route")
		}
	})
	c.SetToken("fixture")
	s, _ := NewScans(c, store)
	op, err := s.Start(context.Background(), "7")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(op.ID)
	if got.State != "accepted" {
		t.Fatal("unchanged native timestamp became completion", got.State)
	}
}
