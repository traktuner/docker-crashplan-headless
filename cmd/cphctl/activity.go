package main

import (
	"flag"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"io"
)

func activityCommand(args []string) (method, path string, body any, ok bool) {
	if len(args) == 0 {
		return "", "", nil, false
	}
	switch args[0] {
	case "summary", "history":
		if len(args) == 1 {
			path = args[0]
			if path == "summary" {
				path = "backup/summary"
			}
			return "GET", path, nil, true
		}
	case "alerts":
		if len(args) == 1 {
			return "GET", "alerts", nil, true
		}
		if len(args) == 3 && args[1] == "dismiss" {
			return "POST", "alerts/dismiss", map[string]string{"id": args[2]}, true
		}
	case "pause", "resume":
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		var in engine.BackupControl
		flags.StringVar(&in.BackupSetID, "set", "", "backup set ID")
		flags.StringVar(&in.PlanID, "plan", "", "plan ID")
		flags.StringVar(&in.DestinationID, "destination", "", "destination ID")
		if args[0] == "pause" {
			flags.StringVar(&in.DurationMinutes, "minutes", "60", "pause duration in minutes")
		}
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
			return "", "", nil, false
		}
		count := 0
		for _, id := range []string{in.BackupSetID, in.PlanID, in.DestinationID} {
			if id != "" {
				if !nativeID(id) {
					return "", "", nil, false
				}
				count++
			}
		}
		if count > 1 || (in.DurationMinutes != "" && !nativeID(in.DurationMinutes)) {
			return "", "", nil, false
		}
		return "POST", "backup/" + args[0], in, true
	}
	return "", "", nil, false
}
