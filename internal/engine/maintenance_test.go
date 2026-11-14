package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestArchiveMaintenanceUsesOnlyNamedAuthorizedCommand(t *testing.T) {
	for _, mode := range []string{"accepted", "lost", "native-error", "wrong-command", "invalid-target", "disconnected", "no-stats", "empty-archive"} {
		t.Run(mode, func(t *testing.T) {
			commands := 0
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					connected := 1000
					if mode == "disconnected" {
						connected = 0
					}
					json.NewEncoder(w).Encode(map[string]any{"UsersMessage": map[string]any{"myUserId": 1, "users": []any{map[string]any{"userId": 1}}}, "ComputersMessage": map[string]any{"computers": []any{map[string]any{"peerId": "7", "properties": map[string]string{"ME": "true", "OWN": "true"}}, map[string]any{"peerId": "9007199254740993", "properties": map[string]string{"DCODE": map[bool]string{true: "CONNECTED", false: "DISCONNECTED"}[connected > 0]}}}}})
				case "/v0/GetBackupStats":
					var in map[string]string
					json.NewDecoder(r.Body).Decode(&in)
					if in["targetId"] != "9007199254740993" {
						t.Error("wrong stats target")
					}
					if mode == "no-stats" {
						io.WriteString(w, `{"backupStats":null}`)
						return
					}
					size := "1024"
					if mode == "empty-archive" {
						size = "0"
					}
					json.NewEncoder(w).Encode(map[string]any{"backupStats": map[string]any{"sourceId": "7", "targetId": "9007199254740993", "isBackupTarget": true, "remoteManifestSize": size}})
				case "/v0/Command":
					commands++
					if r.Method != "POST" || r.Header.Get("Authorization") != "token fixture" {
						t.Error("wrong boundary")
					}
					var in struct {
						Command string `json:"command"`
					}
					json.NewDecoder(r.Body).Decode(&in)
					if in.Command != "backup.maint true 7 9007199254740993" {
						t.Fatal(in.Command)
					}
					switch mode {
					case "lost":
						w.WriteHeader(500)
					case "native-error":
						io.WriteString(w, `{"results":[{"command":{"name":"backup.maint"},"error":"private-canary"}]}`)
					case "wrong-command":
						io.WriteString(w, `{"results":[{"command":{"name":"backup.scan"}}]}`)
					default:
						io.WriteString(w, `{"results":[{"command":{"name":"backup.maint"}}]}`)
					}
				default:
					t.Error(r.URL.Path)
					w.WriteHeader(404)
				}
			})
			c.SetToken("fixture")
			target := "9007199254740993"
			if mode == "invalid-target" {
				target = "../all"
			}
			err := c.MaintainArchive(context.Background(), "7", target)
			refused := mode == "invalid-target" || mode == "disconnected" || mode == "no-stats" || mode == "empty-archive"
			if mode == "accepted" && err != nil {
				t.Fatal(err)
			}
			if mode != "accepted" && err == nil {
				t.Fatal("invalid or uncertain maintenance accepted")
			}
			if (refused && commands != 0) || (!refused && commands != 1) {
				t.Fatal("replayed or invalid dispatch", commands)
			}
		})
	}
}

func TestReconnectChecksTargetAndSendsStockCommand(t *testing.T) {
	var commands []string
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			io.WriteString(w, `{"ComputersMessage":{"computers":[{"peerId":"7","properties":{"ME":"true"}},{"peerId":"9","target":true,"properties":{"DCODE":"DISCONNECTED"}}]}}`)
		case "/v0/Command":
			var in struct {
				Command string `json:"command"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			commands = append(commands, in.Command)
			io.WriteString(w, `{"results":[{"command":{"name":"reconnect"}}]}`)
		default:
			t.Error(r.URL.Path)
		}
	})
	c.SetToken("fixture")
	if err := c.Reconnect(context.Background(), "9"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.cachedTargets(c.SessionRevision()); ok {
		t.Fatal("target list kept after reconnect")
	}
	for _, bad := range []string{"7", "8", "09", ""} {
		if err := c.Reconnect(context.Background(), bad); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(bad, err)
		}
	}
	if len(commands) != 1 || commands[0] != "reconnect" {
		t.Fatal(commands)
	}
}
