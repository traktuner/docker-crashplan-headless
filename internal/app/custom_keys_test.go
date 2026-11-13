package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/auth"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
)

func TestCustomKeyRoutesKeepExportsPrivateAndRequireWebCSRF(t *testing.T) {
	old := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{41}, 56))
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 56))
	var replaced atomic.Bool
	var calls atomic.Int32
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/v0/GetComputers":
			io.WriteString(w, `{"UsersMessage":{"myUserId":1,"users":[{"userId":1}]},"ComputersMessage":{"computers":[{"peerId":"7","userId":1,"properties":{"ME":"true","OWN":"true"}}]}}`)
		case "/v1/DataKey":
			value := old
			if replaced.Load() || r.Method == "POST" {
				value = key
			}
			json.NewEncoder(w).Encode(map[string]string{"securityKeyType": "CustomKey", "dataKey": value})
		case "/v0/SecurityKeyTypeRequest":
			replaced.Store(true)
			io.WriteString(w, `{"errors":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	client, err := engine.NewClient(native.URL, native.Client(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	owner := lifecycle.New(lifecycle.Options{Native: client, MonitorInterval: time.Hour})
	owner.Begin(context.Background(), 1)
	defer owner.End(1)
	client.SetToken("fixture")
	sessions, err := auth.New("operator", "fixture-web-password-12345", "https://backup.fixture.invalid", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	server := New(client, sessions, Options{Lifecycle: owner, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	h := server.WebHandler()
	cookie, csrf := login(t, h)
	inputs := map[string]string{
		"/api/v1/security/derived-key/export": `{"sourceId":"7","passphrase":"private-phrase-canary"}`,
		"/api/v1/security/custom-key":         `{"confirm":"REPLACE CUSTOM KEY","key":"` + key + `"}`,
	}
	for path, body := range inputs {
		before := calls.Load()
		for _, origin := range []string{"", "https://attacker.invalid"} {
			w := request(h, "POST", path, body, cookie, "", origin)
			if w.Code != 403 {
				t.Fatal("missing CSRF accepted", path, w.Code)
			}
		}
		if calls.Load() != before {
			t.Fatal("rejected request reached native engine")
		}
		w := request(h, "POST", path, body, cookie, csrf, "https://backup.fixture.invalid")
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if strings.HasSuffix(path, "/export") {
			if w.Body.String() != key+"\n" || w.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("unsafe key attachment")
			}
		} else if strings.Contains(w.Body.String(), key) || !strings.Contains(w.Body.String(), "CustomKey") {
			t.Fatal("replacement response leaked key or lost type")
		}
	}
	if strings.Contains(logs.String(), key) || strings.Contains(logs.String(), "private-phrase-canary") {
		t.Fatal("private key material logged")
	}
}
