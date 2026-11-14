package engine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAuthorityLookupMatchesDesktopSelector(t *testing.T) {
	var query url.Values
	var agent string
	answer := `[{"primaryAddress":"https://console.us2.crashplan.com"}]`
	selector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		agent = r.UserAgent()
		w.Write([]byte(answer))
	}))
	defer selector.Close()
	saved := authorityClient
	authorityClient = selector.Client()
	defer func() { authorityClient = saved }()
	target, _ := url.Parse(selector.URL + "/api/authority")

	address, err := lookupAuthority(context.Background(), target, "user@example.org", "12.1.1")
	if err != nil || address != "https://console.us2.crashplan.com" {
		t.Fatal(address, err)
	}
	if query.Get("username") != "user@example.org" || len(query.Get("uuid")) != 36 || query.Get("uuid")[14] != '4' || agent != "CrashPlan/12.1.1 Electron CrashPlan-Headless" {
		t.Fatal("selector query", query)
	}
	for body, reason := range map[string]string{
		`[]`: "ACCOUNT_NOT_FOUND",
		`[{"primaryAddress":"a"},{"primaryAddress":"b"}]`: "AUTHORITY_REQUIRED",
	} {
		answer = body
		_, err := lookupAuthority(context.Background(), target, "user@example.org", "12.1.1")
		var rejection *LoginRejection
		if !errors.As(err, &rejection) || rejection.Reason() != reason {
			t.Fatal(body, err)
		}
	}
	answer = `{"not":"a list"}`
	if _, err := lookupAuthority(context.Background(), target, "user@example.org", "12.1.1"); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
}
