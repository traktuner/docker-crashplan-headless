package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestArchiveBrowsePreservesIDsAndPagesWithoutForwardingKeys(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Query map[string]json.RawMessage `json:"backupQueryData"`
			Key   string                     `json:"dataKey"`
		}
		if r.Method != "POST" || r.URL.Path != "/v0/GetChildrenFileVersions" || json.NewDecoder(r.Body).Decode(&in) != nil || string(in.Query["sourceId"]) != `"9007199254740993"` || in.Key != "YQ==" {
			t.Error("wrong archive request")
		}
		fmt.Fprintf(w, `{"backupQueryData":%s,"set":{"fileVersions":[{"backupFile":{"fileId":{"array":"AQAAAAAAAAAAAAAAAAAAAA=="},"sourcePath":"/data/large","fileType":1},"version":{"timestamp":"123","sourceLastModified":"122","sourceLength":"9007199254740993","fileType":1,"sourceChecksum":"private-canary"}}]},"batchNextFileId":{"array":"AgAAAAAAAAAAAAAAAAAAAA=="},"dataKey":"private-canary"}`, mustMarshal(in.Query))
	})
	c.SetToken("fixture")
	in := ArchiveQuery{SourceID: "9007199254740993", TargetID: "8", BackupSetID: "1", At: "0", Limit: 200}
	page, err := c.BrowseArchive(context.Background(), RestoreCredentials{revision: c.SessionRevision(), key: "YQ==", verified: true}, in)
	if err != nil || len(page.Items) != 1 || page.Items[0].Size != "9007199254740993" || page.NextCursor != "AgAAAAAAAAAAAAAAAAAAAA==" {
		t.Fatal(page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "dataKey") {
		t.Fatal("native secret forwarded")
	}
}
func mustMarshal(v any) []byte { b, _ := json.Marshal(v); return b }

func TestArchiveBrowseRejectsErrorsAndMismatchedEcho(t *testing.T) {
	for _, body := range []string{`{"backupQueryData":{"errorCode":"EXPIRED"}}`, `{"backupQueryData":{"sourceId":"9","targetId":"8","backupSetId":"1","timestamp":"0","fileId":{"array":"AAAAAAAAAAAAAAAAAAAAAA=="}},"set":{"fileVersions":[]}}`} {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
		c.SetToken("fixture")
		if _, err := c.BrowseArchive(context.Background(), RestoreCredentials{revision: c.SessionRevision(), key: "YQ==", verified: true}, ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", At: "0", Limit: 200}); err == nil {
			t.Fatal("bad archive response accepted")
		}
	}
}
func TestArchiveMalformedQueryNeverReachesNative(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid query dispatched") })
	c.SetToken("fixture")
	if _, err := c.BrowseArchive(context.Background(), RestoreCredentials{revision: c.SessionRevision(), key: "YQ==", verified: true}, ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", At: "0", Limit: 200, FileID: "bad"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}
func TestArchiveVersionsFiltersNativeFutureResults(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/v0/GetAllVersions" {
			t.Error("wrong versions route")
		}
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprintf(w, `{"backupQueryData":%s,"versionSet":{"versions":[{"timestamp":"100","sourceLastModified":"90","sourceLength":"7","fileType":1},{"timestamp":"200","sourceLastModified":"190","sourceLength":"8","fileType":1}]}}`, body["backupQueryData"])
	})
	c.SetToken("fixture")
	versions, err := c.ArchiveVersions(context.Background(), ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", At: "150", Limit: 200, FileID: "AQAAAAAAAAAAAAAAAAAAAA=="})
	if err != nil || len(versions) != 1 || versions[0].Timestamp != "100" {
		t.Fatal(versions, err)
	}
}

func TestReducedQueryCannotAttributeBrowseOrSpontaneousCounts(t *testing.T) {
	q, _ := (ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1", At: "150", IncludeDeleted: true, Limit: 200}).normalized()
	raw := []byte(`{"backupQueryData":{"sourceId":"7","targetId":"8","fileId":{"array":"AAAAAAAAAAAAAAAAAAAAAA=="},"timestamp":"0","includeDeleted":false,"batchSize":2147483647,"batchLastFileId":{"array":"AQAAAAAAAAAAAAAAAAAAAA=="}},"set":{"fileVersions":[]},"batchNextFileId":{"array":"AQAAAAAAAAAAAAAAAAAAAA=="}}`)
	page, err := decodeArchivePage(q, raw)
	if !errors.Is(err, ErrProtocol) {
		t.Fatal("reduced browse response accepted", page, err)
	}
	raw = []byte(`{"backupQueryData":{"sourceId":"7","targetId":"8","fileId":{"array":"AAAAAAAAAAAAAAAAAAAAAA=="},"timestamp":"0","includeDeleted":false,"batchSize":2147483647},"contents":{"fileId":{"array":"AAAAAAAAAAAAAAAAAAAAAA=="},"numFiles":0,"numDirectories":0,"size":0,"done":true}}`)
	match, err := matchArchiveQuery(q, raw)
	if match || err != nil {
		t.Fatal("spontaneous setless totals attributed", match, err)
	}
	raw = []byte(`{"backupQueryData":{"sourceId":"7","targetId":"8","fileId":{"array":"AAAAAAAAAAAAAAAAAAAAAA=="},"timestamp":"0","includeDeleted":false,"batchSize":2147483647,"errorCode":"IO_EXCEPTION"}}`)
	if match, err := matchArchiveQuery(q, raw); match || err != nil {
		t.Fatal("unrelated native error attributed", match, err)
	}
}
