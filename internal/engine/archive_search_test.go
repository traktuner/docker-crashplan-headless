package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestArchiveSearchReceivesTypedResultsOnTheSharedNativeSocket(t *testing.T) {
	connections := make(chan *websocket.Conn, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/wsevent" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			connections <- conn
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		}
		if r.URL.Path == "/v0/GetComputers" {
			io.WriteString(w, `{"computers":[{"peerId":"8","name":"Local","target":true,"defaultArchiveType":"ARCHIVE_V1"}]}`)
			return
		}
		if r.URL.Path != "/v0/SearchFileVersions" || r.Method != "PUT" {
			t.Error("wrong search route")
		}
		var in struct {
			Query   json.RawMessage `json:"backupQueryData"`
			Pattern string          `json:"searchRegex"`
			Key     string          `json:"privateKey"`
			Full    bool            `json:"searchFullPath"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Pattern != "*.pdf" || in.Key != "YQ==" || !in.Full {
			t.Error("search context or key lost")
		}
		fmt.Fprintf(w, `{"backupQueryData":%s,"set":{"fileVersions":[]},"done":false}`, in.Query)
		conn := <-connections
		connections <- conn
		result := json.RawMessage(fmt.Sprintf(`{"backupQueryData":%s,"set":{"fileVersions":[{"backupFile":{"fileId":{"array":"AQAAAAAAAAAAAAAAAAAAAA=="},"sourcePath":"/data/report.pdf"},"version":{"timestamp":"100","sourceLastModified":"90","sourceLength":"12","fileType":0}}]},"done":true,"privateKey":"must-not-escape"}`, in.Query))
		if err := wsjson.Write(r.Context(), conn, map[string]json.RawMessage{"SearchFileVersionsResultMessage": result}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, server.Client(), time.Second)
	client.SetToken("fixture")
	defer client.CloseEvents()
	page, err := client.SearchArchive(context.Background(), RestoreCredentials{revision: client.SessionRevision(), verified: true, key: "YQ=="}, ArchiveSearch{ArchiveQuery: ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", FileID: "AQAAAAAAAAAAAAAAAAAAAA==", Limit: 1}, Pattern: "*.pdf", FullPath: true})
	if err != nil || len(page.Items) != 1 || page.Items[0].Path != "/data/report.pdf" || !page.Incomplete {
		t.Fatal(page, err)
	}
}
func TestArchiveSearchRejectsUnverifiedContinuationBeforeNativeAccess(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unsupported continuation reached native API") })
	c.SetToken("fixture")
	_, err := c.SearchArchive(context.Background(), RestoreCredentials{revision: c.SessionRevision(), verified: true, key: "YQ=="}, ArchiveSearch{ArchiveQuery: ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", Cursor: "AQAAAAAAAAAAAAAAAAAAAA=="}, Pattern: "*.pdf"})
	if err != ErrInvalidRequest {
		t.Fatal(err)
	}
}

func TestV3FolderSearchRefusesBeforeDispatch(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/GetComputers" {
			t.Error("folder query reached native V3 search", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		io.WriteString(w, `{"computers":[{"peerId":"8","name":"Cloud","target":true,"defaultArchiveType":"ARCHIVE_V3"}]}`)
	})
	c.SetToken("fixture")
	_, err := c.SearchArchive(context.Background(), RestoreCredentials{revision: c.SessionRevision(), verified: true, key: "YQ=="}, ArchiveSearch{ArchiveQuery: ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", FileID: "AQAAAAAAAAAAAAAAAAAAAA=="}, Pattern: "*.pdf"})
	if !errors.Is(err, ErrSearchScope) {
		t.Fatal("unsupported folder scope not explicit", err)
	}
}

func TestSearchHTTPCompletionRetainsLaneUntilOrderedWebSocketTerminal(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			ready := make(chan *websocket.Conn, 1)
			var nativeCalls atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v0/wsevent" {
					conn, err := websocket.Accept(w, r, nil)
					if err != nil {
						return
					}
					ready <- conn
					defer conn.CloseNow()
					for {
						if _, _, err := conn.Read(r.Context()); err != nil {
							return
						}
					}
				}
				nativeCalls.Add(1)
				var body struct {
					Query archiveQueryWire `json:"backupQueryData"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if failed {
					body.Query.Error = "SEVERE"
				}
				json.NewEncoder(w).Encode(map[string]any{"backupQueryData": body.Query, "set": map[string]any{"fileVersions": []any{}}, "done": true})
			})
			defer c.CloseEvents()
			c.SetToken("fixture")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			in := ArchiveSearch{ArchiveQuery: ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"}, Pattern: "first"}
			credentials := RestoreCredentials{verified: true, key: "YQ==", revision: c.SessionRevision()}
			_, err := c.SearchArchive(ctx, credentials, in)
			if failed && !errors.Is(err, ErrRejected) {
				t.Fatal("error lost", err)
			}
			if !failed && err != nil {
				t.Fatal(err)
			}
			in.Pattern = "second"
			if _, err := c.SearchArchive(ctx, credentials, in); !errors.Is(err, ErrSearchPending) || nativeCalls.Load() != 1 {
				t.Fatal("HTTP completion released search lane", err, nativeCalls.Load())
			}
			q, _ := in.ArchiveQuery.normalized()
			wire := q.wire()
			if failed {
				wire.Error = "SEVERE"
			}
			if err := wsjson.Write(ctx, <-ready, map[string]any{"SearchFileVersionsResultMessage": map[string]any{"backupQueryData": wire, "set": map[string]any{"fileVersions": []any{}}, "done": true}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSearchReconnectDoesNotProvePreviousStreamDrained(t *testing.T) {
	connections := make(chan *websocket.Conn, 2)
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/wsevent" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			connections <- conn
			defer conn.CloseNow()
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		}
		calls.Add(1)
		var input struct {
			Query json.RawMessage `json:"backupQueryData"`
		}
		json.NewDecoder(r.Body).Decode(&input)
		fmt.Fprintf(w, `{"backupQueryData":%s,"set":{"fileVersions":[]},"done":true}`, input.Query)
	})
	defer c.CloseEvents()
	c.SetToken("fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	query := ArchiveSearch{ArchiveQuery: ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"}, Pattern: "one"}
	credentials := RestoreCredentials{revision: c.SessionRevision(), verified: true, key: "YQ=="}
	if _, err := c.SearchArchive(ctx, credentials, query); err != nil {
		t.Fatal(err)
	}
	(<-connections).CloseNow()
	for c.EventBus().Connected() {
		select {
		case <-ctx.Done():
			t.Fatal("socket did not close")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err := c.OpenEvents(ctx); err != nil {
		t.Fatal(err)
	}
	q, _ := query.ArchiveQuery.normalized()
	if err := wsjson.Write(ctx, <-connections, map[string]any{"SearchFileVersionsResultMessage": map[string]any{"backupQueryData": q.wire(), "set": map[string]any{"fileVersions": []any{}}, "done": true}}); err != nil {
		t.Fatal(err)
	}
	for !c.searchMu.TryLock() {
		select {
		case <-ctx.Done():
			t.Fatal("drain did not settle")
		case <-time.After(time.Millisecond):
		}
	}
	c.searchMu.Unlock()
	if _, err := c.SearchArchive(ctx, credentials, query); !errors.Is(err, ErrSearchPending) || calls.Load() != 1 {
		t.Fatal("new socket released previous search", err, calls.Load())
	}
}
