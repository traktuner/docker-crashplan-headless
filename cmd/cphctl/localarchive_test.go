package main

import (
	"io"
	"strings"
	"testing"

	"github.com/traktuner/crashplan-headless/internal/destinations"
)

func TestLocalArchiveCommandsKeepExactSubmissions(t *testing.T) {
	id := strings.Repeat("a", 32)
	method, path, body, ok := localArchiveCommand([]string{"create", "--root", "0", "--request-id", id, "--confirm", "CREATE"}, io.Discard)
	req, isRequest := body.(destinations.Request)
	if !ok || !isRequest || method != "POST" || path != "destinations/local" || req.RequestID != id || req.Target.RootID != "0" {
		t.Fatal(method, path, body, ok)
	}
	method, path, _, ok = localArchiveCommand([]string{"status", id}, io.Discard)
	if !ok || method != "GET" || path != "destinations/local/operations/"+id {
		t.Fatal(method, path, ok)
	}
	for _, args := range [][]string{{"create", "--root", "0"}, {"status", "../../x"}, {"unknown"}} {
		if _, _, _, ok := localArchiveCommand(args, io.Discard); ok {
			t.Fatal(args)
		}
	}
}
