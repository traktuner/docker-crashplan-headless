package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests exercise the real native HTTP boundary, with a disposable engine.
func fixture(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	c, err := NewClient(s.URL, s.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestScanUsesNativeTokenAndExactIDWithoutRounding(t *testing.T) {
	var calls atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v0/Command" || r.Method != "POST" {
			t.Errorf("wrong native operation: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "token test-native-token" {
			t.Error("missing native authorization")
		}
		b, _ := io.ReadAll(r.Body)
		var body struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			t.Error(err)
		}
		if body.Command != "backup.scan true 9007199254740993" {
			t.Errorf("wrong command %q", body.Command)
		}
		io.WriteString(w, `{"errors":[],"results":[{"command":{"name":"backup.scan"},"arguments":["true","9007199254740993"]}]}`)
	})
	c.SetToken("test-native-token")
	if err := c.Scan(context.Background(), "9007199254740993"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("scan must dispatch exactly once")
	}
}

func TestScanRejectsInvalidIDsBeforeDispatch(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached engine") })
	c.SetToken("test-native-token")
	for _, id := range []string{"", "0", "-1", "1,backup.reset", "1 false", "9223372036854775808", "01", "1\n"} {
		if err := c.Scan(context.Background(), id); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("id %q: %v", id, err)
		}
	}
}

func TestScanRequiresNativeLoginBeforeDispatch(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("missing token reached engine") })
	if err := c.Scan(context.Background(), "7"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestNative401DoesNotRetryOrExposePrivateBody(t *testing.T) {
	var n atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(401)
		io.WriteString(w, `{"private":"secret-canary"}`)
	})
	c.SetToken("test-native-token")
	err := c.Scan(context.Background(), "7")
	if !errors.Is(err, ErrUnauthorized) || strings.Contains(err.Error(), "secret-canary") {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatal("authorization denial retried")
	}
	if c.Authorized() {
		t.Fatal("rejected native token was retained")
	}
}

func TestNativeResultErrorAndMissingResultAreNotAcceptance(t *testing.T) {
	for _, body := range []string{`{}`, `{"results":[]}`, `{"results":[{"error":"DENIED"}]}`, `{"errors":[{"id":"FAILED"}],"results":[{}]}`, `{"results":[{},{}]}`, `{"results":[{}]} trailing`, `[]`} {
		t.Run(body, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			c.SetToken("test-native-token")
			if err := c.Scan(context.Background(), "7"); err == nil {
				t.Fatal("invalid native response accepted")
			}
		})
	}
}

func TestAmbiguousNetworkFailureNeverReplaysScan(t *testing.T) {
	var n atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { n.Add(1); panic(http.ErrAbortHandler) })
	c.SetToken("test-native-token")
	if err := c.Scan(context.Background(), "7"); !errors.Is(err, ErrIndeterminate) {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatal("ambiguous dispatch replayed")
	}
}

func TestClientRejectsNonLoopbackAndRedirects(t *testing.T) {
	for _, base := range []string{"https://example.com:4243", "http://127.0.0.1:4243", "https://localhost:4243", "https://127.0.0.1:4243/path", "https://u:p@127.0.0.1:4243", "https://127.0.0.1:4243?token=x"} {
		if _, err := NewClient(base, http.DefaultClient, time.Second); err == nil {
			t.Errorf("unsafe engine address accepted: %s", base)
		}
	}
	var redirected atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(307)
	})
	c.SetToken("test-native-token")
	if err := c.Scan(context.Background(), "7"); err == nil {
		t.Fatal("redirect accepted")
	}
	if redirected.Load() != 0 {
		t.Fatal("native token followed redirect")
	}
}

func TestChangedGuestAuthorizationDoesNotDestroyAnotherNativeSession(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	c.SetToken("fixture-native-token")
	if _, err := c.Call(context.Background(), "status", OperationRequest{}); !errors.Is(err, ErrProtocol) {
		t.Fatal("changed guest auth contract was not surfaced", err)
	}
	if !c.Authorized() {
		t.Fatal("guest request invalidated an unrelated native token")
	}
}

func TestHTTPBusinessFailureCannotProveScanDidNotAct(t *testing.T) {
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(400)
		io.WriteString(w, `{"errors":[{"id":"FAILED"}]}`)
	})
	c.SetToken("fixture")
	if err := c.Scan(context.Background(), "7"); !errors.Is(err, ErrIndeterminate) || calls != 1 {
		t.Fatal("ambiguous scan replayed or rejected as no-action", err)
	}
}
