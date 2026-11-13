package main

import (
	"encoding/hex"
	"encoding/json"
	"io"
)

func accountCommand(args []string, input io.Reader) (method, path string, body any, ok bool) {
	if len(args) == 0 {
		return "", "", nil, false
	}
	switch args[0] {
	case "setup":
		if len(args) == 1 {
			return "GET", "account/setup", nil, true
		}
	case "migration":
		if len(args) == 1 {
			return "GET", "account/migration", nil, true
		}
	case "candidates":
		if len(args) == 1 {
			return "GET", "account/migration/candidates", nil, true
		}
	case "replacement":
		if len(args) == 1 {
			return "GET", "account/replace-device", nil, true
		}
		if len(args) == 4 && (args[1] == "finish" || args[1] == "cancel") && args[2] == "--confirm" {
			return "POST", "account/replace-device/" + args[1], map[string]string{"confirm": args[3]}, true
		}
	case "migrate":
		if len(args) == 2 && args[1] == "new" {
			return "POST", "account/migration/selection", map[string]string{"selectedComputerGuid": ""}, true
		}
		if len(args) == 4 && nativeID(args[1]) && args[2] == "--confirm" && args[3] == args[1] {
			return "POST", "account/migration/selection", map[string]string{"selectedComputerGuid": args[1], "confirm": args[1]}, true
		}
	case "inherit", "replace":
		if len(args) == 4 && nativeID(args[1]) && args[2] == "--confirm" && args[3] == args[1] {
			path = "account/migration/authorized"
			if args[0] == "replace" {
				path = "account/replace-device"
			}
			return "POST", path, map[string]string{"sourceComputerGuid": args[1], "confirm": args[1]}, true
		}
	case "sso-confirm", "sso-discard":
		if len(args) >= 3 && len(args[1]) == 32 && len(args[2]) == 32 {
			_, e1 := hex.DecodeString(args[1])
			_, e2 := hex.DecodeString(args[2])
			if e1 == nil && e2 == nil {
				if args[0] == "sso-discard" && len(args) == 3 {
					return "POST", "account/sso/" + args[1] + "/discard", map[string]string{"assertionId": args[2]}, true
				}
				if args[0] == "sso-confirm" && len(args) == 5 && args[3] == "--confirm" && args[4] != "" {
					return "POST", "account/sso/" + args[1] + "/confirm", map[string]string{"assertionId": args[2], "username": args[4]}, true
				}
			}
		}
	case "sso-status", "sso-cancel":
		if len(args) == 2 && len(args[1]) == 32 {
			if _, err := hex.DecodeString(args[1]); err == nil {
				method = "GET"
				if args[0] == "sso-cancel" {
					method = "DELETE"
				}
				return method, "account/sso/" + args[1], nil, true
			}
		}
	case "login", "configuration", "sso":
		if len(args) != 1 {
			return "", "", nil, false
		}
		raw, err := io.ReadAll(io.LimitReader(input, 128<<10+1))
		if err != nil || len(raw) > 128<<10 || !json.Valid(raw) {
			return "", "", nil, false
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return "", "", nil, false
		}
		return "POST", "account/" + args[0], json.RawMessage(raw), true
	}
	return "", "", nil, false
}
