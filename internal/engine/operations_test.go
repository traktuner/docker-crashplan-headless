package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestNamedOperationsPreserveLargeIdentifiersAndNativeMethods(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/GetScanStats" || r.Method != "PUT" {
			t.Error("wrong native scan-stats method")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"backupSetId":9007199254740993}` {
			t.Errorf("identifier changed: %s", b)
		}
		io.WriteString(w, `{"scanning":true,"numFiles":17}`)
	})
	result, err := c.Call(context.Background(), "scan.stats", OperationRequest{Body: json.RawMessage(`{"backupSetId":9007199254740993}`)})
	if err != nil || string(result) != `{"scanning":true,"numFiles":17}` {
		t.Fatal(err)
	}
}

func TestUnknownOperationsAndRoutesNeverReachNativeEngine(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unapproved operation reached native engine") })
	c.SetToken("fixture-native-token")
	for _, name := range []string{"/v0/Command", "command", "upgrade", "backup.reset", "../status", "settings/../../Command", "backup.pause", "restore.jobs.basic", "destinations.local"} {
		if _, err := c.Call(context.Background(), name, OperationRequest{}); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.Call(context.Background(), "backupSets.update", OperationRequest{ID: "1/../../Command", Body: json.RawMessage(`{}`)}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestProtectedSettingsRequireNativeToken(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("unauthorized settings operation reached engine")
	})
	if _, err := c.Settings(context.Background(), "general"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestEmptyScanEnvelopeRemainsUnknown(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"errors":[]}`) })
	if _, err := c.Call(context.Background(), "scan.stats", OperationRequest{Body: json.RawMessage(`{"backupSetId":7}`)}); !errors.Is(err, ErrProtocol) {
		t.Fatal("empty stats must not become scanning=false", err)
	}
}

func TestNativeErrorsAndInvalidBodiesRemainFailures(t *testing.T) {
	for _, body := range []string{`<html>error</html>`, `{"errors":[{"id":"FAILED","private":"canary"}]}`, `null`} {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
		if _, err := c.Call(context.Background(), "status", OperationRequest{}); err == nil {
			t.Fatal("native failure accepted")
		}
	}
}

func TestUnlistedQueryParametersAndScalarBodiesAreRejected(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid body reached native engine") })
	c.SetToken("fixture-native-token")
	if _, err := c.Call(context.Background(), "status", OperationRequest{Query: map[string]string{"token": "injected"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), "scan.stats", OperationRequest{Body: json.RawMessage(`false`)}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestNamedLegacyResponseErrorsAreNotLost(t *testing.T) {
	for _, status := range []int{200, 400, 500, 503} {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			io.WriteString(w, `{"FirstResponseMessage":{"errors":[]},"ScanStatsMessage":{"errors":[{"id":"FAILED","message":"private-canary"}]}}`)
		})
		if _, err := c.Call(context.Background(), "status", OperationRequest{}); !errors.Is(err, ErrRejected) {
			t.Fatal("nested rejection lost", err)
		}
	}
}
