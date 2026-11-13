package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/traktuner/crashplan-headless/internal/destinations"
)

func localArchiveCommand(args []string, diagnostic io.Writer) (string, string, any, bool) {
	if len(args) == 1 && args[0] == "policy" {
		return "GET", "destinations/local/policy", nil, true
	}
	if len(args) == 2 && args[0] == "status" {
		if !validRequestID(args[1]) {
			return "", "", nil, false
		}
		return "GET", "destinations/local/operations/" + args[1], nil, true
	}
	if len(args) == 0 {
		return "", "", nil, false
	}
	in := destinations.Request{Action: args[0]}
	switch in.Action {
	case "create", "attach", "move", "relocate":
	default:
		return "", "", nil, false
	}
	flags := flag.NewFlagSet("destinations "+in.Action, flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	flags.StringVar(&in.Target.RootID, "root", "", "configured writable root ID")
	flags.StringVar(&in.Target.Relative, "relative", "", "existing destination directory relative to root")
	flags.StringVar(&in.SourceID, "source", "", "source device ID; default current device")
	flags.StringVar(&in.DestinationID, "destination", "", "local destination device ID")
	flags.StringVar(&in.RequestID, "request-id", "", "reuse a submission ID with the same payload")
	flags.StringVar(&in.Confirm, "confirm", "", "CREATE, ATTACH, MOVE or RELOCATE")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || in.Target.RootID == "" || in.Confirm != strings.ToUpper(in.Action) {
		return "", "", nil, false
	}
	var err error
	in.RequestID, err = newRequestID(in.RequestID)
	if err != nil {
		return "", "", nil, false
	}
	fmt.Fprintln(diagnostic, "LOCAL_ARCHIVE_REQUEST_ID="+in.RequestID)
	return "POST", "destinations/local", in, true
}
