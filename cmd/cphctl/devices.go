package main

import (
	"flag"
	"fmt"
	"github.com/traktuner/crashplan-headless/internal/destinations"
	"io"
	"strings"
)

func devicesCommand(args []string, diagnostic io.Writer) (string, string, any, bool) {
	if len(args) == 0 {
		return "GET", "devices", nil, true
	}
	if len(args) == 2 && args[0] == "status" {
		id := args[1]
		if !validRequestID(id) {
			return "", "", nil, false
		}
		return "GET", "devices/operations/" + id, nil, true
	}
	if len(args) < 2 || (args[0] != "rename" && args[0] != "remove" && args[0] != "maintenance") || !nativeID(args[1]) {
		return "", "", nil, false
	}
	in := destinations.Request{Action: args[0], DeviceID: args[1]}
	flags := flag.NewFlagSet("devices "+in.Action, flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	flags.StringVar(&in.Name, "name", "", "new device name")
	flags.StringVar(&in.RequestID, "request-id", "", "permanent submission ID")
	var confirm string
	flags.StringVar(&confirm, "confirm", "", "repeat the device ID for removal")
	if flags.Parse(args[2:]) != nil || flags.NArg() != 0 || (in.Action != "rename" && (confirm != in.DeviceID || in.Name != "")) || (in.Action == "rename" && strings.TrimSpace(in.Name) == "") {
		return "", "", nil, false
	}
	in.Confirm = strings.ToUpper(in.Action) + " " + in.DeviceID
	var err error
	in.RequestID, err = newRequestID(in.RequestID)
	if err != nil {
		return "", "", nil, false
	}
	fmt.Fprintln(diagnostic, "DESTINATION_REQUEST_ID="+in.RequestID)
	return "POST", "devices/operations", in, true
}
