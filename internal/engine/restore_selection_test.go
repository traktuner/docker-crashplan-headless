package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestRestoreFolderExclusionsReachNativeAsDeselectedPaths(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Files map[string]struct {
				Selected bool   `json:"selected"`
				Folder   bool   `json:"isFolder"`
				Set      string `json:"backupSetId"`
			} `json:"files"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Files["/data"].Selected || body.Files["/data/skip"].Selected || !body.Files["/data/skip"].Folder || body.Files["/data/skip"].Set != "1" {
			t.Error("exclusion not sent to native")
		}
		io.WriteString(w, `{"restoreId":"99"}`)
	})
	c.SetToken("fixture")
	in := RestoreSubmission{ID: "99", SourceID: "7", TargetID: "8", Destination: "/restore", Existing: "RENAME_ORIGINAL", Permissions: "current", Items: []RestoreSelection{{Path: "/data", BackupSetID: "1", Timestamp: "0", IsFolder: true}}, Excluded: []RestoreSelection{{Path: "/data/skip", BackupSetID: "1", Timestamp: "0", IsFolder: true}}}
	if _, err := c.AddRestore(context.Background(), RestoreCredentials{revision: c.SessionRevision(), verified: true, key: "YQ==", helperRequired: true}, in); err != nil {
		t.Fatal(err)
	}
	in.Excluded[0].Path = "/other"
	if err := in.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("unrelated exclusion accepted", err)
	}
}

func TestRestoreDuplicateTopLevelNamesRequireSeparateJobs(t *testing.T) {
	in := RestoreSubmission{SourceID: "7", TargetID: "8", Destination: "/restore", Existing: "RENAME_ORIGINAL", Permissions: "current", Items: []RestoreSelection{{Path: "/a/same", BackupSetID: "1", Timestamp: "0"}, {Path: "/b/same", BackupSetID: "1", Timestamp: "0"}}}
	if err := in.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("duplicate names can change the native output layout", err)
	}
}
