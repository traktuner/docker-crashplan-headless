package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
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
	_, err = c.BackupControl(context.Background(), "pause", BackupControl{BackupSetID: "7", DestinationID: "8"})
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
	history, err := c.History(context.Background())
	if err != nil || len(history) != 1 || history[0].Message != "Scan completed" {
		t.Fatal(history, err)
	}
}
