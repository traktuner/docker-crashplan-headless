package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNativePassphraseDerivationKeepsUserContextAndKeysPrivate(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{43}, 56))
	for _, mode := range []string{"export", "unlock", "foreign", "changed-session", "empty", "blank"} {
		t.Run(mode, func(t *testing.T) {
			derivations := 0
			var c *Client
			c = fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					io.WriteString(w, `{"UsersMessage":{"myUserId":1,"users":[{"userId":1}]},"ComputersMessage":{"computers":[{"peerId":"7","userId":1,"properties":{"ME":"true","OWN":"true"}},{"peerId":"8","userId":2,"properties":{"OWN":"false"}}]}}`)
				case "/v1/DataKey":
					derivations++
					var in map[string]string
					json.NewDecoder(r.Body).Decode(&in)
					if r.Method != "POST" || in["passphrase"] != "phrase-canary" || in["userid"] != "1" || len(in) != 2 {
						t.Error("native derivation context changed")
					}
					if mode == "changed-session" {
						c.SetToken("replacement")
					}
					json.NewEncoder(w).Encode(map[string]string{"dataKey": key})
				case "/v1/CustomDataKey":
					io.WriteString(w, `{"valid":true}`)
				default:
					t.Error(r.URL.Path)
					w.WriteHeader(404)
				}
			})
			c.SetToken("fixture")
			in := DerivedKeyInput{SourceID: "7", Passphrase: "phrase-canary"}
			if mode == "foreign" {
				in.SourceID = "8"
			}
			if mode == "empty" {
				in.Passphrase = ""
			}
			if mode == "blank" {
				in.Passphrase = " \t\n\u2000"
			}
			if mode == "unlock" {
				if err := c.UnlockArchive(context.Background(), ArchiveUnlock{SourceID: "7", Type: "CustomKey", Passphrase: in.Passphrase}); err != nil || c.archiveKey("7") != key {
					t.Fatal(err)
				}
				return
			}
			exported, err := c.ExportDerivedKey(context.Background(), in)
			if mode == "export" {
				var out bytes.Buffer
				if err != nil {
					t.Fatal(err)
				}
				exported.WriteTo(&out)
				if out.String() != key+"\n" {
					t.Fatal("wrong export")
				}
				raw, _ := json.Marshal(exported)
				if strings.Contains(string(raw), key) {
					t.Fatal("key leaked into JSON")
				}
			}
			if mode != "export" && err == nil {
				t.Fatal("invalid or stale derivation exported")
			}
			if (mode == "foreign" || mode == "empty" || mode == "blank") && derivations != 0 {
				t.Fatal("unsafe derivation dispatched")
			}
		})
	}
}
func TestReplaceCustomKeyIsSeparateConfirmedAndVerifiesActualKey(t *testing.T) {
	old := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{41}, 56))
	next := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 56))
	for _, mode := range []string{"confirmed", "unconfirmed", "unchanged", "lost", "stale-readback", "not-custom"} {
		t.Run(mode, func(t *testing.T) {
			key := old
			writes := 0
			c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/DataKey":
					kind := "CustomKey"
					if mode == "not-custom" {
						kind = "AccountPassword"
					}
					json.NewEncoder(w).Encode(map[string]string{"securityKeyType": kind, "dataKey": key})
				case "/v0/SecurityKeyTypeRequest":
					writes++
					var in struct {
						Type string `json:"type"`
						Key  struct {
							Array string `json:"array"`
						} `json:"privateKey"`
					}
					json.NewDecoder(r.Body).Decode(&in)
					if in.Type != "CustomKey" || in.Key.Array != next {
						t.Error("wrong native replacement")
					}
					if mode != "stale-readback" {
						key = next
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
			})
			c.SetToken("fixture")
			c.archiveKeys = map[string]string{"7": old}
			in := CustomKeyChange{Key: next, Confirm: "REPLACE CUSTOM KEY"}
			if mode == "unconfirmed" {
				in.Confirm = ""
			}
			if mode == "unchanged" {
				in.Key = old
			}
			result, err := c.ReplaceCustomKey(context.Background(), in)
			if mode == "confirmed" && (err != nil || result.Type != "CustomKey" || writes != 1) {
				t.Fatal(result, err, writes)
			}
			if mode != "confirmed" && err == nil {
				t.Fatal("replacement falsely accepted")
			}
			if (mode == "lost" || mode == "stale-readback") && !errors.Is(err, ErrIndeterminate) {
				t.Fatal("uncertain key change lost", err)
			}
			if writes > 0 && c.archiveKey("7") != "" {
				t.Fatal("old cached key survived possible replacement")
			}
			if (mode == "unconfirmed" || mode == "unchanged" || mode == "not-custom") && writes != 0 {
				t.Fatal("unsafe key change")
			}
		})
	}
}
