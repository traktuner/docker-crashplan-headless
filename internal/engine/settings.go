package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
)

var ErrSettingLocked = errors.New("SETTING_LOCKED")

type SettingSpec struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Unit  string `json:"unit,omitempty"`
}
type Setting struct {
	SettingSpec
	Value     json.RawMessage `json:"value"`
	Locked    bool            `json:"locked"`
	Available bool            `json:"available"`
}
type SettingsResult struct {
	Settings []Setting       `json:"settings"`
	Applied  map[string]bool `json:"applied"`
	Complete bool            `json:"complete"`
}
type settingsArea struct {
	Path   string
	Fields []SettingSpec
}

// These are the fields applied by the inspected native handlers. Project only
// these public settings; never return the rest of the native configuration.
var settingsAreas = map[string]settingsArea{
	"backup": {Path: "/v1/BackupConfig", Fields: []SettingSpec{
		{"maintenanceInterval", "Archive maintenance interval", "long", "ms"},
		{"verifyBlocksInterval", "Verify block interval", "long", "ms"},
		{"idleThrottleRate", "CPU limit while idle", "cpu", "%"},
		{"activeThrottleRate", "CPU limit while active", "cpu", "%"},
	}},
	"network": {Path: "/v1/ServiceBackupConfig", Fields: []SettingSpec{
		{"highBandwidthRate", "WAN transfer rate while idle", "rate", "KB/s"},
		{"lowBandwidthRate", "WAN transfer rate while active", "rate", "KB/s"},
		{"lanHighBandwidthRate", "LAN transfer rate while idle", "rate", "KB/s"},
		{"lanLowBandwidthRate", "LAN transfer rate while active", "rate", "KB/s"},
		{"lanTcpTrafficClass", "LAN TCP traffic class", "byte", ""},
		{"notifyDeliveryTime", "Notification delivery time", "notification", ""},
		{"warningEmailEnabled", "Warning emails", "boolean", ""},
		{"minutesUntilWarning", "Warning after", "integer", "minutes"},
		{"severeEmailEnabled", "Critical warning emails", "boolean", ""},
		{"minutesUntilSevere", "Critical warning after", "integer", "minutes"},
		{"backupStatusEmailEnabled", "Backup status emails", "boolean", ""},
		{"backupStatusEmailFreqInMinutes", "Status email interval", "integer", "minutes"},
		{"inboundRunWindow", "Inbound backup window", "window", ""},
		{"backupStatus", "Backup status notification format", "text", ""},
		{"networkInterfacesExcluded", "Excluded network interfaces", "patterns", ""},
		{"minimumBatteryPercent", "Minimum battery charge", "percent", "%"},
	}},
	"general": {Path: "/v1/GeneralSettings", Fields: []SettingSpec{
		{"userIdleDelay", "Consider user idle after", "long", "ms"},
		{"systrayOnStartup", "Native tray preference", "boolean", ""},
	}},
	"ui": {Path: "/v1/ServiceUIConfig", Fields: []SettingSpec{
		{"showFullFilePath", "Show full file paths", "boolean", ""},
		{"autoLogin", "Allow native automatic sign-in", "boolean", ""},
		{"locale", "Language locale", "text", ""},
	}},
	"peer": {Path: "/v1/ServicePeerConfig", Fields: []SettingSpec{
		{"useProxy", "Use proxy auto-configuration", "boolean", ""},
		{"remotePACFileUrl", "Proxy auto-configuration URL", "optional-url", ""},
	}},
}

type RunWindow struct {
	AlwaysRun bool     `json:"alwaysRun"`
	Start     string   `json:"startTimeOfDay"`
	End       string   `json:"endTimeOfDay"`
	Days      []string `json:"days"`
}
type Pattern struct {
	Pattern string `json:"pattern"`
	Regular bool   `json:"regularExpression"`
}

func strictJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return ErrInvalidRequest
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrInvalidRequest
	}
	return nil
}
func clockTime(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	h, err := strconv.Atoi(s[:2])
	m, e := strconv.Atoi(s[3:])
	return err == nil && e == nil && h >= 0 && h < 24 && m >= 0 && m < 60 && strconv.Itoa(h/10)+strconv.Itoa(h%10) == s[:2] && strconv.Itoa(m/10)+strconv.Itoa(m%10) == s[3:]
}
func (w RunWindow) validate() error {
	if !w.AlwaysRun && (!clockTime(w.Start) || !clockTime(w.End) || len(w.Days) == 0) {
		return ErrInvalidRequest
	}
	if len(w.Days) > 7 {
		return ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, day := range w.Days {
		switch day {
		case "SUNDAY", "MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY", "SATURDAY":
		default:
			return ErrInvalidRequest
		}
		if seen[day] {
			return ErrInvalidRequest
		}
		seen[day] = true
	}
	return nil
}
func normalizeSetting(spec SettingSpec, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ErrInvalidRequest
	}
	var value any
	switch spec.Kind {
	case "compression", "dedup", "notification":
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil, ErrInvalidRequest
		}
		if spec.Kind == "compression" {
			switch text {
			case "ON", "OFF", "AUTOMATIC":
			default:
				return nil, ErrInvalidRequest
			}
		} else if spec.Kind == "notification" {
			switch text {
			case "ANY", "MORNING", "AFTERNOON", "EVENING", "NIGHT", "NEVER":
			default:
				return nil, ErrInvalidRequest
			}
		} else {
			switch text {
			case "FULL", "MINIMAL", "AUTOMATIC":
			default:
				return nil, ErrInvalidRequest
			}
		}
		value = text
	case "boolean":
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return nil, ErrInvalidRequest
		}
		value = b
	case "cpu", "byte", "percent", "integer", "long", "retention-interval":
		var number json.Number
		if json.Unmarshal(raw, &number) != nil {
			return nil, ErrInvalidRequest
		}
		n, err := number.Int64()
		min, max := int64(0), int64(2147483647)
		if spec.Kind == "retention-interval" {
			min = -1
		}
		if spec.Kind == "long" {
			max = math.MaxInt64
		}
		if spec.Kind == "cpu" {
			min = 1
			max = 100
		}
		if spec.Kind == "percent" {
			max = 100
		}
		if spec.Kind == "byte" {
			max = 255
		}
		if err != nil || n < min || n > max || strconv.FormatInt(n, 10) != number.String() {
			return nil, ErrInvalidRequest
		}
		if spec.Kind == "long" {
			value = number.String()
		} else {
			value = n
		}
	case "rate":
		var number json.Number
		if json.Unmarshal(raw, &number) != nil {
			return nil, ErrInvalidRequest
		}
		n, err := strconv.ParseFloat(number.String(), 32)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return nil, ErrInvalidRequest
		}
		value = n
	case "text", "time", "url", "optional-url":
		var text string
		if json.Unmarshal(raw, &text) != nil || len(text) > 4096 || strings.ContainsAny(text, "\x00\r\n") {
			return nil, ErrInvalidRequest
		}
		if spec.Kind == "time" && !clockTime(text) {
			return nil, ErrInvalidRequest
		}
		if (spec.Kind == "url" || spec.Kind == "optional-url") && text != "" {
			u, err := url.Parse(text)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
				return nil, ErrInvalidRequest
			}
		}
		value = text
	case "window":
		var window RunWindow
		if strictJSON(raw, &window) != nil || window.validate() != nil {
			return nil, ErrInvalidRequest
		}
		value = window
	case "patterns":
		var patterns []Pattern
		if strictJSON(raw, &patterns) != nil || patterns == nil || len(patterns) > 1000 {
			return nil, ErrInvalidRequest
		}
		for _, pattern := range patterns {
			if pattern.Pattern == "" || len(pattern.Pattern) > 4096 || strings.ContainsRune(pattern.Pattern, 0) {
				return nil, ErrInvalidRequest
			}
		}
		value = patterns
	default:
		return nil, ErrInvalidRequest
	}
	out, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	return out, nil
}
func settingsDefinition(area string) (settingsArea, error) {
	definition, ok := settingsAreas[area]
	if !ok {
		return definition, ErrInvalidRequest
	}
	return definition, nil
}
func (c *Client) Settings(ctx context.Context, area string) ([]Setting, error) {
	ctx = c.operationContext(ctx)
	definition, err := settingsDefinition(area)
	if err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, "GET", definition.Path, nil, true, false)
	if err != nil {
		return nil, err
	}
	if _, ids, e := responseEnvelope(raw, ""); e != nil || len(ids) > 0 {
		if e != nil {
			return nil, e
		}
		return nil, ErrRejected
	}
	return projectSettings(raw, definition)
}

func projectSettings(raw []byte, definition settingsArea) ([]Setting, error) {
	var err error
	var wire map[string]json.RawMessage
	if json.Unmarshal(raw, &wire) != nil || wire == nil {
		return nil, &FieldError{Field: "settings"}
	}
	out := make([]Setting, 0, len(definition.Fields))
	for _, spec := range definition.Fields {
		item := Setting{SettingSpec: spec, Value: json.RawMessage(`null`), Locked: true}
		if raw, ok := wire[spec.Name]; ok {
			var field struct {
				Value  json.RawMessage `json:"value"`
				Locked *bool           `json:"locked"`
			}
			if json.Unmarshal(raw, &field) != nil || field.Locked == nil || (len(field.Value) == 0 && spec.Kind != "optional-url") {
				return nil, &FieldError{Field: spec.Name}
			}
			item.Available = true
			item.Locked = *field.Locked
			if len(field.Value) > 0 && string(field.Value) != "null" {
				item.Value, err = normalizeSetting(spec, field.Value)
				if err != nil {
					return nil, &FieldError{Field: spec.Name}
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}
func (c *Client) PatchSettings(ctx context.Context, area string, changes map[string]json.RawMessage) (SettingsResult, error) {
	ctx = c.operationContext(ctx)
	definition, err := settingsDefinition(area)
	if err != nil || len(changes) == 0 || len(changes) > len(definition.Fields) {
		return SettingsResult{}, ErrInvalidRequest
	}
	values := map[string]json.RawMessage{}
	for name, raw := range changes {
		found := false
		for _, spec := range definition.Fields {
			if spec.Name == name {
				value, e := normalizeSetting(spec, raw)
				if e != nil {
					return SettingsResult{}, e
				}
				values[name] = value
				found = true
				break
			}
		}
		if !found {
			return SettingsResult{}, ErrInvalidRequest
		}
	}
	before, err := c.Settings(ctx, area)
	if err != nil {
		return SettingsResult{}, err
	}
	for name := range values {
		for _, field := range before {
			if field.Name == name {
				if !field.Available {
					return SettingsResult{}, ErrProtocol
				}
				if field.Locked {
					return SettingsResult{}, ErrSettingLocked
				}
			}
		}
	}
	patch := map[string]any{}
	for name, value := range values {
		patch[name] = struct {
			Value json.RawMessage `json:"value"`
		}{value}
	}
	body, _ := json.Marshal(patch)
	raw, err := c.request(ctx, "PUT", definition.Path, body, true, true)
	if err != nil {
		return SettingsResult{}, err
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if _, ids, e := responseEnvelope(raw, ""); e != nil || len(ids) > 0 {
			return SettingsResult{}, ErrIndeterminate
		}
	}
	after, err := c.Settings(ctx, area)
	if err != nil {
		return SettingsResult{}, errors.Join(ErrIndeterminate, err)
	}
	result := SettingsResult{Settings: after, Applied: map[string]bool{}, Complete: true}
	for name, want := range values {
		for _, field := range after {
			if field.Name == name {
				result.Applied[name] = field.Available && bytes.Equal(field.Value, want)
			}
		}
		if !result.Applied[name] {
			result.Complete = false
		}
	}
	return result, nil
}
