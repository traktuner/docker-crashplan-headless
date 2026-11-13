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

func TestSettingsPatchPreservesUnchangedValuesAndChecksReadback(t *testing.T) {
	writes := 0
	current := `{"idleThrottleRate":{"value":80,"locked":false},"activeThrottleRate":{"value":20,"locked":false},"maintenanceInterval":{"value":"604800000","locked":false},"verifyBlocksInterval":{"value":"0","locked":false},"privateKey":"do-not-forward"}`
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/BackupConfig" {
			t.Error("wrong settings path")
		}
		if r.Method == "PUT" {
			writes++
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != `{"idleThrottleRate":{"value":30}}` {
				t.Error("unchanged/unknown native settings overwritten", string(raw))
			}
			current = strings.Replace(current, `"value":80`, `"value":30`, 1)
			return
		}
		io.WriteString(w, current)
	})
	c.SetToken("fixture")
	result, err := c.PatchSettings(context.Background(), "backup", map[string]json.RawMessage{"idleThrottleRate": json.RawMessage(`30`)})
	if err != nil || !result.Complete || !result.Applied["idleThrottleRate"] || writes != 1 {
		t.Fatal(result, err, writes)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "privateKey") || strings.Contains(string(raw), "do-not-forward") {
		t.Fatal("unknown native settings leaked")
	}
}

func TestLockedSettingsRefuseWholePatchBeforeAnyWrite(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("locked settings were written")
		}
		io.WriteString(w, `{"idleThrottleRate":{"value":80,"locked":true},"activeThrottleRate":{"value":20,"locked":false}}`)
	})
	c.SetToken("fixture")
	_, err := c.PatchSettings(context.Background(), "backup", map[string]json.RawMessage{"idleThrottleRate": json.RawMessage(`30`), "activeThrottleRate": json.RawMessage(`40`)})
	if !errors.Is(err, ErrSettingLocked) {
		t.Fatal(err)
	}
}

func TestSettingsNativeSilentIgnoreIsVisibleInResult(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"idleThrottleRate":{"value":80,"locked":false}}`)
		}
	})
	c.SetToken("fixture")
	result, err := c.PatchSettings(context.Background(), "backup", map[string]json.RawMessage{"idleThrottleRate": json.RawMessage(`30`)})
	if err != nil || result.Complete || result.Applied["idleThrottleRate"] {
		t.Fatal("ignored update reported applied", result, err)
	}
}

func TestSettingsRejectUnknownOrInvalidFieldsWithoutNativeAccess(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid patch reached native engine") })
	c.SetToken("fixture")
	for _, patch := range []map[string]json.RawMessage{{"serviceHost": json.RawMessage(`"0.0.0.0"`)}, {"idleThrottleRate": json.RawMessage(`0`)}, {"idleThrottleRate": json.RawMessage(`null`)}, {"maintenanceInterval": json.RawMessage(`"9223372036854775808"`)}, {}} {
		if _, err := c.PatchSettings(context.Background(), "backup", patch); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
}

func TestSettingLongsStayExactAndNativeBooleansStayTyped(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"userIdleDelay":{"value":"9007199254740993","locked":false},"systrayOnStartup":{"value":false,"locked":true},"password":"private-canary"}`)
	})
	c.SetToken("fixture")
	fields, err := c.Settings(context.Background(), "general")
	if err != nil || string(fields[0].Value) != `"9007199254740993"` || string(fields[1].Value) != `false` || !fields[1].Locked {
		t.Fatal(fields, err)
	}
}

func TestNativeNotificationEnumsAndOptionalProxy(t *testing.T) {
	def, _ := settingsDefinition("network")
	for _, value := range []string{"ANY", "MORNING", "AFTERNOON", "EVENING", "NIGHT", "NEVER"} {
		fields, err := projectSettings([]byte(`{"notifyDeliveryTime":{"value":"`+value+`","locked":false}}`), def)
		if err != nil {
			t.Fatal(value, err)
		}
		if fields[5].Kind != "notification" {
			t.Fatal(fields[5])
		}
	}
	def, _ = settingsDefinition("peer")
	fields, err := projectSettings([]byte(`{"useProxy":{"value":false,"locked":false},"remotePACFileUrl":{"locked":true,"dirty":false}}`), def)
	if err != nil || string(fields[1].Value) != "null" || !fields[1].Available || !fields[1].Locked {
		t.Fatal(fields, err)
	}
	for _, raw := range []string{`{"useProxy":{"locked":false}}`, `{"remotePACFileUrl":{"dirty":false}}`} {
		if _, err := projectSettings([]byte(raw), def); !errors.Is(err, ErrProtocol) {
			t.Fatal("missing required value/lock accepted", raw, err)
		}
	}
	for _, raw := range []string{`"13:00"`, `"UNKNOWN"`, `null`} {
		if _, err := normalizeSetting(settingsAreas["network"].Fields[5], json.RawMessage(raw)); err == nil {
			t.Fatal("invalid notification accepted", raw)
		}
	}
}
