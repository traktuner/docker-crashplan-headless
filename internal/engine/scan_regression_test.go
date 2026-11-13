package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/statefile"
)

// Ordinary scan fixtures expose a stable native user and self device.
func scanFixture(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	return fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/GetComputers" {
			io.WriteString(w, `{"UsersMessage":{"myUserId":"11"},"ComputersMessage":{"computers":[{"peerId":"21","userId":"11","properties":{"ME":"true"}}]}}`)
			return
		}
		handler(w, r)
	})
}

func TestScanStalledBarrierSurvivesReloadUntilResolved(t *testing.T) {
	for _, resolution := range []string{"acknowledge", "native-completion"} {
		t.Run(resolution, func(t *testing.T) {
			store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			var completed atomic.Int64
			completed.Store(100)
			var commands atomic.Int32
			c := scanFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/BackupSet/7":
					io.WriteString(w, `{"id":"7"}`)
				case "/v0/GetScanStats":
					n := completed.Load()
					fmt.Fprintf(w, `{"backupSetId":"7","scanning":false,"lastCompletedScanDate":"%d","unamendedLastCompletedScanDate":"%d","scanDurationInMs":"1000"}`, n, n)
				case "/v0/Command":
					commands.Add(1)
					io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
				default:
					t.Error(r.URL.Path)
				}
			})
			c.SetToken("fixture")
			s, err := NewScans(c, store)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.BeginGeneration("same-jvm"); err != nil {
				t.Fatal(err)
			}
			op, err := s.Start(context.Background(), "7")
			if err != nil {
				t.Fatal(err)
			}
			s.operations[0].Created = time.Now().Add(-31 * time.Minute)
			s.operations[0].Updated = s.operations[0].Created
			if err := s.save(); err != nil {
				t.Fatal(err)
			}
			s, err = NewScans(c, store)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.BeginGeneration("same-jvm"); err != nil {
				t.Fatal(err)
			}
			if err := s.Observe(context.Background()); err != nil {
				t.Fatal(err)
			}
			current, _ := s.Get(op.ID)
			if current.State != "stalled" {
				t.Fatal("lost stalled state", current.State)
			}
			if _, err := s.Start(context.Background(), "7"); !errors.Is(err, ErrScanInProgress) || commands.Load() != 1 {
				t.Fatalf("elapsed time released scan: commands=%d err=%v", commands.Load(), err)
			}
			if resolution == "acknowledge" {
				if err := s.Acknowledge(op.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				completed.Store(time.Now().UnixMilli())
				if err := s.Observe(context.Background()); err != nil {
					t.Fatal(err)
				}
				current, _ = s.Get(op.ID)
				if current.State != "completed" {
					t.Fatal("late native completion lost", current.State)
				}
			}
			if _, err := s.Start(context.Background(), "7"); err != nil || commands.Load() != 2 {
				t.Fatal("resolved scan still blocks deliberate action", err, commands.Load())
			}
		})
	}
}

func TestScanCannotConsumeAnotherNativeContext(t *testing.T) {
	for _, change := range []string{"session", "account", "self-device", "during-stats"} {
		t.Run(change, func(t *testing.T) {
			store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			var user, self, completed atomic.Int64
			user.Store(11)
			self.Store(21)
			completed.Store(100)
			var switchDuringStats atomic.Bool
			var commands atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					fmt.Fprintf(w, `{"UsersMessage":{"myUserId":"%d"},"ComputersMessage":{"computers":[{"peerId":"%d","userId":"%d","properties":{"ME":"true"}}]}}`, user.Load(), self.Load(), user.Load())
				case "/v1/BackupSet/7":
					io.WriteString(w, `{"id":"7"}`)
				case "/v0/GetScanStats":
					n := completed.Load()
					fmt.Fprintf(w, `{"backupSetId":"7","scanning":false,"lastCompletedScanDate":"%d","unamendedLastCompletedScanDate":"%d","scanDurationInMs":"1000"}`, n, n)
					if switchDuringStats.Load() {
						self.Store(22)
					}
				case "/v0/Command":
					commands.Add(1)
					io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
				default:
					t.Error(r.URL.Path)
				}
			})
			c.SetToken("first-session")
			s, err := NewScans(c, store)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.BeginGeneration("same-jvm"); err != nil {
				t.Fatal(err)
			}
			op, err := s.Start(context.Background(), "7")
			if err != nil {
				t.Fatal(err)
			}
			// Model an elapsed native scan without a future completion timestamp.
			s.operations[0].Created = time.Now().Add(-5 * time.Second)
			s.operations[0].Updated = s.operations[0].Created
			if err := s.save(); err != nil {
				t.Fatal(err)
			}
			s, err = NewScans(c, store)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.BeginGeneration("same-jvm"); err != nil {
				t.Fatal(err)
			}
			completed.Store(time.Now().UnixMilli())
			switch change {
			case "session":
				c.SetToken("second-session")
			case "account":
				user.Store(12)
			case "self-device":
				self.Store(22)
			case "during-stats":
				switchDuringStats.Store(true)
			}
			if err := s.Observe(context.Background()); err != nil && !errors.Is(err, ErrSessionChanged) {
				t.Fatal(err)
			}
			current, _ := s.Get(op.ID)
			if current.State != "indeterminate" || current.Reason != "NATIVE_SESSION_CHANGED" || current.CompletionEvidence != "" {
				t.Fatalf("foreign context resolved the scan: %+v", current)
			}
			if _, err := s.Start(context.Background(), "7"); !errors.Is(err, ErrScanInProgress) || commands.Load() != 1 {
				t.Fatal("context change released unresolved work", err, commands.Load())
			}
			body, _ := json.Marshal(s.List())
			for _, key := range []string{`"context":`, `"revision":`, `"user":`, `"self":`, "first-session", "second-session"} {
				if strings.Contains(string(body), key) {
					t.Fatal("private scan binding exposed", key)
				}
			}
		})
	}
}

func TestScanLegacyJournalCannotBorrowCurrentContext(t *testing.T) {
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	completed := int64(100)
	c := scanFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/BackupSet/7":
			io.WriteString(w, `{"id":"7"}`)
		case "/v0/GetScanStats":
			fmt.Fprintf(w, `{"backupSetId":"7","scanning":false,"lastCompletedScanDate":"%d","unamendedLastCompletedScanDate":"%d","scanDurationInMs":"1000"}`, completed, completed)
		case "/v0/Command":
			io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
		default:
			t.Error(r.URL.Path)
		}
	})
	c.SetToken("fixture")
	s, err := NewScans(c, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BeginGeneration("same-jvm"); err != nil {
		t.Fatal(err)
	}
	op, err := s.Start(context.Background(), "7")
	if err != nil {
		t.Fatal(err)
	}
	s.operations[0].Created = time.Now().Add(-5 * time.Second)
	s.operations[0].binding = nil
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"revision":0,"user":"11","self":"21"}`, `{"revision":1,"user":"0","self":"21"}`, `{"revision":1,"user":"11","self":"invalid"}`} {
		raw, err := store.Read("operations.json")
		if err != nil {
			t.Fatal(err)
		}
		var records []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &records); err != nil {
			t.Fatal(err)
		}
		records[0]["context"] = json.RawMessage(bad)
		malformed, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Write("operations.json", malformed); err != nil {
			t.Fatal(err)
		}
		if _, err := NewScans(c, store); !errors.Is(err, ErrProtocol) {
			t.Fatal("corrupt scan attribution admitted", bad, err)
		}
		if err := s.save(); err != nil {
			t.Fatal(err)
		}
	}
	s, err = NewScans(c, store)
	if err != nil {
		t.Fatal("legacy journal rejected", err)
	}
	if err := s.BeginGeneration("same-jvm"); err != nil {
		t.Fatal(err)
	}
	completed = time.Now().UnixMilli()
	if err := s.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(op.ID)
	if current.State != "indeterminate" || current.CompletionEvidence != "" {
		t.Fatal("legacy scan claimed unattributed completion", current)
	}
}

func TestScanJournalDoesNotEvictStalledWork(t *testing.T) {
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	commands := 0
	c := scanFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/BackupSet/7", "/v1/BackupSet/8":
			fmt.Fprintf(w, `{"id":"%s"}`, strings.TrimPrefix(r.URL.Path, "/v1/BackupSet/"))
		case "/v0/GetScanStats":
			var in struct {
				ID string `json:"backupSetId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
				return
			}
			fmt.Fprintf(w, `{"backupSetId":"%s","scanning":false,"lastCompletedScanDate":"100","unamendedLastCompletedScanDate":"100","scanDurationInMs":"1000"}`, in.ID)
		case "/v0/Command":
			commands++
			io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
		default:
			t.Error(r.URL.Path)
		}
	})
	c.SetToken("fixture")
	s, err := NewScans(c, store)
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Start(context.Background(), "7")
	if err != nil {
		t.Fatal(err)
	}
	s.operations = make([]ScanOperation, 256)
	for i := range s.operations {
		s.operations[i] = op
		s.operations[i].ID = fmt.Sprintf("%032x", i+1)
		s.operations[i].State = "stalled"
	}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(context.Background(), "8"); !errors.Is(err, ErrScanInProgress) || commands != 1 || len(s.operations) != 256 {
		t.Fatal("journal evicted unresolved work", err, commands, len(s.operations))
	}
}
