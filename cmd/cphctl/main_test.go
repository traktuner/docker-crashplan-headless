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
