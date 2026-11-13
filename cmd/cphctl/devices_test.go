package main

import (
	"github.com/traktuner/crashplan-headless/internal/destinations"
	"io"
	"strings"
	"testing"
)

func TestDeviceCommandsKeepExactIdentityAndConfirmation(t *testing.T) {
	id := strings.Repeat("f", 32)
	for _, action := range []string{"rename", "remove"} {
		args := []string{action, "9007199254740993", "--request-id", id}
		if action == "rename" {
			args = append(args, "--name", "Archive")
		} else {
			args = append(args, "--confirm", "9007199254740993")
		}
		method, path, body, ok := devicesCommand(args, io.Discard)
		r, typed := body.(destinations.Request)
		if !ok || !typed || method != "POST" || path != "devices/operations" || r.DeviceID != "9007199254740993" || r.RequestID != id || r.Confirm != strings.ToUpper(action)+" 9007199254740993" {
			t.Fatal(method, path, body, ok)
		}
	}
	for _, args := range [][]string{{"remove", "7"}, {"remove", "7", "--confirm", "8"}, {"rename", "7"}, {"rename", "../x", "--name", "Name"}, {"status", "../x"}} {
		if _, _, _, ok := devicesCommand(args, io.Discard); ok {
			t.Fatal(args)
		}
	}
}
