package engine

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestR5MaintenanceUsesActualNativeConnectedShape(t *testing.T) {
	commands := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/GetComputers":
			io.WriteString(w, `{"UsersMessage":{"myUserId":1,"users":[{"userId":1}]},"ComputersMessage":{"computers":[{"peerId":"7","properties":{"ME":"true","OWN":"true"}},{"peerId":"8","properties":{"DCODE":"CONNECTED"}}]}}`)
		case "/v0/GetBackupStats":
			io.WriteString(w, `{"backupStats":{"sourceId":"7","targetId":"8","isBackupTarget":true,"remoteManifestSize":"1024"}}`)
		case "/v0/Command":
			commands++
			io.WriteString(w, `{"results":[{"command":{"name":"backup.maint"}}]}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	c.SetToken("fixture")
	if err := c.MaintainArchive(context.Background(), "7", "8"); err != nil {
		t.Fatalf("native connected cloud rejected: %v; dispatched=%d", err, commands)
	}
	if commands != 1 {
		t.Fatalf("commands=%d", commands)
	}
}
