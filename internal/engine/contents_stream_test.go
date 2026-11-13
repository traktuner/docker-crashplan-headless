package engine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestContentsTracksOneNativeStreamThroughItsTerminalEvent(t *testing.T) {
	ready := make(chan *websocket.Conn, 1)
	var calls atomic.Int32
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
		calls.Add(1)
		var body struct {
			Query archiveQueryWire `json:"backupQueryData"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]any{"backupQueryData": body.Query, "contents": map[string]any{"fileId": body.Query.File, "numFiles": 42, "numDirectories": 3, "size": "9007199254740993", "done": false}})
	})
	defer c.CloseEvents()
	c.SetToken("fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q := ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"}
	first, err := c.ArchiveContents(ctx, q)
	if err != nil || first.Done {
		t.Fatal(first, err)
	}
	other := q
	other.SourceID = "9"
	if _, err := c.ArchiveContents(ctx, other); !errors.Is(err, ErrContentsPending) {
		t.Fatal("unfinished count was replaced", err)
	}
	if again, err := c.ArchiveContents(ctx, q); err != nil || again.Files != "42" || calls.Load() != 1 {
		t.Fatal("progress poll dispatched again", again, err, calls.Load())
	}
	normalized, _ := q.normalized()
	event := map[string]any{"backupQueryData": normalized.wire(), "contents": map[string]any{"fileId": nativeFileID{archiveRoot}, "numFiles": 43, "numDirectories": 3, "size": "9007199254740994", "done": true}}
	wsjson.Write(ctx, <-ready, map[string]any{"GetFileContentsResultMessage": event})
	for {
		final, err := c.ArchiveContents(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if final.Done {
			if final.Files != "43" {
				t.Fatal("final counts lost", final)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("terminal event lost")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err := c.ArchiveContents(ctx, other); err != nil {
		t.Fatal("next archive stayed blocked", err)
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected dispatch count", calls.Load())
	}
}

func TestHTTPDoneDoesNotCloseTheOrderedCountStream(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/wsevent" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		}
		var body struct {
			Query archiveQueryWire `json:"backupQueryData"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]any{"backupQueryData": body.Query, "contents": map[string]any{"fileId": body.Query.File, "numFiles": 1, "numDirectories": 0, "size": 1, "done": true}})
	})
	defer c.CloseEvents()
	c.SetToken("fixture")
	q := ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"}
	result, err := c.ArchiveContents(context.Background(), q)
	if err != nil || result.Done {
		t.Fatal("HTTP done closed event stream", result, err)
	}
	q.SourceID = "9"
	if _, err := c.ArchiveContents(context.Background(), q); !errors.Is(err, ErrContentsPending) {
		t.Fatal("count replaced before terminal event", err)
	}
}

func TestHTTPCountErrorRetainsLaneUntilItsWebSocketError(t *testing.T) {
	ready := make(chan *websocket.Conn, 1)
	var calls atomic.Int32
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
		calls.Add(1)
		var body struct {
			Query archiveQueryWire `json:"backupQueryData"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		body.Query.Error = "IO_EXCEPTION"
		json.NewEncoder(w).Encode(map[string]any{"backupQueryData": body.Query})
	})
	defer c.CloseEvents()
	c.SetToken("fixture")
	q := ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"}
	if _, err := c.ArchiveContents(context.Background(), q); !errors.Is(err, ErrRejected) {
		t.Fatal("native error lost", err)
	}
	other := q
	other.SourceID = "9"
	if _, err := c.ArchiveContents(context.Background(), other); !errors.Is(err, ErrContentsPending) || calls.Load() != 1 {
		t.Fatal("HTTP error released count lane", err, calls.Load())
	}
	normalized, _ := q.normalized()
	wire := normalized.wire()
	wire.Error = "IO_EXCEPTION"
	wsjson.Write(context.Background(), <-ready, map[string]any{"GetFileContentsResultMessage": map[string]any{"backupQueryData": wire}})
	deadline := time.After(time.Second)
	for {
		_, err := c.ArchiveContents(context.Background(), other)
		if !errors.Is(err, ErrContentsPending) {
			if !errors.Is(err, ErrRejected) || calls.Load() != 2 {
				t.Fatal("terminal error did not release lane", err, calls.Load())
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("terminal event not consumed")
		case <-time.After(time.Millisecond):
		}
	}
}
