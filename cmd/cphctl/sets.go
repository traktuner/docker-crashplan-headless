package main

import (
	"encoding/json"
	"flag"
	"io"
	"strconv"

	"github.com/traktuner/crashplan-headless/internal/engine"
)

func nativeID(id string) bool {
	n, e := strconv.ParseInt(id, 10, 64)
	return e == nil && n > 0 && strconv.FormatInt(n, 10) == id
}
func setCommand(args []string) (method, path string, body any, ok bool) {
	if len(args) == 0 {
		return "GET", "backup-sets", nil, true
	}
	if len(args) == 1 && args[0] == "list" {
		return "GET", "backup-sets/details", nil, true
	}
	if len(args) == 2 && args[0] == "create" {
		return "POST", "backup-sets", map[string]string{"name": args[1]}, true
	}
	if len(args) < 2 || !nativeID(args[1]) {
		return "", "", nil, false
	}
	path = "backup-sets/" + args[1]
	switch args[0] {
	case "show":
		if len(args) == 2 {
			return "GET", path, nil, true
		}
	case "edit":
		if len(args) == 4 && json.Valid([]byte(args[3])) {
			return "PATCH", path, map[string]any{"changes": map[string]json.RawMessage{args[2]: json.RawMessage(args[3])}}, true
		}
	case "delete":
		if len(args) == 4 && args[2] == "--confirm" && args[3] == args[1] {
			return "DELETE", path, map[string]string{"confirm": args[1]}, true
		}
	case "destination":
		if len(args) == 4 && nativeID(args[2]) && (args[3] == "attach" || args[3] == "detach") {
			return "PUT", path + "/destinations/" + args[2], map[string]bool{"attached": args[3] == "attach"}, true
		}
	}
	return "", "", nil, false
}
func selectionCommand(args []string, input io.Reader) (method, path string, body any, ok bool) {
	if len(args) < 2 || !nativeID(args[1]) {
		return "", "", nil, false
	}
	base := "backup-sets/" + args[1]
	flags := flag.NewFlagSet("select", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	switch args[0] {
	case "browse":
		var q engine.SourceQuery
		flags.StringVar(&q.Parent, "parent", "", "parent directory; omit for roots")
		flags.IntVar(&q.Offset, "offset", 0, "page offset")
		flags.IntVar(&q.Limit, "limit", 200, "page size")
		if flags.Parse(args[2:]) != nil || flags.NArg() != 0 {
			return "", "", nil, false
		}
		return "POST", base + "/selection/browse", q, true
	case "set":
		if len(args) < 3 {
			return "", "", nil, false
		}
		p := engine.SelectionPath{Path: args[2], Selected: true}
		exclude := false
		flags.BoolVar(&p.Directory, "directory", false, "select a directory")
		flags.BoolVar(&exclude, "exclude", false, "exclude this path")
		if flags.Parse(args[3:]) != nil || flags.NArg() != 0 {
			return "", "", nil, false
		}
		p.Selected = !exclude
		return "PATCH", base + "/selection", map[string]any{"changes": []engine.SelectionPath{p}}, true
	case "excludes":
		if len(args) != 2 {
			return "", "", nil, false
		}
		raw, err := io.ReadAll(io.LimitReader(input, 1<<20+1))
		if err != nil || len(raw) > 1<<20 {
			return "", "", nil, false
		}
		var patterns []engine.Pattern
		if json.Unmarshal(raw, &patterns) != nil || patterns == nil {
			return "", "", nil, false
		}
		return "PUT", base + "/excludes", map[string]any{"patterns": patterns}, true
	}
	return "", "", nil, false
}
