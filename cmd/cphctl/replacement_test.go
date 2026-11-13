package main

import (
	"strings"
	"testing"
)

func TestReplacementCompletionCLIUsesNamedConfirmedRoutes(t *testing.T) {
	for _, action := range []string{"finish", "cancel"} {
		method, path, body, ok := accountCommand([]string{"replacement", action, "--confirm", strings.ToUpper(action) + " REPLACEMENT"}, strings.NewReader(""))
		if !ok || method != "POST" || path != "account/replace-device/"+action || body.(map[string]string)["confirm"] != strings.ToUpper(action)+" REPLACEMENT" {
			t.Fatal(method, path, body, ok)
		}
	}
	if _, _, _, ok := accountCommand([]string{"replacement", "finish"}, strings.NewReader("")); ok {
		t.Fatal("unconfirmed completion accepted")
	}
}
