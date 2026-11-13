package app

import (
	"context"
	"encoding/json"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/lifecycle"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRound3RegistrationKeepsDiscoveredPrivateKey(t *testing.T) {
	var sent string
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/LoginConfiguration":
			io.WriteString(w, `{"loginType":"local","regKey":"fixture-discovered-registration"}`)
		case "/v0/LoginRequest":
			var in struct {
				Key string `json:"registrationKey"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			sent = in.Key
			io.WriteString(w, `{"httpToken":"fixture-token","errors":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	c, _ := engine.NewClient(native.URL, native.Client(), time.Second)
	owner := lifecycle.New(lifecycle.Options{Native: c, MonitorInterval: time.Hour})
	owner.Begin(context.Background(), 1)
	defer owner.End(1)
	h := New(c, nil, Options{Lifecycle: owner}).UnixHandler()
	w := request(h, "POST", "/api/v1/account/configuration", `{"username":"new@example.invalid","serverAddress":"fixture.invalid","register":true}`, nil, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-discovered-registration") {
		t.Fatalf("configuration must remain private: %d", w.Code)
	}
	w = request(h, "POST", "/api/v1/account/login", `{"username":"new@example.invalid","serverAddress":"fixture.invalid","register":true,"password":"fixture-password","firstName":"New","lastName":"User"}`, nil, "", "")
	if w.Code != 200 {
		t.Fatalf("login fixture failed: %d", w.Code)
	}
	if sent != "fixture-discovered-registration" {
		t.Fatal("native-discovered registration key was discarded before native LoginRequest")
	}
}
