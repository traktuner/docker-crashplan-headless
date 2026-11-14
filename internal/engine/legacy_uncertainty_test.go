package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestUncertainNativeVersionsCannotContaminateAnotherFile(t *testing.T) {
	// The engine may deliver an answer late, to a later request of the same
	// class. Reads echo their query, so a foreign answer is rejected instead
	// of shown, and the class recovers with the next matching answer.
	var calls atomic.Int32
	var stale atomic.Value
	stale.Store("")
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/other" {
			io.WriteString(w, `{"items":[]}`)
			return
		}
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		switch calls.Add(1) {
		case 1:
			// The first answer is lost.
			stale.Store(string(body["backupQueryData"]))
			w.WriteHeader(500)
			return
		case 2:
			// The lost answer arrives for the next request.
			fmt.Fprintf(w, `{"backupQueryData":%s,"versionSet":{"versions":[]}}`, stale.Load())
			return
		}
		fmt.Fprintf(w, `{"backupQueryData":%s,"versionSet":{"versions":[{"timestamp":"111","sourceLastModified":"110","sourceLength":"7","fileType":0}]}}`, body["backupQueryData"])
	})
	c.SetToken("fixture")
	q := ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", At: "0", FileID: "AQAAAAAAAAAAAAAAAAAAAA=="}
	if _, err := c.ArchiveVersions(context.Background(), q); err == nil {
		t.Fatal("lost answer did not fail")
	}
	q.FileID = "AgAAAAAAAAAAAAAAAAAAAA=="
	if _, err := c.ArchiveVersions(context.Background(), q); !errors.Is(err, ErrProtocol) {
		t.Fatal("delayed earlier result can contaminate another file", err)
	}
	if !c.legacy["GetAllVersionsResultMessage"].uncertain {
		t.Fatal("a foreign answer must keep the class uncertain")
	}
	if _, err := c.legacyRequest(context.Background(), "OtherResult", "GET", "/v0/other", nil, true, false); err != nil {
		t.Fatal("unrelated reads blocked", err)
	}
	versions, err := c.ArchiveVersions(context.Background(), q)
	if err != nil || len(versions) != 1 {
		t.Fatal("matching answer did not recover the class", versions, err)
	}
	if c.legacy["GetAllVersionsResultMessage"].uncertain {
		t.Fatal("class still uncertain after a matching answer")
	}
}

func TestLeavingThePageDoesNotAbandonARead(t *testing.T) {
	release := make(chan struct{})
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		io.WriteString(w, `{"AlertListMessage":{"alerts":[]}}`)
	})
	c.SetToken("fixture")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Alerts(ctx); done <- err }()
	cancel()
	close(release)
	<-done
	if lane := c.legacy["AlertListMessage"]; lane != nil && lane.uncertain {
		t.Fatal("a closed page left the alert class uncertain")
	}
	if _, err := c.Alerts(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedArchiveQueryKeepsTheClassUsable(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		query := body["backupQueryData"]
		query["errorCode"] = "NOT_FOUND"
		encoded, _ := json.Marshal(query)
		fmt.Fprintf(w, `{"backupQueryData":%s,"versionSet":{"versions":[]}}`, encoded)
	})
	c.SetToken("fixture")
	q := ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", At: "0", FileID: "AQAAAAAAAAAAAAAAAAAAAA=="}
	if _, err := c.ArchiveVersions(context.Background(), q); !errors.Is(err, ErrRejected) {
		t.Fatal("rejection not reported as rejection", err)
	}
	if lane := c.legacy["GetAllVersionsResultMessage"]; lane == nil || lane.uncertain {
		t.Fatal("a rejected but matching answer made the class uncertain")
	}
}
