package engine

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestArchiveContentsKeepsExactTotalsAndQueryIdentity(t *testing.T) {
	q, _ := (ArchiveQuery{SourceID: "7", TargetID: "8", BackupSetID: "1"}).normalized()
	body, _ := json.Marshal(map[string]any{"contents": map[string]any{"fileId": nativeFileID{q.FileID}, "numFiles": 42, "numDirectories": 3, "size": "9007199254740993", "done": true}})
	result, err := decodeArchiveContents(q, body)
	if err != nil || result.Files != "42" || result.Directories != "3" || result.Bytes != "9007199254740993" || !result.Done {
		t.Fatal(result, err)
	}
	var wrong map[string]any
	json.Unmarshal(body, &wrong)
	wrong["contents"] = map[string]any{"fileId": nativeFileID{"AQAAAAAAAAAAAAAAAAAAAA=="}, "numFiles": 42, "numDirectories": 3, "size": "1", "done": true}
	body, _ = json.Marshal(wrong)
	if _, err := decodeArchiveContents(q, body); !errors.Is(err, ErrProtocol) {
		t.Fatal("unmatched counts accepted", err)
	}
}
