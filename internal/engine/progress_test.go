package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
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
		"reduceState":"STEP_1_PRUNE_FILES","verify":{"total":200,"start":50,"numCompleted":50},"step1":{"total":10,"numCompleted":1}}}}`)
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
		case "/v0/GetBackupStatsSummary":
			io.WriteString(w, `{"stats":{"resumeSystemTime":0}}`)
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

func TestProgressDeviceLaneRecovers(t *testing.T) {
	fail := true
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			if fail {
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, `{"ComputersMessage":{"computers":[{"peerId":"7","properties":{"ME":"true"}}]}}`)
		default:
			t.Fatal(r.URL.Path)
		}
	})
	c.SetToken("fixture")
	if _, err := c.BackupProgress(context.Background()); err == nil {
		t.Fatal("want error")
	}
	// The error is shared for the fresh window instead of refetching.
	if _, err := c.BackupProgress(context.Background()); err == nil {
		t.Fatal("want cached error")
	}
	fail = false
	c.progressAt = time.Time{}
	if out, err := c.BackupProgress(context.Background()); err != nil || len(out) != 0 {
		t.Fatalf("%v %v", out, err)
	}
	if _, err := c.Devices(context.Background()); err != nil {
		t.Fatalf("device lane still blocked: %v", err)
	}
}

func TestProgressReadingCombinesStockSources(t *testing.T) {
	var computers, scans, summaries int
	resume := time.Now().Add(time.Hour).UnixMilli()
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v0/GetComputers":
			computers++
			io.WriteString(w, `{"ComputersMessage":{"computers":[
				{"peerId":"7","properties":{"ME":"true"}},
				{"peerId":"9","name":"Cloud","target":true,"properties":{"CPC":"true","DCODE":"NOT_AUTHORIZED"}},
				{"peerId":"11","name":"Disk","target":true,"parentGuid":"7","properties":{}}]}}`)
		case "/v0/GetBackupStatsSummary":
			summaries++
			fmt.Fprintf(w, `{"stats":{"resumeSystemTime":%d}}`, resume)
		case "/v0/GetBackupStats":
			var in struct {
				Target string `json:"targetId"`
			}
			json.Unmarshal(body, &in)
			fmt.Fprintf(w, `{"backupStats":{"targetId":%s,"remoteManifestSize":"123","fileTodoStats":{
				"9007199254740993":{"selectedForBackup":true,"numFilesScanned":10,"numBytesScanned":"1000","numRemainingFilesToBackup":2,
					"numRemainingSourceBytes":"400","numScheduledFiles":1,"numScheduledBytes":"50","lastBackupTimestamp":1600000000000,
					"sendingFileTodo":{"doneSending":false,"numSourceBytesCompleted":"100"}},
				"5":{"numFilesScanned":0,"numBytesScanned":"0","lastBackupTimestamp":-1}}}}`, in.Target)
		case "/v0/GetScanStats":
			scans++
			var in struct {
				ID string `json:"backupSetId"`
			}
			json.Unmarshal(body, &in)
			if in.ID == "5" {
				// A foreign answer must not lend its numbers to set 5.
				io.WriteString(w, `{"backupSetId":"6","scanning":false,"totalFiles":1,"totalSize":"1"}`)
				return
			}
			fmt.Fprintf(w, `{"backupSetId":"%s","scanning":false,"checkingForDeletes":true,"totalFiles":20,"totalSize":"2000"}`, in.ID)
		default:
			t.Error(r.URL.Path)
		}
	})
	c.SetToken("fixture")
	out, err := c.BackupProgress(context.Background())
	if err != nil || len(out) != 2 {
		t.Fatal(out, err)
	}
	cloud, disk := out[0], out[1]
	if cloud.Connected || cloud.DisconnectCode != "NOT_AUTHORIZED" || !disk.Connected || disk.DisconnectCode != "" {
		t.Fatalf("connection state %+v %+v", cloud, disk)
	}
	if cloud.SystemResumeTime != strconv.FormatInt(resume, 10) || disk.SystemResumeTime != cloud.SystemResumeTime {
		t.Fatal("system pause", cloud.SystemResumeTime)
	}
	if len(cloud.Sets) != 2 || cloud.Sets[0].BackupSetID != "5" || cloud.Sets[1].BackupSetID != "9007199254740993" {
		t.Fatalf("set order %+v", cloud.Sets)
	}
	empty, big := cloud.Sets[0], cloud.Sets[1]
	if empty.Percent != -1 || empty.Complete || empty.EverBackedUp || empty.TotalFiles != "0" || empty.CheckingForDeletes {
		t.Fatalf("set without scan statistics %+v", empty)
	}
	// (2000 - (400 - 100)) / 2000 = 85 %; scheduled files keep it incomplete.
	if big.Percent != 85 || big.Complete || !big.EverBackedUp || !big.CheckingForDeletes || big.TotalFiles != "20" || big.TotalBytes != "2000" ||
		big.ScheduledBytes != "50" || big.InFlightBytes != "100" {
		t.Fatalf("set %+v", big)
	}
	// One scan statistics read per set, shared by both destinations.
	if scans != 2 || summaries != 1 || computers != 1 {
		t.Fatal(scans, summaries, computers)
	}
	raw, _ := json.Marshal(cloud)
	for _, field := range []string{`"connected":false`, `"disconnectCode":"NOT_AUTHORIZED"`, `"totalBytes":"2000"`, `"scheduledBytes":"50"`, `"inFlightBytes":"100"`, `"percent":85`, `"complete":false`, `"checkingForDeletes":true`, `"everBackedUp":true`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("missing %s in %s", field, raw)
		}
	}
	if strings.Contains(string(raw), "123") || strings.Contains(string(raw), "todo") {
		t.Fatal("internal fields leaked", string(raw))
	}
	// The computer list is reused for the next readings of the same session.
	c.progressAt = time.Time{}
	if _, err := c.BackupProgress(context.Background()); err != nil || computers != 1 {
		t.Fatal("target list not cached", computers, err)
	}
	c.InvalidateTargets()
	c.progressAt = time.Time{}
	if _, err := c.BackupProgress(context.Background()); err != nil || computers != 2 {
		t.Fatal("invalidation ignored", computers, err)
	}
	c.SetToken("other")
	if _, err := c.BackupProgress(context.Background()); err != nil || computers != 3 {
		t.Fatal("cache crossed a session revision", computers, err)
	}
}

func TestProgressFailsWithoutSystemPauseState(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			io.WriteString(w, `{"ComputersMessage":{"computers":[{"peerId":"7","properties":{"ME":"true"}},{"peerId":"9","target":true,"properties":{"DCODE":"CONNECTED"}}]}}`)
		case "/v0/GetBackupStats":
			io.WriteString(w, `{"backupStats":{"targetId":9}}`)
		case "/v0/GetBackupStatsSummary":
			w.WriteHeader(500)
		default:
			t.Error(r.URL.Path)
		}
	})
	c.SetToken("fixture")
	if _, err := c.BackupProgress(context.Background()); err == nil {
		t.Fatal("unknown system pause reported as not paused")
	}
}
