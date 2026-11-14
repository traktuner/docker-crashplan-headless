package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestNativeRestoreHelperRunsOnceAndEndsWithItsGeneration(t *testing.T) {
	data := t.TempDir()
	destination := t.TempDir()
	logs := t.TempDir()
	req := filepath.Join(data, "restore-pipe-123-request")
	resp := filepath.Join(data, "restore-pipe-123-response")
	if syscall.Mkfifo(req, 0600) != nil || syscall.Mkfifo(resp, 0600) != nil {
		t.Fatal("mkfifo")
	}
	script := []byte("#!/bin/sh\nprintf '{\"RequestPipePath\":\"%s\",\"ResponsePipePath\":\"%s\",\"Error\":\"\"}\\n' \"$5\" \"$6\"\nexec /bin/sleep 60\n")
	binary := filepath.Join(t.TempDir(), "helper")
	os.WriteFile(binary, script, 0700)
	sum := sha256.Sum256(script)
	send := make(chan map[string]string, 1)
	var jobDestination atomic.Value
	jobDestination.Store(destination)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/wsevent":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			message := map[string]any{"LaunchRestoreToolMessage": map[string]string{"userName": "root", "requestPipePath": req, "responsePipePath": resp}}
			if wsjson.Write(r.Context(), conn, message) != nil {
				return
			}
			go func() {
				for {
					select {
					case payload := <-send:
						if wsjson.Write(r.Context(), conn, map[string]any{"LaunchRestoreToolMessage": payload}) != nil {
							return
						}
					case <-r.Context().Done():
						return
					}
				}
			}()
			for {
				var value any
				if wsjson.Read(r.Context(), conn, &value) != nil {
					return
				}
			}
		case "/v0/RestoreJobsRequest":
			fmt.Fprintf(w, `{"jobs":[{"restoreId":"99","sourceId":"7","targetId":"8","completed":false,"canceled":false,"userName":"root","restoreType":"CLIENT","restorePath":%q,"numFilesToRestore":1,"numFilesRestored":0,"numBytesToRestore":"1","numBytesRestored":"0"}],"errors":[]}`, jobDestination.Load().(string))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	native.SetToken("fixture")
	defer native.CloseEvents()
	roots, _ := NewDestinations([]string{destination})
	defer roots.Close()
	h, err := NewHelpers(HelperOptions{Native: native, Destinations: roots, Binary: binary, ExpectedSHA: hex.EncodeToString(sum[:]), DataDir: data, LogDir: logs, UserName: "root", Console: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.Begin(ctx, 1); err != nil {
		t.Fatal(err)
	}
	defer h.End(1)
	if _, err := native.OpenEvents(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for h.Snapshot().State != "running" {
		if time.Now().After(deadline) {
			t.Fatal("helper did not run", h.Snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
	pid := h.Snapshot().PID
	send <- map[string]string{"userName": "root", "requestPipePath": req, "responsePipePath": resp}
	time.Sleep(50 * time.Millisecond)
	if h.Snapshot().PID != pid || h.Snapshot().Starts != 1 {
		t.Fatal("duplicate native launch created another helper")
	}
	send <- map[string]string{"userName": "other", "requestPipePath": req, "responsePipePath": resp}
	deadline = time.Now().Add(time.Second)
	for h.Snapshot().Reason == "" {
		if time.Now().After(deadline) {
			t.Fatal("invalid launch was not reported")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if h.Snapshot().PID != pid {
		t.Fatal("rejected event hid a live helper", h.Snapshot())
	}
	cancel()
	if err := h.End(1); err != nil {
		t.Fatal(err)
	}
	if h.Snapshot().PID != 0 {
		t.Fatal("helper outlived generation")
	}
	// An imported pending job must be checked through the same actual event path.
	native.CloseEvents()
	jobDestination.Store("/outside")
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	if err := h.Begin(second, 2); err != nil {
		t.Fatal(err)
	}
	defer h.End(2)
	if _, err := native.OpenEvents(second); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for h.Snapshot().State != "rejected" {
		if time.Now().After(deadline) {
			t.Fatal("inadmissible imported job launched a helper", h.Snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if h.Snapshot().Reason != ErrPendingJob.Error() || h.Snapshot().PID != 0 || h.Snapshot().Starts != 0 {
		t.Fatal("wrong imported-job outcome", h.Snapshot())
	}
	cancelSecond()
}

func TestHelperRefusesForeignParentRegularPipesAndUntrustedBinary(t *testing.T) {
	data := t.TempDir()
	other := t.TempDir()
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	roots, _ := NewDestinations(nil)
	defer roots.Close()
	h, err := NewHelpers(HelperOptions{Native: native, Destinations: roots, Binary: "/bin/true", ExpectedSHA: strings.Repeat("0", 64), DataDir: data, LogDir: t.TempDir(), UserName: "root"})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(data)
	g := &helperGeneration{dataIdentity: info}
	request := filepath.Join(other, "restore-pipe-1-request")
	response := filepath.Join(other, "restore-pipe-1-response")
	syscall.Mkfifo(request, 0600)
	syscall.Mkfifo(response, 0600)
	if _, err := h.pipes(g, launchMessage{"root", request, response}); !errors.Is(err, ErrHelper) {
		t.Fatal("foreign pipe parent accepted")
	}
	request = filepath.Join(data, "restore-pipe-1-request")
	response = filepath.Join(data, "restore-pipe-1-response")
	os.WriteFile(request, nil, 0600)
	syscall.Mkfifo(response, 0600)
	if _, err := h.pipes(g, launchMessage{"root", request, response}); !errors.Is(err, ErrHelper) {
		t.Fatal("regular file used as native FIFO")
	}
	if err := h.verifyBinary(g); !errors.Is(err, ErrHelper) {
		t.Fatal("untrusted helper hash accepted")
	}
}
func TestImportedHelperJobOutsideRestoreRootsIsRefused(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jobs":[{"restoreId":"99","sourceId":"7","targetId":"8","completed":false,"canceled":false,"userName":"root","restoreType":"CLIENT","restorePath":"/outside","numFilesToRestore":1,"numFilesRestored":0,"numBytesToRestore":"1","numBytesRestored":"0"}],"errors":[]}`)
	}))
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	native.SetToken("fixture")
	roots, _ := NewDestinations([]string{t.TempDir()})
	defer roots.Close()
	h, err := NewHelpers(HelperOptions{Native: native, Destinations: roots, Binary: "/bin/true", ExpectedSHA: strings.Repeat("0", 64), DataDir: t.TempDir(), LogDir: t.TempDir(), UserName: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.admit(context.Background()); !errors.Is(err, ErrPendingJob) {
		t.Fatal("imported job bypassed destination admission", err)
	}
}

func TestHelperDataDirectoryRefusalNamesItsReason(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	native, _ := engine.NewClient(server.URL, server.Client(), time.Second)
	roots, _ := NewDestinations(nil)
	defer roots.Close()
	shared := t.TempDir()
	if err := os.Chmod(shared, 0777); err != nil {
		t.Fatal(err)
	}
	for reason, data := range map[string]string{"writable_by_others": shared, "unavailable": filepath.Join(t.TempDir(), "missing")} {
		var logs bytes.Buffer
		h, err := NewHelpers(HelperOptions{Native: native, Destinations: roots, Binary: "/bin/true", ExpectedSHA: strings.Repeat("0", 64), DataDir: data, LogDir: t.TempDir(), UserName: "root", Logger: slog.New(slog.NewTextHandler(&logs, nil))})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Begin(context.Background(), 1); !errors.Is(err, ErrHelper) {
			t.Fatal("unsafe helper directory accepted", reason, err)
		}
		text := logs.String()
		if !strings.Contains(text, "msg=restore_helper_directory_refused") || !strings.Contains(text, "reason="+reason) || strings.Contains(text, data) {
			t.Fatal("helper directory refusal is not explained safely:", reason, text)
		}
	}
}
