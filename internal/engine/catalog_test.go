package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestArchiveSetsUseSelectedSourceAndDestination(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/BackupSets" || r.URL.Query().Get("sourceGuid") != "9007199254740993" || r.URL.Query().Get("destinationGuid") != "8" {
			t.Error("archive context was lost")
		}
		io.WriteString(w, `[{"id":"9007199254740995","name":"Archived set","privateKey":"canary"}]`)
	})
	c.SetToken("fixture")
	sets, err := c.ArchiveSets(context.Background(), "9007199254740993", "8")
	if err != nil || len(sets) != 1 || sets[0].ID != "9007199254740995" {
		t.Fatal(sets, err)
	}
}

func TestDeviceCatalogProjectsNamesAndExactIDsWithoutPrivateBodies(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/GetComputers" || r.Header.Get("Authorization") != "token fixture" {
			t.Error("wrong device request")
		}
		io.WriteString(w, `{"UsersMessage":{"password":"private-canary"},"ComputersMessage":{"computers":[{"peerId":"9007199254740993","name":"Source","source":true,"target":false,"availableTarget":false,"properties":{"ME":"true"},"transportPublicKey":"private-canary"},{"peerId":"8","name":"Cloud","source":false,"target":true,"availableTarget":true,"properties":{}}]},"TargetComputerUsagesMessage":{"token":"private-canary"}}`)
	})
	c.SetToken("fixture")
	devices, err := c.Devices(context.Background())
	if err != nil || len(devices) != 2 || devices[0].ID != "9007199254740993" || !devices[0].Self || devices[1].Name != "Cloud" {
		t.Fatal(devices, err)
	}
	raw, _ := json.Marshal(devices)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "token") {
		t.Fatal("private social model forwarded")
	}
}

func TestDevicesCarryConnectionAndQuota(t *testing.T) {
	devices, err := parseDevices([]byte(`{"computers":[
		{"peerId":"7","name":"Me","properties":{"ME":"true"}},
		{"peerId":"9","name":"Cloud","target":true,"offeredBytesOnTarget":"-1","properties":{"CPC":"true","DCODE":"NEW_VERSION"}},
		{"peerId":"10","name":"Remote","target":true,"offeredBytesOnTarget":"9007199254740993","properties":{"DCODE":"CONNECTED"}},
		{"peerId":"11","name":"Disk","target":true,"parentGuid":"7","userId":3,"properties":{"DCODE":"DISCONNECTED"}},
		{"peerId":"12","name":"Odd","target":true,"properties":{"DCODE":"not an enum"}}]}`))
	if err != nil || len(devices) != 5 {
		t.Fatal(devices, err)
	}
	me, cloud, remote, disk, odd := devices[0], devices[1], devices[2], devices[3], devices[4]
	if !me.Connected || me.DisconnectCode != "" || me.QuotaBytes != "" {
		t.Fatalf("self %+v", me)
	}
	if cloud.Connected || cloud.DisconnectCode != "NEW_VERSION" || cloud.QuotaBytes != "-1" {
		t.Fatalf("cloud %+v", cloud)
	}
	if !remote.Connected || remote.DisconnectCode != "" || remote.QuotaBytes != "9007199254740993" {
		t.Fatalf("remote %+v", remote)
	}
	// A child (local folder) destination counts as connected, as in the stock app.
	if !disk.Connected || disk.DisconnectCode != "" || disk.parentID != "7" || disk.userID != "3" {
		t.Fatalf("disk %+v", disk)
	}
	if odd.Connected || odd.DisconnectCode != "" {
		t.Fatalf("unknown code must be omitted %+v", odd)
	}
	if _, err := parseDevices([]byte(`{"computers":[{"peerId":"7","parentGuid":"x"}]}`)); err == nil {
		t.Fatal("invalid parent accepted")
	}
}

func TestDeviceDetailsAddUsageAndLocalLocation(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			io.WriteString(w, `{"ComputersMessage":{"computers":[
				{"peerId":"7","name":"Me","properties":{"ME":"true"}},
				{"peerId":"9","name":"Cloud","target":true,"properties":{"CPC":"true","DCODE":"CONNECTED"}},
				{"peerId":"11","name":"Disk","target":true,"parentGuid":"7","userId":3,"properties":{}}]}}`)
		case "/v0/GetBackupStatsSummary":
			io.WriteString(w, `{"stats":{"resumeSystemTime":0}}`)
		case "/v0/GetBackupStats":
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), `"11"`) {
				io.WriteString(w, `{"backupStats":{"targetId":11,"remoteManifestSize":"4096"}}`)
				return
			}
			io.WriteString(w, `{"backupStats":{"targetId":9,"remoteManifestSize":9007199254740993}}`)
		case "/v1/SocialNetworkConfig":
			io.WriteString(w, `{"computerConfigs":{"map":{"11":{"manifestPath":{"value":"/backups/disk"}}}},"userConfigs":{"map":{}}}`)
		default:
			t.Error(r.URL.Path)
		}
	})
	c.SetToken("fixture")
	devices, err := c.DeviceDetails(context.Background())
	if err != nil || len(devices) != 3 {
		t.Fatal(devices, err)
	}
	if devices[0].UsedBytes != "" || devices[0].Location != "" {
		t.Fatalf("self %+v", devices[0])
	}
	if devices[1].UsedBytes != "9007199254740993" || devices[1].Location != "" {
		t.Fatalf("cloud %+v", devices[1])
	}
	if devices[2].UsedBytes != "4096" || devices[2].Location != "/backups/disk/7" {
		t.Fatalf("disk %+v", devices[2])
	}
	raw, _ := json.Marshal(devices[2])
	if strings.Contains(string(raw), "userId") || strings.Contains(string(raw), "parent") {
		t.Fatal("internal fields leaked", string(raw))
	}
}
