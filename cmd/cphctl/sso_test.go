package main

import (
	"strings"
	"testing"
)

func TestSSOConfirmationCLIRequiresPresentedIdentity(t *testing.T) {
	id, assertion := strings.Repeat("a", 32), strings.Repeat("b", 32)
	method, path, body, ok := accountCommand([]string{"sso-confirm", id, assertion, "--confirm", "canonical"}, strings.NewReader(""))
	if !ok || method != "POST" || path != "account/sso/"+id+"/confirm" || body.(map[string]string)["username"] != "canonical" || body.(map[string]string)["assertionId"] != assertion {
		t.Fatal(method, path, body, ok)
	}
	if _, _, _, ok := accountCommand([]string{"sso-confirm", id, assertion}, strings.NewReader("")); ok {
		t.Fatal("unconfirmed SSO accepted")
	}
}
