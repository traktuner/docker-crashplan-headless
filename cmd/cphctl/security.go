package main

import (
	"encoding/json"
	"io"
)

// Secret-bearing commands read JSON from stdin, never command arguments.
func securityCommand(args []string, input io.Reader) (method, path string, body any, ok bool) {
	if len(args) != 1 {
		return "", "", nil, false
	}
	if args[0] == "status" {
		return "GET", "security", nil, true
	}
	switch args[0] {
	case "unlock", "backup-unlock", "password", "key-type", "custom-key":
	default:
		return "", "", nil, false
	}

	raw, ok := readPrivateJSON(input)
	if !ok {
		return "", "", nil, false
	}
	return "POST", "security/" + args[0], raw, true
}
func readPrivateJSON(input io.Reader) (json.RawMessage, bool) {
	raw, err := io.ReadAll(io.LimitReader(input, (128<<10)+1))
	var object map[string]json.RawMessage
	if err != nil || len(raw) > 128<<10 || json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, false
	}
	return json.RawMessage(raw), true
}
