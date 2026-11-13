package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
)

func TestSecurityPublicRoutesRejectUntrustedFieldsAndKeepKeysPrivate(t *testing.T) {
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			io.WriteString(w, `{"UsersMessage":{"myUserId":"1"},"ComputersMessage":{"computers":[{"peerId":"7","userId":"1","properties":{"ME":"true"}}]}}`)
		case "/v1/DataKey":
			io.WriteString(w, `{"securityKeyType":"PrivatePassword","dataKey":"private-key-canary"}`)
		case "/v1/PrivatePasswordDataKey":
			io.WriteString(w, `{"dataKey":"a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="}`)
		case "/v1/ApplicationStatus":
			io.WriteString(w, `{"productVersion":"12.1.1","buildNumber":"2"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	client, _ := engine.NewClient(native.URL, native.Client(), time.Second)
	owner := lifecycle.New(lifecycle.Options{Native: client, MonitorInterval: time.Hour})
	owner.Begin(context.Background(), 1)
	defer owner.End(1)
	client.SetToken("fixture")
	s := New(client, nil, Options{Lifecycle: owner})
	h := s.UnixHandler()
	w := request(h, "GET", "/api/v1/security", "", nil, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-key-canary") {
		t.Fatal("unsafe security state", w.Code)
	}
	w = request(h, "POST", "/api/v1/security/unlock", `{"sourceId":"7","type":"PrivatePassword","secret":"fixture-secret"}`, nil, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "a2tra2tr") || strings.Contains(w.Body.String(), "fixture-secret") {
		t.Fatal("unsafe unlock response", w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/security/unlock", `{"sourceId":"7","type":"PrivatePassword","secret":"fixture-secret","importFile":"/etc/passwd"}`, nil, "", "")
	if w.Code != 400 {
		t.Fatal("unknown import field accepted", w.Code)
	}
	web, _, _ := newFixture(t)
	for _, path := range []string{"/api/v1/security/unlock", "/api/v1/security/key-type", "/api/v1/security/password", "/api/v1/security/backup-unlock", "/api/v1/security/export-key", "/api/v1/security/derived-key/export", "/api/v1/security/custom-key"} {
		w = request(web.WebHandler(), "POST", path, `{}`, nil, "", "")
		if w.Code != 401 {
			t.Fatal("security route needs a session", path, w.Code)
		}
	}
}
