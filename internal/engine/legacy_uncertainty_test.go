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
	release, seen := make(chan struct{}), make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/other" {
			io.WriteString(w, `{"items":[]}`)
			return
		}
		if calls.Add(1) == 1 {
			close(seen)
			<-release
			return
		}
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprintf(w, `{"backupQueryData":%s,"versionSet":{"versions":[{"timestamp":"111","sourceLastModified":"110","sourceLength":"7","fileType":0}]}}`, body["backupQueryData"])
	})
	c.SetToken("fixture")
	q := ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", At: "0", FileID: "AQAAAAAAAAAAAAAAAAAAAA=="}
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.ArchiveVersions(first, q); result <- err }()
	<-seen
	cancel()
	if err := <-result; err == nil {
		t.Fatal("dispatched request did not fail after cancellation")
	}
	q.FileID = "AgAAAAAAAAAAAAAAAAAAAA=="
	if _, err := c.ArchiveVersions(context.Background(), q); !errors.Is(err, ErrLegacyPending) {
		t.Fatal("delayed earlier result can contaminate another file", err)
	}
	if calls.Load() != 1 {
		t.Fatal("uncertain native class was dispatched again", calls.Load())
	}
	if _, err := c.legacyRequest(context.Background(), "OtherResult", "GET", "/v0/other", nil, true, false); err != nil {
		t.Fatal("unrelated reads blocked", err)
	}
	c.BindGeneration(context.Background())
	if _, err := c.ArchiveVersions(context.Background(), q); err != nil {
		t.Fatal("new generation kept an old worker barrier", err)
	}
}
