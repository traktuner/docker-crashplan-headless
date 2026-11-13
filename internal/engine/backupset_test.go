package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const setFixture = `{"id":"9007199254740993","locked":false,"name":{"value":"Main","locked":false},"scanInterval":{"value":"86400000","locked":false},"scanTime":{"value":"03:00","locked":false},"backupPaths":{"pathSet":{"value":{"paths":[{"path":"/storage/one","directory":true,"selected":true},{"path":"/storage/two","directory":true,"selected":true}]},"locked":false},"userExcludes":{"value":[],"locked":false},"systemExcludes":{"value":[],"locked":true},"hostSystemExcludes":{"value":[],"locked":true}},"destinations":{"map":{"8":{"guid":"8","locked":false}}}}`

func TestSelectionUpdatePreservesOtherPathsAndReadsBack(t *testing.T) {
	current := setFixture
	writes := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/BackupSet/9007199254740993" {
			t.Error("wrong set path")
		}
		if r.Method == "PUT" {
			writes++
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), "/storage/two") || strings.Contains(string(raw), "scanInterval") {
				t.Error("selection overwrote unrelated state", string(raw))
			}
			current = strings.Replace(current, `"path":"/storage/one","directory":true,"selected":true`, `"path":"/storage/one","directory":true,"selected":false`, 1)
			return
		}
		io.WriteString(w, current)
	})
	c.SetToken("fixture")
	result, err := c.ChangeSelection(context.Background(), "9007199254740993", []SelectionPath{{Path: "/storage/one", Directory: true, Selected: false}})
	if err != nil || !result.Complete || len(result.Set.Paths) != 2 || writes != 1 {
		t.Fatal(result, err, writes)
	}
}

func TestLockedSelectionAndDefaultSetDeletionNeverWrite(t *testing.T) {
	writes := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
		}
		io.WriteString(w, strings.Replace(setFixture, `"locked":false`, `"locked":true`, 1))
	})
	c.SetToken("fixture")
	if _, err := c.ChangeSelection(context.Background(), "9007199254740993", []SelectionPath{{Path: "/storage/one", Directory: true, Selected: false}}); !errors.Is(err, ErrSettingLocked) {
		t.Fatal(err)
	}
	if err := c.DeleteBackupSet(context.Background(), "1"); !errors.Is(err, ErrInvalidRequest) || writes != 0 {
		t.Fatal(err, writes)
	}
}

func TestBackupSetScanSchedulePatchKeepsOtherSettings(t *testing.T) {
	current := setFixture
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != `{"scanTime":{"value":"04:00"}}` {
				t.Error("overbroad set update", string(raw))
			}
			current = strings.Replace(current, `03:00`, `04:00`, 1)
			return
		}
		io.WriteString(w, current)
	})
	c.SetToken("fixture")
	result, err := c.PatchBackupSet(context.Background(), "9007199254740993", map[string]json.RawMessage{"scanTime": json.RawMessage(`"04:00"`)})
	if err != nil || !result.Complete {
		t.Fatal(result, err)
	}
}

func TestBackupSetDoesNotRequireExcludedDeprecatedFields(t *testing.T) {
	body := strings.ReplaceAll(setFixture, `,"systemExcludes":{"value":[],"locked":true},"hostSystemExcludes":{"value":[],"locked":true}`, "")
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
	c.SetToken("fixture")
	if _, err := c.BackupSet(context.Background(), "9007199254740993"); err != nil {
		t.Fatal("deprecated fields were required", err)
	}
}

func TestSourceBrowseUsesGlobalExclusionsAndChecksParentEcho(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/BackupSet/9007199254740993":
			io.WriteString(w, setFixture)
		case "/v1/BackupConfig":
			io.WriteString(w, `{"systemExcludes":{"value":[{"pattern":"system","regularExpression":false}]},"hostSystemExcludes":{"value":[]}}`)
		case "/v0/GetChildrenPaths":
			var in struct {
				Rules map[string]json.RawMessage `json:"rules"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || !strings.Contains(string(in.Rules["systemExcludes"]), "system") {
				t.Error("global source exclusions omitted")
			}
			io.WriteString(w, `{"parentPath":{"path":"/storage"},"children":[{"path":"/storage/one","directory":true,"exists":true,"selectionCode":"SELECTED"}],"totalCount":1}`)
		default:
			t.Error("unexpected source route", r.URL.Path)
		}
	})
	c.SetToken("fixture")
	page, err := c.BrowseSource(context.Background(), "9007199254740993", SourceQuery{Parent: "/storage", Limit: 200})
	if err != nil || len(page.Items) != 1 || page.Items[0].Selection != "SELECTED" {
		t.Fatal(page, err)
	}
}

func TestNativeDirectorySelectionAcceptsTrailingSlash(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Replace(setFixture, `/storage/one`, `/storage/one/`, 1))
	})
	c.SetToken("fixture")
	set, err := c.BackupSet(context.Background(), "9007199254740993")
	if err != nil || set.Paths[0].Path != "/storage/one" {
		t.Fatal("native directory path rejected", set, err)
	}
}

func TestRetentionIsProjectedAsReadOnlyNativePolicy(t *testing.T) {
	body := strings.Replace(setFixture, `"destinations":`, `"retentionPolicy":{"backupFrequency":{"value":"900000","locked":false},"privateKey":"never-forward"},"destinations":`, 1)
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
	c.SetToken("fixture")
	set, err := c.BackupSet(context.Background(), "9007199254740993")
	if err != nil || len(set.Retention) == 0 || string(set.Retention[0].Value) != `"900000"` || !set.Retention[0].Locked {
		t.Fatal("retention shape or write policy lost", set.Retention, err)
	}
}

func TestNativeNeverRetentionPreservesBackupSetDetails(t *testing.T) {
	for _, field := range []string{"versionLastNinetyDaysInterval", "versionLastYearInterval", "versionPrevYearsInterval"} {
		t.Run(field, func(t *testing.T) {
			body := strings.Replace(setFixture, `"destinations":`, `"retentionPolicy":{"`+field+`":{"value":-1,"locked":false}},"destinations":`, 1)
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "["+body+"]") })
			c.SetToken("fixture")
			sets, err := c.BackupSets(context.Background())
			if err != nil || len(sets) != 1 {
				t.Fatal("native Never policy blocks set list", err)
			}
			found := false
			for _, setting := range sets[0].Retention {
				if setting.Name == field {
					found = true
					if string(setting.Value) != "-1" || !setting.Available || !setting.Locked {
						t.Fatal("native sentinel or read-only policy lost", setting)
					}
				}
			}
			if !found {
				t.Fatal("retention field omitted")
			}
		})
	}
}

func TestRetentionSentinelDoesNotRelaxOtherIntegerDomains(t *testing.T) {
	for _, kind := range []string{"integer", "long", "retention-interval"} {
		raw := json.RawMessage(`-1`)
		if kind == "retention-interval" {
			raw = json.RawMessage(`-2`)
		}
		if _, err := normalizeSetting(SettingSpec{Kind: kind}, raw); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("unsupported negative value accepted", kind, err)
		}
	}
}

func TestSelectionRejectsNonCanonicalNativePathInsteadOfRewritingIt(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Replace(setFixture, `/storage/one`, `/storage/../etc`, 1))
	})
	c.SetToken("fixture")
	if _, err := c.BackupSet(context.Background(), "9007199254740993"); !errors.Is(err, ErrProtocol) {
		t.Fatal("malformed native path was silently rewritten", err)
	}
}

func TestBackupCompressionAndDedupUseNativeEnums(t *testing.T) {
	for _, name := range []string{"compression", "dedup"} {
		allowed := []string{"ON", "OFF", "AUTOMATIC"}
		if name == "dedup" {
			allowed = []string{"FULL", "MINIMAL", "AUTOMATIC"}
		}
		for _, v := range allowed {
			raw, _ := json.Marshal(v)
			if _, err := normalizeSetting(SettingSpec{Kind: name}, raw); err != nil {
				t.Fatal(name, v, err)
			}
		}
		if _, err := normalizeSetting(SettingSpec{Kind: name}, json.RawMessage(`"invented"`)); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
}
