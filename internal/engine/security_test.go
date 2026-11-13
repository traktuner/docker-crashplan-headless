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

func TestExplicitCustomKeyExportIsNotASerializableStatusDTO(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 56))
	c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"securityKeyType": "CustomKey", "dataKey": key})
	})
	c.SetToken("fixture")
	exported, err := c.ExportArchiveKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := exported.WriteTo(&output); err != nil || output.String() != key+"\n" {
		t.Fatal("explicit export failed", err)
	}
	raw, _ := json.Marshal(exported)
	if strings.Contains(string(raw), key) {
		t.Fatal("key escaped through JSON")
	}
}

func TestSecurityStatusNeverReturnsNativeKeyMaterial(t *testing.T) {
	c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"securityKeyType":"CustomKey","dataKey":"private-key-canary","question":"Recovery question"}`)
	})
	c.SetToken("fixture")
	state, err := c.Security(context.Background())
	if err != nil || state.Type != "CustomKey" {
		t.Fatal(state, err)
	}
	raw, _ := json.Marshal(state)
	if strings.Contains(string(raw), "private-key-canary") || strings.Contains(string(raw), "dataKey") {
		t.Fatal("key escaped public status")
	}
}

func TestArchiveUnlockRetainsKeyOnlyForCurrentSessionAndGeneration(t *testing.T) {
	c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/PrivatePasswordDataKey" || r.Method != "POST" {
			t.Error("wrong unlock route")
		}
		io.WriteString(w, `{"dataKey":"a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="}`)
	})
	c.SetToken("fixture")
	ctx := c.BindGeneration(context.Background())
	if err := c.UnlockArchive(ctx, ArchiveUnlock{SourceID: "7", Type: "PrivatePassword", Secret: "test-password"}); err != nil {
		t.Fatal(err)
	}
	if c.archiveKey("7") == "" || c.archiveKey("8") != "" {
		t.Fatal("unlock did not stay source bound")
	}
	c.SetToken("other-session")
	if c.archiveKey("7") != "" {
		t.Fatal("key survived a session change")
	}
	if err := c.UnlockArchive(ctx, ArchiveUnlock{SourceID: "7", Type: "PrivatePassword", Secret: "test-password"}); err != nil {
		t.Fatal(err)
	}
	c.BindGeneration(context.Background())
	if c.archiveKey("7") != "" {
		t.Fatal("key survived a generation change")
	}
	if err := c.UnlockArchive(ctx, ArchiveUnlock{SourceID: "7", Type: "PrivatePassword", Secret: "test-password"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("stale unlock accepted", err)
	}
}

func TestCustomKeyValidationFalseNeverUnlocks(t *testing.T) {
	c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"valid":false}`) })
	c.SetToken("fixture")
	if err := c.UnlockArchive(context.Background(), ArchiveUnlock{SourceID: "7", Type: "CustomKey", Secret: "a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="}); !errors.Is(err, ErrRejected) {
		t.Fatal(err)
	}
	if c.archiveKey("7") != "" {
		t.Fatal("invalid key retained")
	}
}

func TestSecurityUpgradeRequiresConfirmationAndForbidsDowngrade(t *testing.T) {
	writes := 0
	kind := "AccountPassword"
	c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"securityKeyType":"`+kind+`"}`)
			return
		}
		writes++
		if r.URL.Path != "/v0/SecurityKeyTypeRequest" || r.Method != "PUT" {
			t.Error("wrong upgrade route")
		}
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["importFile"]; ok {
			t.Error("arbitrary import exposed")
		}
		kind = "PrivatePassword"
		io.WriteString(w, `{"errors":[]}`)
	})
	c.SetToken("fixture")
	in := SecurityUpgrade{Type: "PrivatePassword", Password: "long-password"}
	if _, err := c.UpgradeSecurity(context.Background(), in); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("missing confirmation", err)
	}
	in.Confirm = "UPGRADE TO PrivatePassword"
	if result, err := c.UpgradeSecurity(context.Background(), in); err != nil || result.Type != "PrivatePassword" {
		t.Fatal(result, err)
	}
	if _, err := c.UpgradeSecurity(context.Background(), in); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("same key type was mutated again", err)
	}
	in.Type = "AccountPassword"
	in.Confirm = "UPGRADE TO AccountPassword"
	if _, err := c.UpgradeSecurity(context.Background(), in); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("downgrade accepted", err)
	}
	if writes != 1 {
		t.Fatal("unexpected native mutations", writes)
	}
}

func TestShortCustomKeyNeverReachesNative(t *testing.T) {
	c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("short custom key reached native")
		io.WriteString(w, `{"valid":true}`)
	})
	c.SetToken("fixture")
	if err := c.UnlockArchive(context.Background(), ArchiveUnlock{SourceID: "7", Type: "CustomKey", Secret: "YQ=="}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestArchivePasswordValidationAndNativeErrors(t *testing.T) {
	calls := 0
	c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(400)
		io.WriteString(w, `{"errors":[{"id":"INCORRECT_LOGIN_PASSWORD","message":"secret-canary"}]}`)
	})
	c.SetToken("fixture")
	in := ArchivePassword{Action: "change", CurrentPassword: "test", Password: "short"}
	if err := c.ArchivePassword(context.Background(), in); !errors.Is(err, ErrInvalidRequest) || calls != 0 {
		t.Fatal(err, calls)
	}
	in.Password = "long-password"
	if err := c.ArchivePassword(context.Background(), in); err == nil || strings.Contains(err.Error(), "secret-canary") {
		t.Fatal("rejection lost", err)
	}
	if calls != 1 {
		t.Fatal("password operation repeated")
	}
}

// Sensitive-operation fixtures expose the real native identity envelopes.
func identityFixture(t *testing.T, h http.HandlerFunc) *Client {
	return fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/GetComputers" {
			io.WriteString(w, `{"UsersMessage":{"myUserId":"1","users":[{"userId":"1","allowLocalFolders":true,"autoOfferSelf":true}]},"ComputersMessage":{"computers":[{"peerId":"7","userId":"1","properties":{"ME":"true","OWN":"true"}}]}}`)
			return
		}
		h(w, r)
	})
}
