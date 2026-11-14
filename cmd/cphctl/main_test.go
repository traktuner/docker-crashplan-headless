package main

import (
	"bytes"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHelpWorksWithoutARunningDaemon(t *testing.T) {
	var out, diag bytes.Buffer
	if exit := run([]string{"help"}, strings.NewReader(""), &out, &diag); exit != 0 || !strings.Contains(out.String(), "security") || !strings.Contains(out.String(), "restore") || !strings.Contains(out.String(), "stdin") {
		t.Fatal("headless help unavailable", exit, out.String(), diag.String())
	}
}

func TestScanAcknowledgeDoesNotDispatchAnotherScan(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CPH_RUN_DIR", dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scans/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/acknowledge" || r.Method != "POST" {
			t.Error("acknowledgement dispatched a scan")
		}
		w.Write([]byte(`{"data":{"acknowledged":true}}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	var output, diagnostic bytes.Buffer
	if exit := run([]string{"scan", "ack", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, strings.NewReader(""), &output, &diagnostic); exit != 0 {
		t.Fatal("ack command failed", exit, diagnostic.String())
	}
}

func TestScanWaitTimeoutDoesNotStopEngineOrReplayCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CPH_RUN_DIR", dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/backup-sets/7/scan" {
			t.Error("unexpected control action")
		}
		w.Write([]byte(`{"data":{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","state":"accepted"}}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	var output, diagnostic bytes.Buffer
	start := time.Now()
	exit := run([]string{"scan", "7", "--wait", "--timeout", "5ms"}, strings.NewReader(""), &output, &diagnostic)
	if exit != 124 || time.Since(start) > time.Second {
		t.Fatal("wait timeout not honored", exit, diagnostic.String())
	}
}

func TestRestoreCLIUsesProductRoutesAndOneRequest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CPH_RUN_DIR", dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/v1/restore/jobs" {
			t.Error("wrong restore route")
		}
		w.Write([]byte(`{"data":{"state":"accepted","restoreId":"99"}}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	var out, diag bytes.Buffer
	args := []string{"restore", "start", "--source", "7", "--destination", "8", "--set", "1", "--root", "0", "--path", "/data/file", "--request-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if exit := run(args, strings.NewReader(""), &out, &diag); exit != 0 || calls != 1 {
		t.Fatal(exit, out.String(), diag.String(), calls)
	}
}

func TestRestoreCLIRejectsUnknownActionsWithoutConnection(t *testing.T) {
	for _, args := range [][]string{{"restore"}, {"restore", "reset"}, {"restore", "stop", "99/unsafe"}, {"restore", "start", "--native-url", "http://other"}} {
		var out, diag bytes.Buffer
		if exit := run(args, strings.NewReader(""), &out, &diag); exit != 2 {
			t.Fatal("invalid restore command reached transport", args, exit)
		}
	}
}

func TestSettingsCLIRejectsUnknownAreasAndInvalidBodies(t *testing.T) {
	for _, args := range [][]string{{"settings", "get", "secrets"}, {"settings", "set", "backup", "idleThrottleRate", "bad{json"}, {"settings", "set", "backup"}} {
		var out, diag bytes.Buffer
		if exit := run(args, strings.NewReader(""), &out, &diag); exit != 2 {
			t.Fatal(args, exit)
		}
	}
}

func TestSettingsCLIReportsIgnoredUpdateAsFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CPH_RUN_DIR", dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/settings/backup" || r.Method != "PATCH" {
			t.Error("wrong settings control")
		}
		w.Write([]byte(`{"data":{"complete":false,"applied":{"idleThrottleRate":false},"settings":[]}}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	var out, diag bytes.Buffer
	if exit := run([]string{"settings", "set", "backup", "idleThrottleRate", "30"}, strings.NewReader(""), &out, &diag); exit != 5 {
		t.Fatal("ignored update reported success", exit, out.String(), diag.String())
	}
}

func TestHelpAdvertisesExactlyTheImplementedCommandForms(t *testing.T) {
	for _, form := range []string{"sets destination ID DESTINATION attach|detach", "select excludes ID", "custom-key", "security derive-key --output NEW_PRIVATE_FILE", "browse|versions|contents|search"} {
		if !strings.Contains(usage, form) {
			t.Error("implemented command missing from help:", form)
		}
	}
	if strings.Contains(usage, "delete|priority") {
		t.Error("help advertises a set command that does not exist")
	}
	// An empty private directory has no daemon. Exit 3 proves that the form
	// was accepted and reached the transport; exit 2 means it was refused.
	t.Setenv("CPH_RUN_DIR", t.TempDir())
	output := filepath.Join(t.TempDir(), "key")
	for _, c := range []struct {
		stdin string
		args  []string
	}{
		{"", []string{"sets"}}, {"", []string{"sets", "list"}}, {"", []string{"sets", "show", "7"}}, {"", []string{"sets", "create", "Documents"}},
		{"", []string{"sets", "edit", "7", "priority", "1"}}, {"", []string{"sets", "delete", "7", "--confirm", "7"}},
		{"", []string{"sets", "destination", "7", "8", "attach"}}, {"", []string{"sets", "destination", "7", "8", "detach"}},
		{"", []string{"select", "browse", "7", "--parent", "/storage/data"}}, {"", []string{"select", "set", "7", "/storage/data", "--directory", "--exclude"}},
		{`[{"pattern":"*.tmp","regularExpression":false}]`, []string{"select", "excludes", "7"}},
		{"", []string{"security", "status"}}, {"{}", []string{"security", "custom-key"}}, {"{}", []string{"security", "key-type"}},
		{"{}", []string{"security", "derive-key", "--output", output}}, {"", []string{"security", "export-key", "--output", output}},
		{"", []string{"restore", "contents", "--source", "7", "--destination", "8", "--set", "1"}},
		{"", []string{"settings", "get", "backup"}}, {"", []string{"scans"}}, {"", []string{"engine", "restart"}},
	} {
		var out, diag bytes.Buffer
		if exit := run(c.args, strings.NewReader(c.stdin), &out, &diag); exit != 3 {
			t.Error("advertised command did not reach the control transport:", c.args, exit, diag.String())
		}
	}
}

func TestRefusedCommandsExplainTheRefusalWithoutConnection(t *testing.T) {
	t.Setenv("CPH_RUN_DIR", t.TempDir())
	var keyOut, keyDiag bytes.Buffer
	if exit := run([]string{"security", "derive-key", "--output", filepath.Join(t.TempDir(), "key")}, strings.NewReader("not json"), &keyOut, &keyDiag); exit != 2 || !strings.Contains(keyDiag.String(), "INVALID_COMMAND") {
		t.Error("invalid key input gave no explanation:", exit, keyDiag.String())
	}
	for _, args := range [][]string{
		{"sets", "priority", "7", "1"}, {"sets", "edit", "7", "priority"}, {"select", "set", "7"}, {"security", "import-key"},
		{"account", "login", "extra"}, {"settings", "get", "secrets"}, {"scan"}, {"scan", "ack", "short"}, {"engine", "stop"},
		{"devices", "rename", "7"}, {"destinations", "create"}, {"pause", "--set", "0"}, {"login", "user"},
		{"security", "derive-key", "--output", filepath.Join(t.TempDir(), "key"), "extra"},
	} {
		var out, diag bytes.Buffer
		if exit := run(args, strings.NewReader("{}"), &out, &diag); exit != 2 || !strings.Contains(diag.String(), "INVALID_COMMAND") || out.Len() != 0 {
			t.Error("refused command gave no explanation:", args, exit, diag.String())
		}
	}
}
