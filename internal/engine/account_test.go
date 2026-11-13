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

func TestPendingMigrationUsesOfferedDeviceAndReplacesPrivateToken(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/LoginRequest":
			io.WriteString(w, `{"httpToken":"pending-private","selfServiceCheckJson":"{\"status\":\"SELECTION_REQUIRED\",\"devices\":[{\"computerGuid\":\"9007199254740993\",\"name\":\"Old device\",\"password\":\"never-forward\"}]}"}`)
		case "/v1/DeviceMigrationSelfService":
			var body map[string]string
			if r.Header.Get("Authorization") != "token pending-private" || json.NewDecoder(r.Body).Decode(&body) != nil || body["selectedComputerGuid"] != "9007199254740993" {
				t.Error("wrong migration binding")
			}
			io.WriteString(w, `{"httpToken":"new-private"}`)
		default:
			t.Error("unexpected account request", r.URL.Path)
		}
	})
	result, err := c.Login(context.Background(), Credentials{Username: "user", Password: "test", ServerAddress: "console.example.invalid"})
	if err != nil || result.Complete {
		t.Fatal(result, err)
	}
	pending := c.Migration()
	if pending.Status != "SELECTION_REQUIRED" || len(pending.Devices) != 1 {
		t.Fatal(pending)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "never-forward") || strings.Contains(string(raw), "pending-private") {
		t.Fatal("raw migration data escaped")
	}
	if _, err := c.SelectMigration(context.Background(), "9"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("unoffered device accepted", err)
	}
	result, err = c.SelectMigration(context.Background(), "9007199254740993")
	if err != nil || !result.Complete || c.nativeToken() != "new-private" {
		t.Fatal(result, err)
	}
	if c.Migration().Status == "SELECTION_REQUIRED" {
		t.Fatal("completed selection retained")
	}
}

func TestMigrationWithoutPendingLoginNeverReachesNative(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("migration without bound login") })
	if _, err := c.SelectMigration(context.Background(), ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestAuthorizedMigrationRequiresOfferedGUIDAndExplicitConfirmation(t *testing.T) {
	posts := 0
	c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/DeviceMigrationAuthorized" {
			t.Error("wrong migration route")
		}
		if r.Method == "POST" {
			posts++
			io.WriteString(w, `{"success":true}`)
			return
		}
		io.WriteString(w, `{"status":"READY","devices":[{"computerGuid":"7","name":"Old device"}],"deviceMigrationGuid":"do-not-forward"}`)
	})
	c.SetToken("fixture")
	if err := c.AuthorizeMigration(context.Background(), "7", "different"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if err := c.AuthorizeMigration(context.Background(), "9", "9"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if err := c.AuthorizeMigration(context.Background(), "7", "7"); err != nil || posts != 1 {
		t.Fatal(err, posts)
	}
}

func TestNativeLoginRetriesOnlyExplicitReattemptResponse(t *testing.T) {
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(400)
			io.WriteString(w, `{"errors":[{"id":"REATTEMPT_LOGIN"}]}`)
			return
		}
		io.WriteString(w, `{"httpToken":"final-private"}`)
	})
	if _, err := c.Login(context.Background(), Credentials{Username: "user", Password: "test", ServerAddress: "console.example.invalid"}); err != nil || calls != 2 {
		t.Fatal("explicit login reattempt failed", err, calls)
	}
}

func TestAccountRequestHasItsOwnDeclaredTimeout(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		io.WriteString(w, `{"httpToken":"account-private"}`)
	})
	c.http.Timeout = 5 * time.Millisecond
	if err := c.SetAccountTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Login(context.Background(), Credentials{Username: "user", Password: "test", ServerAddress: "console.example.invalid"}); err != nil {
		t.Fatal("ordinary request deadline truncated login", err)
	}
}

func TestExplicitLoginReattemptIsBoundedAndOtherErrorsNeverRetry(t *testing.T) {
	for _, id := range []string{"REATTEMPT_LOGIN", "FAILED", "TIMED_OUT", "SYSTEM"} {
		calls := 0
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"id": id}}})
		})
		_, err := c.Login(context.Background(), Credentials{Username: "fixture", Password: "fixture-password", ServerAddress: "console.fixture.invalid"})
		expected := 1
		if id == "REATTEMPT_LOGIN" {
			expected = 4
		}
		if err == nil || calls != expected {
			t.Fatal(id, err, calls)
		}
		if (id == "SYSTEM" || id == "TIMED_OUT") && !errors.Is(err, ErrIndeterminate) {
			t.Fatal("ambiguous native login was declared rejected", id, err)
		}
	}
}
