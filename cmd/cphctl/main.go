package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/traktuner/crashplan-headless/internal/ctl"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
func run(args []string, input io.Reader, output, diagnostic io.Writer) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(output, usage)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprint(diagnostic, usage)
		return 2
	}
	invalid := func() int {
		fmt.Fprintln(diagnostic, "INVALID_COMMAND: run cphctl help for the command syntax")
		return 2
	}
	dir := os.Getenv("CPH_RUN_DIR")
	if dir == "" {
		dir = "/run/cph"
	}
	client := ctl.New(filepath.Join(dir, "control.sock"))
	method := "GET"
	path := ""
	var body any
	waitForScan := false
	waitTimeout := 24 * time.Hour
	switch args[0] {
	case "summary", "history", "alerts", "pause", "resume":
		var ok bool
		method, path, body, ok = activityCommand(args)
		if !ok {
			return invalid()
		}
	case "security":
		if len(args) == 4 && args[1] == "derive-key" && args[2] == "--output" {
			return exportDerivedKey(client, args[3], input, output, diagnostic)
		}
		if len(args) == 4 && args[1] == "export-key" && args[2] == "--output" {
			return exportArchiveKey(client, args[3], output, diagnostic)
		}
		var ok bool
		method, path, body, ok = securityCommand(args[1:], input)
		if !ok {
			return invalid()
		}
	case "account":
		var ok bool
		method, path, body, ok = accountCommand(args[1:], input)
		if !ok {
			return invalid()
		}
	case "destinations":
		var ok bool
		method, path, body, ok = localArchiveCommand(args[1:], diagnostic)
		if !ok {
			return invalid()
		}
	case "devices":
		var ok bool
		method, path, body, ok = devicesCommand(args[1:], diagnostic)
		if !ok {
			return invalid()
		}
	case "settings":
		if len(args) < 3 {
			return invalid()
		}
		switch args[2] {
		case "backup", "network", "general", "ui", "peer":
		default:
			return invalid()
		}
		path = "settings/" + args[2]
		if args[1] == "get" && len(args) == 3 {
			break
		}
		if args[1] != "set" || len(args) != 5 || !json.Valid([]byte(args[4])) {
			return invalid()
		}
		method = "PATCH"
		body = map[string]any{"changes": map[string]json.RawMessage{args[3]: json.RawMessage(args[4])}}
	case "restore":
		var err error
		method, path, body, err = restoreCommand(args[1:], diagnostic)
		if err != nil {
			fmt.Fprintln(diagnostic, "INVALID_RESTORE_COMMAND")
			return 2
		}
	case "status":
		path = "state"
	case "health":
		path = "health"
	case "sets":
		var ok bool
		method, path, body, ok = setCommand(args[1:])
		if !ok {
			return invalid()
		}
	case "select":
		var ok bool
		method, path, body, ok = selectionCommand(args[1:], input)
		if !ok {
			return invalid()
		}
	case "scans":
		path = "scans"
	case "engine":
		if len(args) != 2 || args[1] != "restart" {
			return invalid()
		}
		method = "POST"
		path = "engine/restart"
		body = struct{}{}
	case "scan":
		if len(args) >= 2 && args[1] == "ack" {
			if len(args) != 3 || len(args[2]) != 32 {
				return invalid()
			}
			if _, err := hex.DecodeString(args[2]); err != nil {
				return invalid()
			}
			method = "POST"
			path = "scans/" + args[2] + "/acknowledge"
			body = struct{}{}
			break
		}
		if len(args) < 2 || strings.ContainsAny(args[1], "/?#%") {
			return invalid()
		}
		flags := flag.NewFlagSet("scan", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		flags.BoolVar(&waitForScan, "wait", false, "wait for native completion")
		flags.DurationVar(&waitTimeout, "timeout", 24*time.Hour, "maximum CLI wait; does not stop the engine")
		if flags.Parse(args[2:]) != nil || flags.NArg() != 0 || waitTimeout <= 0 {
			return invalid()
		}
		method = "POST"
		path = "backup-sets/" + args[1] + "/scan"
		body = struct{}{}
	case "login":
		if len(args) != 3 {
			return invalid()
		}
		password, err := io.ReadAll(io.LimitReader(input, 65537))
		if err != nil || len(password) > 65536 {
			return invalid()
		}
		method = "POST"
		path = "account/login"
		body = map[string]string{"username": args[1], "serverAddress": args[2], "password": strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r")}
	default:
		fmt.Fprintln(diagnostic, "UNKNOWN_COMMAND")
		return 2
	}
	raw, status, err := client.Request(context.Background(), method, path, body)
	if err != nil {
		fmt.Fprintln(diagnostic, err.Error())
		return 3
	}
	fmt.Fprintln(output, string(raw))
	if status < 200 || status >= 300 {
		return responseExit(raw)
	}
	if (args[0] == "settings" || args[0] == "sets" || args[0] == "select") && (method == "PATCH" || method == "PUT") {
		var result struct {
			Data struct {
				Complete bool `json:"complete"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &result) != nil || !result.Data.Complete {
			return 5
		}
	}
	if args[0] == "scan" && waitForScan {
		var op struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &op) != nil || len(op.Data.ID) != 32 {
			return 1
		}
		waitCtx, cancel := context.WithTimeout(context.Background(), waitTimeout)
		defer cancel()
		timer := time.NewTicker(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-waitCtx.Done():
				fmt.Fprintln(diagnostic, "SCAN_WAIT_TIMEOUT: the engine was not stopped")
				return 124
			case <-timer.C:
			}
			current, status, err := client.Request(waitCtx, "GET", "scans/"+op.Data.ID, nil)
			if err != nil {
				fmt.Fprintln(diagnostic, err.Error())
				return 1
			}
			if status != 200 {
				fmt.Fprintln(output, string(current))
				return 1
			}
			var state struct {
				Data struct {
					State string `json:"state"`
				} `json:"data"`
			}
			if json.Unmarshal(current, &state) != nil {
				return 1
			}
			switch state.Data.State {
			case "completed":
				fmt.Fprintln(output, string(current))
				return 0
			case "indeterminate", "interrupted", "stalled", "failed":
				fmt.Fprintln(output, string(current))
				return 1
			}
		}
	}
	return 0
}

func responseExit(raw []byte) int {
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return 3
	}
	switch result.Error.Code {
	case "WEB_AUTHORIZATION_REQUIRED", "NATIVE_AUTHORIZATION_REQUIRED", "NATIVE_LOGIN_REJECTED":
		return 4
	case "NATIVE_OPERATION_INDETERMINATE":
		return 5
	default:
		return 6
	}
}
