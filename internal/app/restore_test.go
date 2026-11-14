package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/ctl"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
	"github.com/traktuner/crashplan-headless/internal/restore"
	"github.com/traktuner/crashplan-headless/internal/statefile"
)

func restoreFixture(t *testing.T) (*Server, *atomic.Int32) {
	t.Helper()
	root := t.TempDir()
	adds := &atomic.Int32{}
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/StatusQuery":
			io.WriteString(w, `{"user":{"userId":17}}`)
		case "/v0/RestorePrivateKeyRequest":
			io.WriteString(w, `{"securityKeyInfoList":[{"sourceGuid":"7","dataKey":{"array":"YQ=="}}]}`)
		case "/v1/Setting/device_engine_writepermission_enforcement_enabled":
			io.WriteString(w, `{"device_engine_writepermission_enforcement_enabled":"false"}`)
		case "/v1/AddRestore":
			adds.Add(1)
			io.WriteString(w, `{"restoreId":"99","errors":[]}`)
		case "/v0/RestoreJobsRequest":
			fmt.Fprintf(w, `{"jobs":[{"restoreId":"99","sourceId":"7","targetId":"8","completed":false,"canceled":false,"restoreType":"CLIENT","userName":"root","restorePath":%q,"numFilesToRestore":1,"numFilesRestored":0,"numBytesToRestore":"10","numBytesRestored":"0","privateKey":"secret-canary"}]}`, root)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(native.Close)
	client, err := engine.NewClient(native.URL, native.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := restore.NewDestinations([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { roots.Close() })
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service, err := restore.NewService(client, roots, store, "root")
	if err != nil {
		t.Fatal(err)
	}
	owner := lifecycle.New(lifecycle.Options{Native: client, MonitorInterval: time.Hour})
	if err := owner.Begin(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.End(1) })
	client.SetToken("fixture")
	sessions, _, _ := newFixture(t)
	return New(client, sessions.sessions, Options{Lifecycle: owner, Restores: service}), adds
}

const restoreBody = `{"requestId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sourceId":"7","targetId":"8","target":{"rootId":"0","relative":""},"existing":"RENAME_ORIGINAL","permissions":"current","includeDeleted":false,"items":[{"path":"/data/file","backupSetId":"1","timestamp":"0","isFolder":false}]}`

func TestPublicRestoreRoutesShareAdmissionAndDoNotReplay(t *testing.T) {
	s, adds := restoreFixture(t)
	for i := 0; i < 2; i++ {
		w := request(s.UnixHandler(), "POST", "/api/v1/restore/jobs", restoreBody, nil, "", "")
		if w.Code != 202 || !strings.Contains(w.Body.String(), `"restoreId":"99"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if adds.Load() != 1 {
		t.Fatal("duplicate native restore", adds.Load())
	}
	for _, path := range []string{"roots", "jobs", "operations"} {
		w := request(s.UnixHandler(), "GET", "/api/v1/restore/"+path, "", nil, "", "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "secret-canary") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
}
func TestPublicRestoreRejectsUnknownFieldsAndUnconfiguredRoots(t *testing.T) {
	s, adds := restoreFixture(t)
	for _, body := range []string{strings.Replace(restoreBody, `"rootId":"0"`, `"rootId":"9"`, 1), strings.Replace(restoreBody, `"sourceId":"7"`, `"sourceId":"7","nativeToken":"bypass"`, 1)} {
		w := request(s.UnixHandler(), "POST", "/api/v1/restore/jobs", body, nil, "", "")
		if w.Code < 400 || adds.Load() != 0 {
			t.Fatal("invalid restore accepted", w.Code)
		}
	}
}
func TestWebRestoreRequiresSessionAndCSRF(t *testing.T) {
	s, adds := restoreFixture(t)
	w := request(s.WebHandler(), "POST", "/api/v1/restore/jobs", restoreBody, nil, "", "https://backup.fixture.invalid")
	if w.Code != 401 || adds.Load() != 0 {
		t.Fatal("session bypass", w.Code)
	}
	cookie, csrf := login(t, s.WebHandler())
	w = request(s.WebHandler(), "POST", "/api/v1/restore/jobs", restoreBody, cookie, "", "https://backup.fixture.invalid")
	if w.Code != 403 || adds.Load() != 0 {
		t.Fatal("CSRF bypass", w.Code)
	}
	w = request(s.WebHandler(), "POST", "/api/v1/restore/jobs", restoreBody, cookie, csrf, "https://backup.fixture.invalid")
	if w.Code != 202 || adds.Load() != 1 {
		t.Fatal("authorized web restore failed", w.Code, w.Body.String())
	}
}

func TestRestoreCannotResumeJobOutsideConfiguredRoots(t *testing.T) {
	s, _ := restoreFixture(t)
	roots, err := restore.NewDestinations([]string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer roots.Close()
	store, err := statefile.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.options.Restores, err = restore.NewService(s.native, roots, store, "root")
	if err != nil {
		t.Fatal(err)
	}
	w := request(s.UnixHandler(), "POST", "/api/v1/restore/jobs/99/start", `{}`, nil, "", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "RESTORE_PENDING_JOB_OUTSIDE_ROOTS") {
		t.Fatal("outside restore resumed", w.Code, w.Body.String())
	}
}

func TestIndeterminateRestoreTakesPriorityOverJournalError(t *testing.T) {
	if code := errorCode(errors.Join(engine.ErrIndeterminate, restore.ErrJournal)); code != "NATIVE_OPERATION_INDETERMINATE" {
		t.Fatal("potential mutation hidden by persistence error", code)
	}
}

// Drive the same private socket exported by cphd through the public CLI client.
// The native side remains a fixture, not an account-backed restore acceptance.
func TestRestorePublicUnixRoundTrip(t *testing.T) {
	s, adds := restoreFixture(t)
	socket := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: s.UnixHandler()}
	go server.Serve(listener)
	defer server.Close()
	client := ctl.New(socket)
	var body map[string]any
	if err := json.Unmarshal([]byte(restoreBody), &body); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		raw, status, err := client.Request(context.Background(), "POST", "restore/jobs", body)
		if err != nil || status != 202 || !strings.Contains(string(raw), `"state":"accepted"`) {
			t.Fatal(status, err, string(raw))
		}
	}
	if adds.Load() != 1 {
		t.Fatal("socket repeated native restore", adds.Load())
	}
	body["target"] = map[string]string{"rootId": "9"}
	raw, status, err := client.Request(context.Background(), "POST", "restore/jobs", body)
	if err != nil || status != 400 || !strings.Contains(string(raw), "INVALID_REQUEST") || adds.Load() != 1 {
		t.Fatal("changed replay crossed public boundary", status, err, string(raw))
	}
}

func TestRestoreJobsCarryStateAndClearCompletedAnswersCount(t *testing.T) {
	s, _ := restoreFixture(t)
	w := request(s.UnixHandler(), "GET", "/api/v1/restore/jobs", "", nil, "", "")
	for _, field := range []string{`"state":"waiting"`, `"rateBytesPerSecond":"0"`, `"remainingMs":"-1"`} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), field) {
			t.Fatal("restore job contract field missing", field, w.Code, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "blockedReason") {
		t.Fatal("empty blockedReason must be omitted", w.Body.String())
	}
	// The only fixture job is not completed: nothing is removed.
	w = request(s.UnixHandler(), "POST", "/api/v1/restore/jobs/clear-completed", `{}`, nil, "", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"data":{"removed":0}}` {
		t.Fatal(w.Code, w.Body.String())
	}
}
