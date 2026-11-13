package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLoginConfigurationMapsAuthorityTimeoutWithoutForwardingItsBody(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(504)
		io.WriteString(w, "private authority response")
	})
	_, err := c.LoginConfiguration(context.Background(), LoginInfo{Username: "fixture", ServerAddress: "console.fixture.invalid"})
	var rejection *LoginRejection
	if !errors.As(err, &rejection) || rejection.Reason() != "SERVER_UNAVAILABLE" {
		t.Fatal("authority failure not classified", err)
	}
}

func TestLoginSetupDoesNotClearNativeAlertsWithAppCode(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/LoginSetup" || r.Method != "POST" || string(body) != "{}" {
			t.Error("unexpected model mutation or route during login setup")
		}
		io.WriteString(w, `{"authenticated":true,"authorityFullAddress":"console.fixture.invalid","username":"fixture-user"}`)
	})
	if _, err := c.LoginSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLoginSetupProjectsOnlyKnownPublicFields(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"authenticated":false,"authorityFullAddress":"console.fixture.invalid","password":"secret-canary","httpToken":"token-canary","errors":[]}`)
	})
	raw, err := c.LoginSetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "canary") || !strings.Contains(string(raw), "console.fixture.invalid") {
		t.Fatal("unfiltered setup response")
	}
}

func TestLoginConfigurationEncodesNamesAndKeepsTheNativeLoginType(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/LoginConfiguration" || r.Method != "GET" || r.URL.Query().Get("username") != "user+label@example.invalid" || r.URL.Query().Get("serverAddress") != "console.fixture.invalid" {
			t.Error("login configuration query changed")
		}
		io.WriteString(w, `{"loginType":"cloud_sso","regKey":"fixture-registration"}`)
	})
	result, err := c.LoginConfiguration(context.Background(), LoginInfo{Username: "user+label@example.invalid", ServerAddress: "console.fixture.invalid"})
	if err != nil || result.Type != "cloud_sso" || result.RegistrationKey != "fixture-registration" {
		t.Fatal(err)
	}
}
