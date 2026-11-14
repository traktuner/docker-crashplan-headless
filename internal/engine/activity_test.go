package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPauseChecksNativeErrorEvenWithHTTP200(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"error":"INVALID_BACKUP_SET","errorMessage":"private-canary"}`)
	})
	c.SetToken("fixture")
	if _, err := c.BackupControl(context.Background(), "pause", BackupControl{BackupSetID: "7", DurationMinutes: "60"}); !errors.Is(err, ErrRejected) {
		t.Fatal("native pause error ignored", err)
	}
}
func TestPausePreservesScopeAndHasNoAutomaticReplay(t *testing.T) {
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if r.Method != "POST" || r.URL.Path != "/v0/PauseBackupRequest" || in["backupSetId"] != "9007199254740993" || in["durationMinutes"] != "60" {
			t.Error("pause scope changed")
		}
		io.WriteString(w, `{"backupSetIds":["9007199254740993"]}`)
	})
	c.SetToken("fixture")
	result, err := c.BackupControl(context.Background(), "pause", BackupControl{BackupSetID: "9007199254740993", DurationMinutes: "60"})
	if err != nil || len(result) != 1 || result[0] != "9007199254740993" || calls != 1 {
		t.Fatal(result, err, calls)
	}
	_, err = c.BackupControl(context.Background(), "pause", BackupControl{BackupSetID: "7", PlanID: "8"})
	if err == nil || calls != 1 {
		t.Fatal("ambiguous scope dispatched")
	}
}
func TestAlertsAndHistoryProjectOnlyPublicFields(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/AlertRequest":
			io.WriteString(w, `{"alerts":[{"name":"CustomAlert","guid":"custom|7","flags":1,"expiration":"100","title":"Notice","message":"Backup notice","dataKey":"private-canary"}],"newAlert":true}`)
		case "/v0/GetHistoryLog":
			io.WriteString(w, `{"messages":[{"level":"I","timestamp":"100","message":"Scan completed"}],"historyBytes":"private-canary"}`)
		}
	})
	c.SetToken("fixture")
	alerts, err := c.Alerts(context.Background())
	if err != nil || len(alerts) != 1 || alerts[0].ID != "custom|7" {
		t.Fatal(alerts, err)
	}
	history, err := c.History(context.Background(), 0)
	if err != nil || len(history) != 1 || history[0].Message != "Scan completed" {
		t.Fatal(history, err)
	}
}

func TestBackupControlPairScopes(t *testing.T) {
	var got []map[string]string
	var paths []string
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		got = append(got, in)
		paths = append(paths, r.URL.Path)
		io.WriteString(w, `{"backupSetIds":["9007199254740993"]}`)
	})
	c.SetToken("fixture")
	pair := BackupControl{BackupSetID: "9007199254740993", DestinationID: "9007199254740995"}
	for _, action := range []string{"run", "resume"} {
		if _, err := c.BackupControl(context.Background(), action, pair); err != nil {
			t.Fatal(action, err)
		}
	}
	// The engine has no set + destination pause; the destination is paused.
	if _, err := c.BackupControl(context.Background(), "pause", pair); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || paths[0] != "/v0/ResumeBackupRequest" || paths[1] != "/v0/ResumeBackupRequest" || paths[2] != "/v0/PauseBackupRequest" {
		t.Fatal(paths)
	}
	for i := 0; i < 2; i++ {
		if got[i]["backupSetId"] != "9007199254740993" || got[i]["destinationGuid"] != "9007199254740995" || len(got[i]) != 2 {
			t.Fatalf("resume pair changed: %v", got[i])
		}
	}
	if got[2]["destinationGuid"] != "9007199254740995" || len(got[2]) != 1 {
		t.Fatalf("pause pair: %v", got[2])
	}
	// "Back up now" without a scope resumes everything, like the stock call.
	if _, err := c.BackupControl(context.Background(), "run", BackupControl{}); err != nil || len(got[3]) != 0 {
		t.Fatal(got, err)
	}
	for _, bad := range []BackupControl{
		{BackupSetID: "7", PlanID: "8", DestinationID: "9"},
		{PlanID: "8", DestinationID: "9"},
		{BackupSetID: "07"},
	} {
		if _, err := c.BackupControl(context.Background(), "run", bad); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(bad, err)
		}
	}
	if _, err := c.BackupControl(context.Background(), "run", BackupControl{DurationMinutes: "60"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("run with duration", err)
	}
	if len(got) != 4 {
		t.Fatal("rejected requests were dispatched", len(got))
	}
}

func TestAlertsCarryStockSemantics(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/AlertRequest":
			io.WriteString(w, `{"alerts":[
				{"name":"HelpIntroAlert","flags":0,"expiration":"9223372036854775807"},
				{"name":"EnginePausedAlert","flags":0,"expiration":"100"},
				{"name":"CustomAlert","guid":"com.backup42.common.alert.CustomAlert|7","title":"Org notice","message":"Hello","flags":1,"expiration":"100"},
				{"name":"PatchFailedAlert","flags":0,"expiration":"100"},
				{"name":"BackupSevereAlert","flags":2,"expiration":"100"}]}`)
		case "/v0/GetComputers":
			io.WriteString(w, `{"ComputersMessage":{"computers":[{"peerId":"7","properties":{"ME":"true"}},{"peerId":"9","name":"Cloud","target":true,"properties":{"CPC":"true","DCODE":"OLD_VERSION"}}]}}`)
		default:
			t.Error(r.URL.Path)
		}
	})
	c.SetToken("fixture")
	alerts, err := c.Alerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range alerts {
		names = append(names, a.Name)
	}
	want := "UpgradeRequiredErrorAlert,PatchFailedAlert,CustomAlert,EnginePausedAlert,BackupSevereAlert"
	if strings.Join(names, ",") != want {
		t.Fatalf("order/omission: %v", names)
	}
	up, patch, custom, paused, other := alerts[0], alerts[1], alerts[2], alerts[3], alerts[4]
	if up.Closable || up.Priority != 1 || up.Title != "Upgrade required:" || up.Message != "You have an older version of CrashPlan." || up.ID != "UpgradeRequiredErrorAlert" {
		t.Fatalf("local version alert %+v", up)
	}
	if patch.Action != "retry" || !patch.Closable || patch.Priority != 4 || patch.Title != "" || !strings.HasPrefix(patch.Message, "CrashPlan wasn't able to upgrade") {
		t.Fatalf("patch %+v", patch)
	}
	if custom.Title != "Org notice" || custom.Message != "Hello" || custom.Priority != 7 || !custom.Closable || custom.Action != "" {
		t.Fatalf("custom %+v", custom)
	}
	if paused.Action != "resume" || paused.Closable || paused.Priority != 8 || paused.Title != "Paused:" {
		t.Fatalf("paused %+v", paused)
	}
	if other.Priority != 9 || !other.Closable || other.Title != "" || other.Action != "" {
		t.Fatalf("unknown alert %+v", other)
	}
}

func TestVersionAlertSuppressedDuringUpgrade(t *testing.T) {
	devices := []Device{{ID: "9", Cloud: true, DisconnectCode: "NEW_VERSION"}}
	if a, ok := localVersionAlert(devices, nil); !ok || a.Name != "VersionMismatchErrorAlert" || a.Closable {
		t.Fatal(a, ok)
	}
	if _, ok := localVersionAlert(devices, []Alert{{Name: "DownloadingPatchAlert"}}); ok {
		t.Fatal("version alert while downloading a patch")
	}
	if _, ok := localVersionAlert([]Device{{ID: "9", Cloud: true, Connected: true}}, nil); ok {
		t.Fatal("version alert while connected")
	}
	if _, ok := localVersionAlert([]Device{{ID: "9", Cloud: true, DisconnectCode: "NOT_AUTHORIZED"}}, nil); ok {
		t.Fatal("NOT_AUTHORIZED is not a version alert")
	}
}

func TestDismissRefusesPermanentAlerts(t *testing.T) {
	removed := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/AlertRequest":
			io.WriteString(w, `{"alerts":[{"name":"EnginePausedAlert","flags":0,"expiration":"100"}]}`)
		case "/v0/AlertRemove":
			removed++
		}
	})
	c.SetToken("fixture")
	if err := c.DismissAlert(context.Background(), "EnginePausedAlert"); !errors.Is(err, ErrInvalidRequest) || removed != 0 {
		t.Fatal(err, removed)
	}
	if err := c.DismissAlert(context.Background(), "UpgradeRequiredErrorAlert"); !errors.Is(err, ErrInvalidRequest) || removed != 0 {
		t.Fatal("local alert dismissed", err)
	}
}

func TestHistoryLimitAndCache(t *testing.T) {
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"messages":[{"level":"I","timestamp":"1","message":"a"},{"level":"I","timestamp":"2","message":"b"},{"level":"W","timestamp":"3","message":"c"}]}`)
	})
	c.SetToken("fixture")
	last, err := c.History(context.Background(), 2)
	if err != nil || len(last) != 2 || last[0].Message != "b" || last[1].Message != "c" {
		t.Fatal(last, err)
	}
	last[0].Message = "mutated"
	all, err := c.History(context.Background(), 0)
	if err != nil || len(all) != 3 || all[1].Message != "b" || calls != 1 {
		t.Fatal("cache not shared or mutated", all, err, calls)
	}
	if more, _ := c.History(context.Background(), 10); len(more) != 3 {
		t.Fatal(more)
	}
	// A new session revision never sees the previous session's history.
	c.SetToken("other")
	if _, err := c.History(context.Background(), 1); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	// An expired reading is read again.
	c.caches().historyAt = time.Now().Add(-historyFresh)
	if _, err := c.History(context.Background(), 1); err != nil || calls != 3 {
		t.Fatal(err, calls)
	}
	if _, err := c.History(context.Background(), HistoryLimitMax+1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestHistoryErrorIsNotCached(t *testing.T) {
	fail := true
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(500)
			return
		}
		io.WriteString(w, `{"messages":[]}`)
	})
	c.SetToken("fixture")
	if _, err := c.History(context.Background(), 0); err == nil {
		t.Fatal("want error")
	}
	fail = false
	if out, err := c.History(context.Background(), 0); err != nil || len(out) != 0 {
		t.Fatal(out, err)
	}
}
