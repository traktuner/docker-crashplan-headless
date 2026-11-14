package destinations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/restore"
	"github.com/traktuner/crashplan-headless/internal/statefile"
)

func TestLocalArchiveAdmissionAndReplay(t *testing.T) {
	for _, mode := range []string{"success", "lost", "denied", "replaced", "child-symlink", "child-file", "child-directory"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			switch mode {
			case "child-symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "7")); err != nil {
					t.Fatal(err)
				}
			case "child-file":
				if err := os.WriteFile(filepath.Join(root, "7"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			case "child-directory":
				if err := os.Mkdir(filepath.Join(root, "7"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			writes := 0
			var store *statefile.Store
			native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					fmt.Fprintf(w, `{"UsersMessage":{"myUserId":1,"users":[{"userId":1,"allowLocalFolders":%t,"autoOfferSelf":true}]},"ComputersMessage":{"computers":[{"peerId":"7","userId":1,"properties":{"ME":"true","OWN":"true"}}]}}`, mode != "denied")
					if mode == "replaced" {
						os.Rename(root, root+"-old")
						os.Mkdir(root, 0700)
					}
				case "/v0/CreateFolderArchiveRequest":
					writes++
					raw, err := store.Read("local-archive-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json")
					if err != nil || !strings.Contains(string(raw), `"state":"indeterminate"`) {
						t.Error("missing durable replay barrier", err)
					}
					var in struct {
						Path string `json:"path"`
					}
					json.NewDecoder(r.Body).Decode(&in)
					if in.Path != root {
						t.Error("wrong native destination")
					}
					if mode == "lost" {
						w.WriteHeader(500)
						io.WriteString(w, `{"private":"canary"}`)
						return
					}
					io.WriteString(w, `{"guid":"8","errors":[]}`)
				default:
					t.Error("unexpected request", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer native.Close()
			c, _ := engine.NewClient(native.URL, native.Client(), time.Second)
			c.SetToken("fixture")
			roots, _ := restore.NewDestinations([]string{root})
			defer roots.Close()
			store, _ = statefile.Open(filepath.Join(t.TempDir(), "state"))
			defer store.Close()
			service, _ := NewService(c, roots, store)
			in := Request{RequestID: strings.Repeat("a", 32), Action: "create", Target: restore.Target{RootID: "0"}, Confirm: "CREATE"}
			result, err := service.Start(context.Background(), in)
			if (mode == "success" || mode == "child-directory") && (err != nil || result.State != "accepted" || result.DestinationID != "8") {
				t.Fatal(result, err)
			}
			if (mode == "denied" || mode == "replaced" || mode == "child-symlink" || mode == "child-file") && (err == nil || writes != 0) {
				t.Fatal("unsafe dispatch", result, err, writes)
			}
			if mode == "success" || mode == "lost" {
				saved, e := service.Status(in.RequestID)
				if e != nil || saved.State != result.State {
					t.Fatal(saved, e)
				}
				restart, _ := NewService(c, roots, store)
				again, e := restart.Start(context.Background(), in)
				if e != nil || writes != 1 || again.State != result.State {
					t.Fatal("replayed operation", again, e, writes)
				}
				if _, e := restart.Status(strings.Repeat("b", 32)); !errors.Is(e, engine.ErrInvalidRequest) || errors.Is(e, ErrJournal) {
					t.Fatal("unknown submission ID is not reported as an invalid request", e)
				}
				in.Target.Relative = "changed"
				if _, e := restart.Start(context.Background(), in); e == nil {
					t.Fatal("request digest changed")
				} else if !errors.Is(e, engine.ErrInvalidRequest) || errors.Is(e, ErrJournal) {
					t.Fatal("changed payload under a saved ID is not reported as an invalid request", e)
				}
				if kept, e := restart.Status(in.RequestID); e != nil || kept.State != result.State || kept.Digest != saved.Digest || writes != 1 {
					t.Fatal("refused payload changed the saved submission", kept, e, writes)
				}
				for _, damaged := range []string{
					`{"requestId":"other"}`,
					`{"requestId":"` + in.RequestID + `","requestDigest":"short","state":"accepted"}`,
					`{"requestId":"` + in.RequestID + `","requestDigest":"` + strings.Repeat("0", 64) + `","state":"unknown"}`,
				} {
					if store.Write("local-archive-"+in.RequestID+".json", []byte(damaged)) != nil {
						t.Fatal("fixture record not replaced")
					}
					if _, e := restart.Start(context.Background(), in); !errors.Is(e, ErrJournal) || errors.Is(e, engine.ErrInvalidRequest) || writes != 1 {
						t.Fatal("damaged saved submission is not a journal failure", damaged, e, writes)
					}
					if _, e := restart.Status(in.RequestID); !errors.Is(e, ErrJournal) {
						t.Fatal("damaged saved submission status is not a journal failure", damaged, e)
					}
				}
			}
		})
	}
}

func TestLocalArchiveRequiresConfirmationBeforeNativeAccess(t *testing.T) {
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unconfirmed operation reached native API") }))
	defer native.Close()
	c, _ := engine.NewClient(native.URL, native.Client(), time.Second)
	roots, _ := restore.NewDestinations([]string{t.TempDir()})
	defer roots.Close()
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	service, _ := NewService(c, roots, store)
	if _, err := service.Start(context.Background(), Request{RequestID: strings.Repeat("b", 32), Action: "create", Target: restore.Target{RootID: "0"}, Confirm: ""}); err != engine.ErrInvalidRequest {
		t.Fatal(err)
	}
}
func TestLocalArchiveMoveRejectsSourceOutsideWritableRoots(t *testing.T) {
	root := t.TempDir()
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			io.WriteString(w, `{"UsersMessage":{"myUserId":1,"users":[{"userId":1,"allowLocalFolders":true,"autoOfferSelf":true}]},"ComputersMessage":{"computers":[{"peerId":"7","userId":1,"properties":{"ME":"true","OWN":"true"}},{"peerId":"8","userId":1,"parentGuid":"7","properties":{"OWN":"true"}}]}}`)
		case "/v1/SocialNetworkConfig":
			io.WriteString(w, `{"computerConfigs":{"map":{"8":{"manifestPath":{"value":"/outside"}}}}}`)
		default:
			t.Error("unadmitted move reached native mutation", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	c, _ := engine.NewClient(native.URL, native.Client(), time.Second)
	c.SetToken("fixture")
	roots, _ := restore.NewDestinations([]string{root})
	defer roots.Close()
	store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
	defer store.Close()
	service, _ := NewService(c, roots, store)
	_, err := service.Start(context.Background(), Request{RequestID: strings.Repeat("b", 32), Action: "move", Target: restore.Target{RootID: "0"}, DestinationID: "8", Confirm: "MOVE"})
	if err != restore.ErrDestination {
		t.Fatal("outside source not refused", err)
	}
}

func TestOwnedIncomingArchiveUsesOwnDevicePolicy(t *testing.T) {
	for _, mode := range []string{"allowed", "denied", "adoption", "adoption-ended"} {
		allowed := mode == "allowed"
		adoption := mode == "adoption" || mode == "adoption-ended"
		reads := 0
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "9"), 0700); err != nil {
				t.Fatal(err)
			}
			writes := 0
			native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					fmt.Fprintf(w, `{"UsersMessage":{"myUserId":1,"users":[{"userId":1,"allowLocalFolders":false,"autoOfferSelf":%t}]},"ComputersMessage":{"computers":[{"peerId":"7","userId":1,"properties":{"ME":"true","OWN":"true"}},{"peerId":"9","userId":1,"properties":{"OWN":"true"}}]}}`, allowed)
				case "/v1/ReplaceDevice":
					reads++
					active := adoption && (mode != "adoption-ended" || reads == 1)
					fmt.Fprintf(w, `{"available":false,"inProgress":%t,"deviceSelectedGuid":"null"}`, active)
				case "/v1/LocalArchive":
					json.NewEncoder(w).Encode(map[string]any{"archiveProperties": map[string]string{"guid": "9", "location": filepath.Join(root, "9")}})
				case "/v0/AttachArchiveRequest":
					writes++
					io.WriteString(w, `{"errors":[]}`)
				default:
					t.Error(r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer native.Close()
			c, _ := engine.NewClient(native.URL, native.Client(), time.Second)
			c.SetToken("fixture")
			roots, _ := restore.NewDestinations([]string{root})
			defer roots.Close()
			store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
			defer store.Close()
			service, _ := NewService(c, roots, store)
			result, err := service.Start(context.Background(), Request{RequestID: strings.Repeat("c", 32), Action: "attach", SourceID: "9", DestinationID: "7", Target: restore.Target{RootID: "0"}, Confirm: "ATTACH"})
			if (allowed || mode == "adoption") && (err != nil || writes != 1 || result.State != "accepted") {
				t.Fatal(result, err, writes)
			}
			if !allowed && mode != "adoption" && (err == nil || writes != 0) {
				t.Fatal("owned archive policy bypass", result, err, writes)
			}
		})
	}
}

func TestSourceMissingRelocationNeverStartsAFileMove(t *testing.T) {
	for _, mode := range []string{"missing", "present", "source-symlink", "target-symlink", "target-present", "lost"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			oldPath, newPath := filepath.Join(root, "old"), filepath.Join(root, "new")
			os.Mkdir(oldPath, 0700)
			os.Mkdir(newPath, 0700)
			switch mode {
			case "present":
				os.Mkdir(filepath.Join(oldPath, "7"), 0700)
			case "source-symlink":
				os.Symlink(t.TempDir(), filepath.Join(oldPath, "7"))
			case "target-symlink":
				os.Symlink(t.TempDir(), filepath.Join(newPath, "7"))
			case "target-present":
				os.Mkdir(filepath.Join(newPath, "7"), 0700)
			}
			current := oldPath
			writes := 0
			native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					io.WriteString(w, `{"UsersMessage":{"myUserId":1,"users":[{"userId":1,"allowLocalFolders":true,"autoOfferSelf":true}]},"ComputersMessage":{"computers":[{"peerId":"7","userId":1,"properties":{"ME":"true","OWN":"true"}},{"peerId":"8","userId":1,"parentGuid":"7","properties":{"OWN":"true"}}]}}`)
				case "/v1/SocialNetworkConfig":
					json.NewEncoder(w).Encode(map[string]any{"computerConfigs": map[string]any{"map": map[string]any{"8": map[string]any{"guid": map[string]any{"value": "8", "locked": false}, "manifestPath": map[string]any{"value": current, "locked": false}, "manifestPathDefault": map[string]any{"value": false, "locked": false}, "allocatedCapacity": map[string]any{"value": "9007199254740993", "locked": true}}}}})
				case "/v0/SaveDestination":
					writes++
					var in struct {
						ID          string                     `json:"guid"`
						Destination map[string]any             `json:"destination"`
						Config      map[string]json.RawMessage `json:"config"`
					}
					if json.NewDecoder(r.Body).Decode(&in) != nil || in.ID != "8" || len(in.Destination) != 0 || !strings.Contains(string(in.Config["allocatedCapacity"]), "9007199254740993") {
						t.Error("relocation rewrote unrelated configuration")
					}
					var path struct {
						Value string `json:"value"`
					}
					json.Unmarshal(in.Config["manifestPath"], &path)
					if path.Value != newPath {
						t.Error("wrong relocation target")
					}
					current = path.Value
					if mode == "lost" {
						w.WriteHeader(500)
						io.WriteString(w, `{"private":"canary"}`)
						return
					}
					io.WriteString(w, `{"errors":[]}`)
				case "/v0/MoveJobRequest":
					t.Error("configuration-only relocation dispatched file movement")
					w.WriteHeader(500)
				default:
					t.Error(r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer native.Close()
			c, _ := engine.NewClient(native.URL, native.Client(), time.Second)
			c.SetToken("fixture")
			roots, _ := restore.NewDestinations([]string{root})
			defer roots.Close()
			store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
			defer store.Close()
			service, _ := NewService(c, roots, store)
			in := Request{RequestID: strings.Repeat("d", 32), Action: "relocate", DestinationID: "8", Target: restore.Target{RootID: "0", Relative: "new"}, Confirm: "RELOCATE"}
			result, err := service.Start(context.Background(), in)
			if mode == "missing" && (err != nil || result.State != "relocated" || !result.SourceMissing || writes != 1) {
				t.Fatal(result, err, writes)
			}
			if mode != "missing" && mode != "lost" && (err == nil || writes != 0) {
				t.Fatal("unsafe relocation", result, err, writes)
			}
			if mode == "lost" {
				if err == nil || writes != 1 || result.State != "indeterminate" {
					t.Fatal(result, err, writes)
				}
				again, e := service.Start(context.Background(), in)
				if e != nil || again.State != "indeterminate" || writes != 1 {
					t.Fatal("replayed lost relocation", again, e, writes)
				}
			}
		})
	}
}

func TestDeviceAdministrationUsesNativeReadbackAndPermanentSubmission(t *testing.T) {
	for _, mode := range []string{"rename-self", "rename-local", "remove-local", "lost", "remove-self", "outside-local", "locked-set", "remove-replaced-parent", "remove-child-gone"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			os.Mkdir(filepath.Join(root, "7"), 0700)
			writes := 0
			name := "Before"
			deleted := false
			device := "8"
			action := "rename"
			if mode == "rename-self" {
				device = "7"
			}
			if mode == "remove-local" || mode == "remove-self" || mode == "outside-local" || mode == "locked-set" || mode == "remove-replaced-parent" || mode == "remove-child-gone" {
				action = "remove"
			}
			if mode == "remove-self" {
				device = "7"
			}
			native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					json.NewEncoder(w).Encode(map[string]any{"UsersMessage": map[string]any{"myUserId": 1, "users": []any{map[string]any{"userId": 1, "allowLocalFolders": true, "autoOfferSelf": true}}}, "ComputersMessage": map[string]any{"computers": []any{map[string]any{"peerId": "7", "name": map[bool]string{true: name, false: "Self"}[device == "7"], "properties": map[string]string{"ME": "true", "OWN": "true"}}, map[string]any{"peerId": "8", "name": name, "parentGuid": "7", "deleted": deleted, "properties": map[string]string{"OWN": "true"}}}}})
				case "/v1/SocialNetworkConfig":
					path := root
					if mode == "outside-local" {
						path = "/not-declared"
					}
					json.NewEncoder(w).Encode(map[string]any{"computerConfigs": map[string]any{"map": map[string]any{device: map[string]any{"manifestPath": map[string]any{"value": path}}}}})
				case "/v1/BackupSet":
					if mode == "locked-set" {
						io.WriteString(w, `[{"id":"1","locked":true,"name":{"value":"Locked","locked":true},"backupPaths":{"pathSet":{"value":{"paths":[]},"locked":false},"userExcludes":{"value":[],"locked":false}},"destinations":{"map":{"8":{"guid":"8","locked":true}}}}]`)
					} else {
						io.WriteString(w, `[]`)
					}
				case "/v0/SaveDestination":
					writes++
					var in struct {
						ID          string `json:"guid"`
						Destination struct {
							Name    *string `json:"name"`
							Deleted bool    `json:"deleted"`
						} `json:"destination"`
						Config json.RawMessage `json:"config"`
					}
					if json.NewDecoder(r.Body).Decode(&in) != nil || in.ID != device || in.Config != nil {
						t.Error("unrelated native destination fields changed")
					}
					if in.Destination.Name != nil {
						name = *in.Destination.Name
					}
					if in.Destination.Deleted {
						deleted = true
					}
					if mode == "remove-replaced-parent" {
						os.Rename(root, root+"-old")
						os.Mkdir(root, 0700)
					}
					if mode == "remove-child-gone" {
						os.Remove(filepath.Join(root, "7"))
					}
					if mode == "lost" {
						w.WriteHeader(500)
						return
					}
					io.WriteString(w, `{"errors":[]}`)
				default:
					t.Error(r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer native.Close()
			c, _ := engine.NewClient(native.URL, native.Client(), time.Second)
			c.SetToken("fixture")
			roots, _ := restore.NewDestinations([]string{root})
			defer roots.Close()
			store, _ := statefile.Open(filepath.Join(t.TempDir(), "state"))
			defer store.Close()
			service, _ := NewService(c, roots, store)
			in := Request{RequestID: strings.Repeat("e", 32), Action: action, DeviceID: device, Confirm: strings.ToUpper(action) + " " + device}
			if action == "rename" {
				in.Name = "After"
			}
			result, err := service.Start(context.Background(), in)
			unsafe := mode == "remove-self" || mode == "outside-local" || mode == "locked-set"
			if unsafe {
				if err == nil || writes != 0 {
					t.Fatal("unsafe destination mutation", result, err, writes)
				}
				return
			}
			if mode == "lost" || mode == "remove-replaced-parent" {
				if err == nil || result.State != "indeterminate" {
					t.Fatal(result, err)
				}
			} else if err != nil || result.State != "completed" || writes != 1 {
				t.Fatal(result, err, writes)
			}
			restart, _ := NewService(c, roots, store)
			again, e := restart.Start(context.Background(), in)
			if e != nil || writes != 1 || again.State != result.State {
				t.Fatal("device operation replay", again, e, writes)
			}
		})
	}
}
