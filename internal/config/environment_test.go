package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNativeEnvironmentDoesNotCarryWebCredentials(t *testing.T) {
	env := NativeEnvironment([]string{"CPH_WEB_PASSWORD=private-canary", "AWS_SECRET_ACCESS_KEY=other-canary", "CPH_PATCHES=no-file-watcher,no-watcher-walk", "LANG=C.UTF-8"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "canary") || !strings.Contains(joined, "CPH_PATCHES=") {
		t.Fatal("native environment leaks secrets or drops required patch setting")
	}
}

func TestRestoreRootsEnvironmentIsGeneratedFromValidatedConfiguration(t *testing.T) {
	env := NativeEnvironment([]string{"CPH_RESTORE_ROOTS_B64=untrusted"}, "/restore one", "/restore:two")
	want := "CPH_RESTORE_ROOTS_B64=" + base64.StdEncoding.EncodeToString([]byte("/restore one")) + ":" + base64.StdEncoding.EncodeToString([]byte("/restore:two"))
	if env[len(env)-1] != want || strings.Contains(strings.Join(env, "\n"), "untrusted") {
		t.Fatal(env)
	}
}
func TestProductionDoesNotForwardAgentDisableSwitch(t *testing.T) {
	for _, entry := range NativeEnvironment([]string{"CPH_AGENT=0"}) {
		if strings.HasPrefix(entry, "CPH_AGENT=") {
			t.Fatal("required restore boundary can be disabled")
		}
	}
}

func TestRetiredDiagnosticSwitchIsNotForwarded(t *testing.T) {
	for _, entry := range NativeEnvironment([]string{"CPH_DIAG=1"}) {
		if strings.HasPrefix(entry, "CPH_DIAG=") {
			t.Fatal("unused diagnostic switch forwarded")
		}
	}
}
