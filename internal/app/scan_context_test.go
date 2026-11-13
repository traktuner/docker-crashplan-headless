package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
	"github.com/traktuner/crashplan-headless/internal/statefile"
)

func TestScanHTTPPreservesUnresolvedWork(t *testing.T) {
	for _, transition := range []string{"stalled", "login"} {
		t.Run(transition, func(t *testing.T) {
			var completed atomic.Int64
			completed.Store(100)
			var commands atomic.Int32
			native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					user := "11"
					if r.Header.Get("Authorization") == "token second-session" {
						user = "12"
					}
					fmt.Fprintf(w, `{"UsersMessage":{"myUserId":"%s"},"ComputersMessage":{"computers":[{"peerId":"21","userId":"%s","properties":{"ME":"true"}}]}}`, user, user)
				case "/v1/BackupSet/7":
					io.WriteString(w, `{"id":"7"}`)
				case "/v0/GetScanStats":
					n := completed.Load()
					fmt.Fprintf(w, `{"backupSetId":"7","scanning":false,"lastCompletedScanDate":"%d","unamendedLastCompletedScanDate":"%d","scanDurationInMs":"1000"}`, n, n)
				case "/v0/Command":
					commands.Add(1)
					io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
				case "/v0/LoginRequest":
					io.WriteString(w, `{"httpToken":"second-session"}`)
				default:
					t.Error(r.URL.Path)
				}
			}))
			defer native.Close()
			client, err := engine.NewClient(native.URL, native.Client(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			scans, err := engine.NewScans(client, store)
			if err != nil {
				t.Fatal(err)
			}
			owner := lifecycle.New(lifecycle.Options{Native: client, Scans: scans, MonitorInterval: time.Hour})
			if err := owner.Begin(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			defer owner.End(1)
			client.SetToken("first-session")
			server := New(client, nil, Options{Lifecycle: owner, Scans: scans, Sources: &fixtureGate{}})
			call := func(method, path, body string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				server.UnixHandler().ServeHTTP(w, httptest.NewRequest(method, "http://unix/api/v1/"+path, strings.NewReader(body)))
				return w
			}
			first := call("POST", "backup-sets/7/scan", `{}`)
			if first.Code != 202 {
				t.Fatal(first.Code, first.Body.String())
			}
			var result struct{ Data engine.ScanOperation }
			if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			id := result.Data.ID
			raw, err := store.Read("operations.json")
			if err != nil {
				t.Fatal(err)
			}
			var records []map[string]json.RawMessage
			if err := json.Unmarshal(raw, &records); err != nil {
				t.Fatal(err)
			}
			age := time.Now().Add(-31 * time.Minute)
			records[0]["created"], _ = json.Marshal(age)
			records[0]["updated"], _ = json.Marshal(age)
			raw, err = json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Write("operations.json", raw); err != nil {
				t.Fatal(err)
			}
			scans, err = engine.NewScans(client, store)
			if err != nil {
				t.Fatal(err)
			}
			if err := scans.BeginGeneration(owner.Snapshot().Generation); err != nil {
				t.Fatal(err)
			}
			server.options.Scans = scans
			want := "stalled"
			if transition == "login" {
				login := call("POST", "account/login", `{"username":"fixture","password":"fixture-password","serverAddress":"fixture.invalid"}`)
				if login.Code != 200 {
					t.Fatal(login.Code, login.Body.String())
				}
				completed.Store(time.Now().UnixMilli())
				want = "indeterminate"
			}
			if err := scans.Observe(context.Background()); err != nil {
				t.Fatal(err)
			}
			state := call("GET", "scans/"+id, "")
			if state.Code != 200 || json.Unmarshal(state.Body.Bytes(), &result) != nil || result.Data.State != want || result.Data.CompletionEvidence != "" {
				t.Fatal("wrong scan attribution", state.Code, state.Body.String())
			}
			if again := call("POST", "backup-sets/7/scan", `{}`); again.Code != 409 || commands.Load() != 1 {
				t.Fatal("unresolved scan dispatched again", again.Code, commands.Load())
			}
			if ack := call("POST", "scans/"+id+"/acknowledge", `{}`); ack.Code != 200 || commands.Load() != 1 {
				t.Fatal("acknowledgement dispatched work", ack.Code, commands.Load())
			}
		})
	}
}
