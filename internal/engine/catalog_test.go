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
