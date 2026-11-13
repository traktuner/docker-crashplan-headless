package app

import (
	"strings"
	"testing"
)

func TestSettingsRoutesRequireWebSessionAndExposeNoGenericProxy(t *testing.T) {
	s, _, _ := newFixture(t)
	w := request(s.WebHandler(), "GET", "/api/v1/settings/backup", "", nil, "", "")
	if w.Code != 401 {
		t.Fatal("settings session bypass", w.Code)
	}
	w = request(s.UnixHandler(), "PATCH", "/api/v1/settings/backup", `{"changes":{"serviceHost":"0.0.0.0"}}`, nil, "", "")
	if w.Code != 400 {
		t.Fatal("arbitrary setting routed", w.Code, w.Body.String())
	}
	w = request(s.UnixHandler(), "PATCH", "/api/v1/settings/backup", `{"changes":{"idleThrottleRate":30},"nativeUrl":"https://other"}`, nil, "", "")
	if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_REQUEST") {
		t.Fatal(w.Code, w.Body.String())
	}
}
