package app

import (
	"net/http"
	"testing"
)

func TestLocalArchiveAndContentsUseSessionAndCSRF(t *testing.T) {
	s, calls, _ := newFixture(t)
	for _, path := range []string{"/api/v1/destinations/local/policy", "/api/v1/destinations/local/operations/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		w := request(s.WebHandler(), "GET", path, "", nil, "", "")
		if w.Code != http.StatusUnauthorized {
			t.Fatal(w.Code)
		}
	}
	cookie, _ := login(t, s.WebHandler())
	before := calls.Load()
	for _, path := range []string{"/api/v1/destinations/local", "/api/v1/restore/contents"} {
		w := request(s.WebHandler(), "POST", path, `{}`, cookie, "", "https://backup.fixture.invalid")
		if w.Code != http.StatusForbidden {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls.Load() != before {
		t.Fatal("untrusted mutation reached native transport")
	}
}

func TestDeviceOperationsRequireSessionAndCSRF(t *testing.T) {
	s, calls, _ := newFixture(t)
	for _, path := range []string{"/api/v1/devices/operations/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "/api/v1/devices"} {
		if w := request(s.WebHandler(), "GET", path, "", nil, "", ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	cookie, _ := login(t, s.WebHandler())
	before := calls.Load()
	if w := request(s.WebHandler(), "POST", "/api/v1/devices/operations", `{"action":"remove"}`, cookie, "", "https://backup.fixture.invalid"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if calls.Load() != before {
		t.Fatal("untrusted device operation reached native API")
	}
}
