package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRestoreContextUsesAuthenticatedNativeKeyWithoutExposingIt(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token fixture" {
			t.Error("native authorization missing")
		}
		switch r.URL.Path {
		case "/v1/Setting/device_engine_writepermission_enforcement_enabled":
			io.WriteString(w, `{"device_engine_writepermission_enforcement_enabled":"true"}`)
		case "/v0/StatusQuery":
			io.WriteString(w, `{"user":{"userId":17},"password":"do-not-forward"}`)
		case "/v0/RestorePrivateKeyRequest":
			io.WriteString(w, `{"securityKeyInfoList":[{"sourceGuid":"7","dataKey":{"array":"cHJpdmF0ZS1rZXktY2FuYXJ5"}}]}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	})
	c.SetToken("fixture")
	ctx, err := c.RestoreContext(context.Background(), "7")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ctx)
	if strings.Contains(string(raw), "canary") || strings.Contains(string(raw), "cHJpdm") {
		t.Fatal("archive key serialized")
	}
	if ctx.UserID() != 17 || !ctx.RequiresMarker() {
		t.Fatal("wrong account user")
	}
}

func TestRestoreControlUsesOneNamedActionAndValidatesBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var in map[string]string
		if r.Method != "PUT" || r.URL.Path != "/v0/ControlRestore" || json.NewDecoder(r.Body).Decode(&in) != nil || in["action"] != "STOP" || in["restoreId"] != "99" {
			t.Error("wrong native control")
		}
		io.WriteString(w, `{"jobs":[],"errors":[]}`)
	})
	c.SetToken("fixture")
	if err := c.ControlRestore(context.Background(), "99", "STOP"); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"", "RESTART", "STOP;reset"} {
		if err := c.ControlRestore(context.Background(), "99", action); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
	if err := c.ControlRestore(context.Background(), "99/unsafe", "STOP"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("control replayed", calls.Load())
	}
}

func TestRestoreControlDoesNotTreatEmptyHTTP200AsAcceptance(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	c.SetToken("fixture")
	if err := c.ControlRestore(context.Background(), "99", "STOP"); !errors.Is(err, ErrIndeterminate) {
		t.Fatal("empty restore control accepted", err)
	}
}

func TestRestoreCheckAndSubmitUseExactNativeFieldsAndDoNotReplay(t *testing.T) {
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/v0/RestoreCheckRequest":
			io.WriteString(w, `{"restoreId":"99","fileName":"/tmp/99.txt","errors":[]}`)
		case "/v1/AddRestore":
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Fatal("bad request")
			}
			for _, key := range []string{"restoreId", "userId", "sourceId", "targetId", "numFilesToRestore", "numBytesToRestore", "osUid", "osGid", "includeDeleted", "useOsUids", "files", "clientRestoreSubType", "restorePath", "existingFilesRestoreStrategy"} {
				if _, ok := body[key]; !ok {
					t.Errorf("missing %s", key)
				}
			}
			if string(body["sourceId"]) != `"7"` || string(body["numBytesToRestore"]) != `"0"` {
				t.Error("native long encoding")
			}
			io.WriteString(w, `{"restoreId":"99","errors":[]}`)
		default:
			t.Error("unexpected route")
		}
	})
	c.SetToken("fixture")
	check, err := c.CheckRestore(context.Background())
	if err != nil || check.ID != "99" || check.Marker != "/tmp/99.txt" {
		t.Fatal(check, err)
	}
	req := RestoreSubmission{ID: "99", SourceID: "7", TargetID: "8", Destination: "/restore", Existing: "RENAME_ORIGINAL", Permissions: "current", Items: []RestoreSelection{{Path: "/data/file", BackupSetID: "1", Timestamp: "0"}}}
	if id, err := c.AddRestore(context.Background(), RestoreCredentials{revision: c.SessionRevision(), userID: 17, verified: true, helperRequired: true, key: "YQ=="}, req); err != nil || id != "99" {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("native action replayed", calls)
	}
}

func TestRestoreMalformedInputsNeverReachNativeEngine(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("bad restore reached engine") })
	c.SetToken("fixture")
	for _, r := range []RestoreSubmission{{}, {ID: "1", SourceID: "7", TargetID: "8", Destination: "/restore", Existing: "UNKNOWN", Permissions: "current", Items: []RestoreSelection{{Path: "/data/x", BackupSetID: "1", Timestamp: "0"}}}} {
		if _, err := c.AddRestore(context.Background(), RestoreCredentials{revision: c.SessionRevision(), userID: 17, verified: true, helperRequired: true, key: "YQ=="}, r); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
}

func TestRestoreJobsExposeOnlyTypedSafeFields(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jobs":[{"restoreId":"99","sourceId":"7","targetId":"8","completed":false,"canceled":false,"userName":"root","restoreType":"CLIENT","restorePath":"/restore","numFilesToRestore":2,"numFilesRestored":1,"numBytesToRestore":"22","numBytesRestored":"11","privateKey":"private-canary","dataPassword":"password-canary"}],"errors":[]}`)
	})
	c.SetToken("fixture")
	jobs, err := c.RestoreJobs(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(jobs)
	if strings.Contains(string(raw), "canary") || strings.Contains(string(raw), "privateKey") || strings.Contains(string(raw), "dataPassword") {
		t.Fatal("raw native job forwarded")
	}
	if !jobs[0].UsesHelper() || jobs[0].Destination() != "/restore" || jobs[0].UserName() != "root" {
		t.Fatal("helper admission metadata lost")
	}
}

func TestLegacyRestoreJobRepliesCannotCrossConcurrentRequests(t *testing.T) {
	var active atomic.Int32
	var maxActive atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
		}
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, `{"jobs":[],"errors":[]}`)
	})
	c.SetToken("fixture")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.RestoreJobs(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatal("same legacy response class overlapped", maxActive.Load())
	}
}

func TestRestoreJobsPreserveNullVersusEmptyHelperUser(t *testing.T) {
	for _, user := range []string{`null`, `""`, `"root"`} {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"jobs":[{"restoreId":"99","sourceId":"7","targetId":"8","completed":false,"canceled":false,"userName":%s,"restoreType":"CLIENT","numFilesToRestore":0,"numFilesRestored":0,"numBytesToRestore":"0","numBytesRestored":"0"}],"errors":[]}`, user)
		})
		c.SetToken("fixture")
		jobs, err := c.RestoreJobs(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if jobs[0].UsesHelper() != (user != `null`) {
			t.Fatal("native null and empty helper user were conflated", user)
		}
	}
}

func TestNativeDirectRestoreUsesReturnedNativeID(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/StatusQuery":
			io.WriteString(w, `{"user":{"userId":17}}`)
		case "/v0/RestorePrivateKeyRequest":
			io.WriteString(w, `{"securityKeyInfoList":[{"sourceGuid":"7","dataKey":{"array":"YQ=="}}]}`)
		case "/v1/Setting/device_engine_writepermission_enforcement_enabled":
			io.WriteString(w, `{"device_engine_writepermission_enforcement_enabled":"false"}`)
		case "/v1/AddRestore":
			io.WriteString(w, `{"restoreId":"100","errors":[]}`)
		default:
			t.Fatal("unexpected native call, including speculative RestoreCheck")
		}
	})
	c.SetToken("fixture")
	credentials, err := c.RestoreContext(context.Background(), "7")
	if err != nil || credentials.RequiresMarker() {
		t.Fatal("native direct mode lost", err)
	}
	request := RestoreSubmission{ID: "0", SourceID: "7", TargetID: "8", Destination: "/restore", Existing: "RENAME_ORIGINAL", Permissions: "current", Items: []RestoreSelection{{Path: "/data/x", BackupSetID: "1", Timestamp: "0"}}}
	id, err := c.AddRestore(context.Background(), credentials, request)
	if err != nil || id != "100" {
		t.Fatal("direct restore generated ID lost", id, err)
	}
}

func TestRestoreEnforcementDefaultIsOnlyUsedForNative404(t *testing.T) {
	for _, status := range []int{404, 503, 200} {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v0/StatusQuery":
				io.WriteString(w, `{"user":{"userId":17}}`)
			case "/v0/RestorePrivateKeyRequest":
				io.WriteString(w, `{"securityKeyInfoList":[{"sourceGuid":"7","dataKey":{"array":"YQ=="}}]}`)
			case "/v1/Setting/device_engine_writepermission_enforcement_enabled":
				w.WriteHeader(status)
				io.WriteString(w, `{"device_engine_writepermission_enforcement_enabled":"invalid"}`)
			default:
				t.Error("unexpected context call")
			}
		})
		c.SetToken("fixture")
		credentials, err := c.RestoreContext(context.Background(), "7")
		if status == 404 {
			if err != nil || !credentials.RequiresMarker() {
				t.Fatal("native default lost", err)
			}
		} else if err == nil {
			t.Fatal("unavailable/malformed setting silently defaulted", status)
		}
	}
}
