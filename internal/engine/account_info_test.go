package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func accountFixture(t *testing.T, authenticated, status string) *Client {
	return fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/LoginSetup":
			io.WriteString(w, `{"authenticated":`+authenticated+`,"authorityFullAddress":"https://authority.example.com:443","username":"user@example.com","productVersion":"12.1.1","buildNumber":"5"}`)
		case "/v0/GetComputers":
			io.WriteString(w, `{"ComputersMessage":{"computers":[{"peerId":"9007199254740993","name":"nas","properties":{"ME":"true"}},{"peerId":"9","name":"Cloud","target":true,"properties":{"CPC":"true"}}]}}`)
		case "/v0/StatusQuery":
			if status == "" {
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, status)
		default:
			t.Error(r.URL.Path)
		}
	})
}

func TestAccountProjectsLoginDeviceAndLicense(t *testing.T) {
	c := accountFixture(t, "true", `{"StatusResponseMessage":{"user":{"username":"user@example.com","email":"user@example.com"},
		"licenseMessage":{"username":"user@example.com","license":{"name":"CP_LICENSE","key":"private-canary","expireDate":1900000000000,"permissions":["BACKUP"]}},
		"websiteHost":"https://www.example.com"}}`)
	c.SetToken("fixture")
	info, err := c.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := AccountInfo{Username: "user@example.com", Server: "https://authority.example.com:443", DeviceName: "nas", DeviceGUID: "9007199254740993",
		ProductVersion: "12.1.1", LicenseType: "CP_LICENSE", LicenseExpires: "1900000000000"}
	if info != want {
		t.Fatalf("got %+v", info)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "private-canary") || strings.Contains(string(raw), "permissions") {
		t.Fatal("license internals leaked")
	}
}

func TestAccountWithoutLicenseOmitsIt(t *testing.T) {
	c := accountFixture(t, "true", "")
	c.SetToken("fixture")
	info, err := c.Account(context.Background())
	if err != nil || info.LicenseType != "" || info.LicenseExpires != "" || info.DeviceGUID != "9007199254740993" {
		t.Fatal(info, err)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "license") {
		t.Fatal("empty license fields not omitted", string(raw))
	}
}

func TestAccountRequiresSignIn(t *testing.T) {
	c := accountFixture(t, "false", "")
	if _, err := c.Account(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("no native token", err)
	}
	c.SetToken("fixture")
	if _, err := c.Account(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("engine not signed in", err)
	}
}
