package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLocalArchiveOperationsKeepPropertiesPrivateAndUseNativeMethods(t *testing.T) {
	for _, action := range []string{"attach", "move"} {
		t.Run(action, func(t *testing.T) {
			writes := 0
			validated := false
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/LocalArchive" {
					if r.URL.Query().Get("deviceGuid") != "7" || r.URL.Query().Get("path") != "/restore" {
						t.Error("native archive query mismatch")
					}
					io.WriteString(w, `{"archiveProperties":{"guid":"7","location":"/restore/7","privateKey":"private-canary"}}`)
					return
				}
				writes++
				if !validated || r.Method != "POST" {
					t.Error("mutation without final admission")
				}
				var body map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Fatal("invalid payload")
				}
				switch action {
				case "attach":
					if r.URL.Path != "/v0/AttachArchiveRequest" || body["archiveProperties"] == nil || string(body["childGuid"]) != `"8"` {
						t.Error("wrong attach body")
					}
					io.WriteString(w, `{"childGuid":"8","errors":[]}`)
				case "move":
					if r.URL.Path != "/v0/MoveJobRequest" || body["job"] == nil || string(body["newArchiveLocation"]) != `"/restore"` {
						t.Error("wrong move body")
					}
					io.WriteString(w, `{"job":{"id":"99","done":false,"canceled":false},"sourceMissing":false,"errors":[]}`)
				}
			})
			c.SetToken("fixture")
			result, err := c.ExecuteLocalArchive(context.Background(), action, "/restore", "7", "8", func(path string) error {
				if path != "/restore" && path != "/restore/7" {
					t.Fatal(path)
				}
				validated = true
				return nil
			})
			raw, _ := json.Marshal(result)
			if err != nil || writes != 1 || result.State != "accepted" || strings.Contains(string(raw), "canary") {
				t.Fatal(result, err, writes)
			}
		})
	}
}
func TestLocalArchiveRejectsMismatchedArchiveAndFailedFinalAdmission(t *testing.T) {
	for _, mode := range []string{"source", "path", "admission"} {
		t.Run(mode, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unsafe archive dispatched")
				}
				guid, path := "7", "/restore/7"
				if mode == "source" {
					guid = "9"
				}
				if mode == "path" {
					path = "/outside/7"
				}
				json.NewEncoder(w).Encode(map[string]any{"archiveProperties": map[string]string{"guid": guid, "location": path}})
			})
			c.SetToken("fixture")
			_, err := c.ExecuteLocalArchive(context.Background(), "attach", "/restore", "7", "8", func(string) error {
				if mode == "admission" {
					return ErrRestorePath
				}
				return nil
			})
			if err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestLocalArchivePathUsesNativeFallbackOrder(t *testing.T) {
	for _, mode := range []string{"computer", "user", "default"} {
		t.Run(mode, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/SocialNetworkConfig" {
					switch mode {
					case "computer":
						io.WriteString(w, `{"computerConfigs":{"map":{"8":{"manifestPath":{"value":"/computer"}}}},"userConfigs":{"map":{"1":{"manifestPath":{"value":"/user"}}}}}`)
					case "user":
						io.WriteString(w, `{"userConfigs":{"map":{"1":{"manifestPath":{"value":"/user"}}}}}`)
					default:
						io.WriteString(w, `{}`)
					}
					return
				}
				if r.URL.Path != "/v1/BackupConfig" {
					t.Error("wrong default native configuration")
				}
				io.WriteString(w, `{"manifestPath":{"value":"/default"}}`)
			})
			c.SetToken("fixture")
			path, err := c.LocalArchivePath(context.Background(), LocalArchiveDevice{ID: "8", UserID: "1"})
			if err != nil || path != "/"+mode {
				t.Fatal(path, err)
			}
		})
	}
}
func TestLocalArchiveCreateRechecksBeforeMutation(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("final admission failure dispatched native mutation")
	})
	c.SetToken("fixture")
	if _, err := c.ExecuteLocalArchive(context.Background(), "create", "/restore", "7", "", func(string) error { return ErrRestorePath }); err != ErrRestorePath {
		t.Fatal(err)
	}
}
func TestLocalArchiveOptionalPolicyMatchesNativeFalseDefault(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"UsersMessage":{"myUserId":1,"users":[{"userId":1,"allowLocalFolders":true}]},"ComputersMessage":{"computers":[{"peerId":"7","userId":1,"properties":{"ME":"true","OWN":"true"}}]}}`)
	})
	c.SetToken("fixture")
	policy, err := c.LocalArchivePolicy(context.Background())
	if err != nil || !policy.Allowed || policy.OwnDevices {
		t.Fatal(policy, err)
	}
}
