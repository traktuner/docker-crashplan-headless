package main

import (
	"github.com/traktuner/crashplan-headless/internal/restore"
	"io"
	"testing"
)

func TestRestoreCLICanMixFilesFoldersAndExclusions(t *testing.T) {
	args := []string{"start", "--source", "7", "--destination", "8", "--set", "1", "--root", "0", "--file", "/file", "--directory", "/data", "--exclude-file", "/data/skip", "--exclude-directory", "/data/skip-dir"}
	_, _, body, err := restoreCommand(args, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	req := body.(restore.StartRequest)
	if len(req.Items) != 2 || req.Items[0].IsFolder || !req.Items[1].IsFolder || len(req.Excluded) != 2 || req.Excluded[0].IsFolder || !req.Excluded[1].IsFolder {
		t.Fatal("selection types were lost", req)
	}
}
