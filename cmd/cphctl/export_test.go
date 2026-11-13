package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCustomKeyExportWritesOnlyAnExclusivePrivateFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CPH_RUN_DIR", dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 56)) + "\n"
	raceTarget := ""
	var raceMu sync.Mutex
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/security/export-key" || r.Method != "POST" {
			t.Error("wrong export route")
		}
		raceMu.Lock()
		target := raceTarget
		raceMu.Unlock()
		if target != "" {
			if err := os.WriteFile(target, []byte("keep-existing"), 0600); err != nil {
				t.Error(err)
			}
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte(key))
	})}
	go server.Serve(listener)
	defer server.Close()
	destination := filepath.Join(dir, "key.txt")
	var out, diag bytes.Buffer
	args := []string{"security", "export-key", "--output", destination}
	if exit := run(args, strings.NewReader(""), &out, &diag); exit != 0 {
		t.Fatal("export failed", exit, diag.String())
	}
	raw, _ := os.ReadFile(destination)
	info, _ := os.Stat(destination)
	if string(raw) != key || info.Mode().Perm() != 0600 || strings.Contains(out.String(), key) {
		t.Fatal("unsafe key export")
	}
	if exit := run(args, strings.NewReader(""), &out, &diag); exit == 0 {
		t.Fatal("existing key file overwritten")
	}
	raceMu.Lock()
	raceTarget = filepath.Join(dir, "race.txt")
	args[3] = raceTarget
	raceMu.Unlock()
	if exit := run(args, strings.NewReader(""), &out, &diag); exit == 0 {
		t.Fatal("file created during key transport was overwritten")
	}
	if raw, _ := os.ReadFile(args[3]); string(raw) != "keep-existing" {
		t.Fatal("replacement file changed")
	}
}

func TestDerivedKeyExportUsesPrivateStdinAndExclusiveFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CPH_RUN_DIR", dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{43}, 56)) + "\n"
	var calls atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/security/derived-key/export" || r.Method != "POST" {
			t.Error("wrong derived export route")
		}
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != `{"sourceId":"7","passphrase":"private-phrase-canary"}` {
			t.Error("private stdin changed")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, key)
	})}
	go server.Serve(listener)
	defer server.Close()
	var out, diag bytes.Buffer
	path := filepath.Join(dir, "derived.txt")
	args := []string{"security", "derive-key", "--output", path}
	in := `{"sourceId":"7","passphrase":"private-phrase-canary"}`
	if exit := run(args, strings.NewReader(in), &out, &diag); exit != 0 {
		t.Fatal(exit, diag.String())
	}
	raw, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(raw) != key || info.Mode().Perm() != 0600 || strings.Contains(out.String(), key) || strings.Contains(diag.String(), "canary") {
		t.Fatal("private export leaked or wrong mode")
	}
	if exit := run(args, strings.NewReader(in), &out, &diag); exit == 0 {
		t.Fatal("existing derived key overwritten")
	}
	if exit := run([]string{"security", "derive-key"}, strings.NewReader(in), &out, &diag); exit == 0 {
		t.Fatal("derived export accepted without exclusive file")
	}
	if calls.Load() != 1 {
		t.Fatal("refused export dispatched", calls.Load())
	}
}
