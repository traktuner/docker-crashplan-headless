package app

import (
	"context"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublicReplacementCompletionIsTypedAndReadsBackState(t *testing.T) {
	active := true
	writes := 0
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/GetComputers" {
			io.WriteString(w, `{"UsersMessage":{"myUserId":"1"},"ComputersMessage":{"computers":[{"peerId":"7","userId":"1","properties":{"ME":"true"}}]}}`)
			return
		}
		if r.URL.Path != "/v1/ReplaceDevice" {
			t.Error(r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method == "PUT" {
			writes++
			active = false
			return
		}
		if active {
			io.WriteString(w, `{"available":true,"inProgress":true,"deviceSelectedGuid":"9","screenState":"/replace-device/complete"}`)
		} else {
			io.WriteString(w, `{"available":false,"inProgress":false,"deviceSelectedGuid":"null"}`)
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
	for _, body := range []string{`{"confirm":"wrong"}`, `{"confirm":"FINISH REPLACEMENT","inProgress":true}`} {
		w := request(h, "POST", "/api/v1/account/replace-device/finish", body, nil, "", "")
		if w.Code != 400 || writes != 0 {
			t.Fatal("untyped or unconfirmed reset", w.Code, writes)
		}
	}
	w := request(h, "POST", "/api/v1/account/replace-device/finish", `{"confirm":"FINISH REPLACEMENT"}`, nil, "", "")
	if w.Code != 200 || writes != 1 || active {
		t.Fatal(w.Code, w.Body.String(), writes)
	}
}
