package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestDestinationProgressReadsSynchronization(t *testing.T) {
	raw := []byte(`{"backupStats":{"targetId":552,"validating":true,"backingUp":false,
		"syncProgressStats":{"current":"250","total":"1000","files":false},
		"sessionCompletedRateInBytesPerSec":1234.7,
		"fileTodoStats":{"1":{"scanning":false,"selectedForBackup":true,"numFilesScanned":10,"numBytesScanned":"100",
			"numRemainingFilesToBackup":4,"numRemainingSourceBytes":"40","lastBackupTimestamp":-1,"nextBackupTimestamp":1700000000000,"resumeTime":0,
			"sendingFileTodo":{"fileTodoIndex":{"backupFile":{"sourcePath":"/storage/a.txt"}}}}}}}`)
	p, err := parseDestinationProgress(raw, "552")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Validating || p.Sync == nil || p.Sync.Percent != 25 || p.Sync.Files || p.RateBytes != "1234" {
		t.Fatalf("unexpected %+v %+v", p, p.Sync)
	}
	if len(p.Sets) != 1 || p.Sets[0].RemainingFiles != "4" || p.Sets[0].LastBackup != "-1" || p.Sets[0].SendingPath != "/storage/a.txt" {
		t.Fatalf("unexpected sets %+v", p.Sets)
	}
}

func TestProgressPastEstimateIsIndeterminate(t *testing.T) {
	if got := ratioPercent("60800000", "15161306"); got != -1 {
		t.Fatalf("got %v", got)
	}
	if got := ratioPercent("1", "3"); got != 33.33 {
		t.Fatalf("got %v", got)
	}
	if got := ratioPercent("5", "0"); got != -1 {
		t.Fatalf("got %v", got)
	}
}

func TestDestinationProgressWithoutStats(t *testing.T) {
	p, err := parseDestinationProgress([]byte(`{"backupStats":null}`), "9")
	if err != nil || p.TargetID != "9" || p.Sync != nil {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := parseDestinationProgress([]byte(`{"backupStats":{"targetId":8}}`), "9"); !errors.Is(err, ErrProtocol) {
		t.Fatalf("want protocol error, got %v", err)
	}
}

func TestMaintenanceProgress(t *testing.T) {
	raw := []byte(`{"backupStats":{"targetId":"9","archiveMaintenanceStats":{"running":true,"verifyingBlockManifest":true,
		"verify":{"total":200,"start":50,"numCompleted":50}}}}`)
	p, err := parseDestinationProgress(raw, "9")
	if err != nil || p.Maintenance == nil || p.Maintenance.Step != "verifying_blocks" || p.Maintenance.Percent != 50 {
		t.Fatalf("%+v %v", p.Maintenance, err)
	}
}

func TestSettingsMismatchNamesTheField(t *testing.T) {
	_, err := projectSettings([]byte(`{"locale":{"value":7,"locked":false}}`), settingsAreas["ui"])
	var field *FieldError
	if !errors.As(err, &field) || field.Field != "locale" || !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v", err)
	}
}

func TestProgressLaneRecoversAfterLostAnswer(t *testing.T) {
	fail := true
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			io.WriteString(w, `{"ComputersMessage":{"computers":[{"peerId":"7","properties":{"ME":"true"}},{"peerId":"9","name":"Cloud","target":true,"availableTarget":true,"properties":{"CPC":"true"}}]}}`)
		case "/v0/GetBackupStats":
			if fail {
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, `{"backupStats":{"targetId":9,"validating":true,"syncProgressStats":{"current":1,"total":4,"files":true}}}`)
		default:
			t.Fatal(r.URL.Path)
		}
	})
	c.SetToken("fixture")
	first, err := c.BackupProgress(context.Background())
	if err != nil || len(first) != 1 || first[0].Error == "" {
		t.Fatalf("one failing destination must not fail the page: %+v %v", first, err)
	}
	fail = false
	c.progressAt = time.Time{}
	second, err := c.BackupProgress(context.Background())
	if err != nil || len(second) != 1 || second[0].Error != "" || second[0].Sync == nil || second[0].Sync.Percent != 25 || !second[0].Sync.Files {
		t.Fatalf("lane did not recover: %+v %v", second, err)
	}
	if c.legacy["BackupStatsMessage"].uncertain {
		t.Fatal("lane still uncertain after a verified answer")
	}
}
