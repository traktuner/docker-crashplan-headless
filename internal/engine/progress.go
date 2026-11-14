package engine

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"
)

// DestinationProgress is what the desktop app shows per destination: the
// synchronization ("Synchronizing block information 42%"), archive maintenance,
// and the transfer per backup set. Read from PUT /v0/GetBackupStats, which the
// desktop app polls every second for each backup target.
type DestinationProgress struct {
	TargetID     string             `json:"targetId"`
	Name         string             `json:"name"`
	BackingUp    bool               `json:"backingUp"`
	Validating   bool               `json:"validating"`
	Sync         *ProgressCounter   `json:"sync,omitempty"`
	Maintenance  *MaintenanceStatus `json:"maintenance,omitempty"`
	NotReadyCode string             `json:"notReadyCode,omitempty"`
	OutOfSpace   bool               `json:"outOfSpace"`
	RateBytes    string             `json:"completedRateBytesPerSecond"`
	Sets         []SetProgress      `json:"sets"`
	// Error is set when this destination could not be read; others still are.
	Error string `json:"error,omitempty"`
}

// ProgressCounter carries exact decimal counts and a percentage clamped to
// 0..100 the way the desktop app clamps it. Percent is -1 when no total exists.
type ProgressCounter struct {
	Current string  `json:"current"`
	Total   string  `json:"total"`
	Percent float64 `json:"percent"`
	Files   bool    `json:"files"`
}

type MaintenanceStatus struct {
	Cache   bool    `json:"cache"`
	Deep    bool    `json:"deep"`
	Step    string  `json:"step"`
	Percent float64 `json:"percent"`
}

type SetProgress struct {
	BackupSetID    string `json:"backupSetId"`
	Scanning       bool   `json:"scanning"`
	Selected       bool   `json:"selected"`
	FilesScanned   string `json:"filesScanned"`
	BytesScanned   string `json:"bytesScanned"`
	RemainingFiles string `json:"remainingFiles"`
	RemainingBytes string `json:"remainingBytes"`
	LastBackup     string `json:"lastBackup"`
	NextBackup     string `json:"nextBackup"`
	// ResumeTime is the epoch millisecond time a paused backup resumes; 0 or
	// a past time means not paused (desktop: resumeTime - now).
	ResumeTime string `json:"resumeTime"`
	// SendingPath is the file being transferred right now, as in the desktop status line.
	SendingPath string `json:"sendingPath,omitempty"`
}

// progressFresh is how long one engine reading serves every open page.
const progressFresh = 2 * time.Second

// BackupProgress reads the progress of every backup destination of this device.
// One reading is shared by all callers for a short time, so several open pages
// do not multiply the engine requests.
func (c *Client) BackupProgress(ctx context.Context) ([]DestinationProgress, error) {
	ctx = c.operationContext(ctx)
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	revision := c.SessionRevision()
	if c.progressCache != nil && c.progressRev == revision && time.Since(c.progressAt) < progressFresh {
		return c.progressCache, nil
	}
	// A closed page must not abandon a native request halfway: the engine
	// answer is still read, so the response lane stays in step.
	native, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	devices, err := c.Devices(native)
	if err != nil {
		return nil, err
	}
	out := []DestinationProgress{}
	for _, device := range devices {
		if !device.Target || device.Deleted || device.Self {
			continue
		}
		progress, err := c.destinationProgress(native, device.ID)
		if err != nil {
			progress = DestinationProgress{TargetID: device.ID, Sets: []SetProgress{}, RateBytes: "0", Error: progressError(err)}
		}
		progress.Name = device.Name
		out = append(out, progress)
	}
	c.progressCache, c.progressAt, c.progressRev = out, time.Now(), revision
	return out, nil
}

func progressError(err error) string {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return "NATIVE_AUTHORIZATION_REQUIRED"
	case errors.Is(err, ErrProtocol):
		return "NATIVE_PROTOCOL_MISMATCH"
	default:
		return "NATIVE_ENGINE_UNAVAILABLE"
	}
}

func (c *Client) destinationProgress(ctx context.Context, target string) (DestinationProgress, error) {
	body, _ := json.Marshal(map[string]string{"targetId": target})
	var parsed DestinationProgress
	raw, err := c.legacyProbe(ctx, "BackupStatsMessage", "PUT", "/v0/GetBackupStats", body, func(raw []byte) bool {
		decoded, e := restoreResponse(raw, nil, "BackupStatsMessage")
		if e != nil {
			return false
		}
		p, e := parseDestinationProgress(decoded, target)
		parsed = p
		return e == nil
	})
	if err != nil {
		return DestinationProgress{}, err
	}
	_ = raw
	return parsed, nil
}

// legacyProbe is legacyRequest for read-only status classes. A lost answer
// marks the lane uncertain like any legacy request, but a later answer that
// verifiably belongs to this request (matching target) clears it again, so a
// closed page or one timeout does not disable the class until a restart.
func (c *Client) legacyProbe(ctx context.Context, class, method, path string, body []byte, verify func([]byte) bool) ([]byte, error) {
	c.mu.Lock()
	if ctx.Value(generationKey{}) == nil && c.lifetime != nil {
		ctx = context.WithValue(ctx, generationKey{}, c.lifetime.Value(generationKey{}))
	}
	if c.legacy == nil {
		c.legacy = make(map[string]*legacyLane)
	}
	lane := c.legacy[class]
	if lane == nil {
		lane = &legacyLane{gate: make(chan struct{}, 1)}
		c.legacy[class] = lane
	}
	c.mu.Unlock()
	select {
	case lane.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
	defer func() { <-lane.gate }()
	if !c.currentGeneration(ctx) || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	raw, err := c.request(ctx, method, path, body, true, false)
	if err == nil && !c.currentGeneration(ctx) {
		err = ErrUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrNotDispatched) {
			lane.uncertain = true
		}
		return raw, err
	}
	if verify(raw) {
		lane.uncertain = false
		return raw, nil
	}
	lane.uncertain = true
	return raw, ErrProtocol
}

func parseDestinationProgress(raw []byte, target string) (DestinationProgress, error) {
	var wire struct {
		Stats *struct {
			Target     json.RawMessage            `json:"targetId"`
			BackingUp  bool                       `json:"backingUp"`
			Validating bool                       `json:"validating"`
			Sync       map[string]json.RawMessage `json:"syncProgressStats"`
			Archive    map[string]json.RawMessage `json:"archiveMaintenanceStats"`
			Cache      map[string]json.RawMessage `json:"cacheMaintenanceStats"`
			NotReady   json.RawMessage            `json:"backupNotReadyCode"`
			OutOfSpace json.RawMessage            `json:"outOfSpace"`
			Rate       json.RawMessage            `json:"sessionCompletedRateInBytesPerSec"`
			FileTodo   map[string]json.RawMessage `json:"fileTodoStats"`
		} `json:"backupStats"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return DestinationProgress{}, &FieldError{Field: "backupStats"}
	}
	out := DestinationProgress{TargetID: target, Sets: []SetProgress{}, RateBytes: "0"}
	s := wire.Stats
	if s == nil {
		// No stats yet for this destination, for example right after start.
		return out, nil
	}
	if id := decimal(s.Target); id != "" && id != target {
		return DestinationProgress{}, &FieldError{Field: "backupStats.targetId"}
	}
	out.BackingUp = s.BackingUp
	out.Validating = s.Validating
	out.OutOfSpace = len(s.OutOfSpace) > 0 && string(s.OutOfSpace) != "null"
	if r := decimalFloor(s.Rate); r != "" {
		out.RateBytes = r
	}
	if code := text(s.NotReady); code != "" && len(code) < 128 {
		out.NotReadyCode = code
	}
	if s.Sync != nil {
		current, total := decimal(s.Sync["current"]), decimal(s.Sync["total"])
		var files bool
		_ = json.Unmarshal(s.Sync["files"], &files)
		if current != "" || total != "" {
			out.Sync = &ProgressCounter{Current: orZero(current), Total: orZero(total), Percent: ratioPercent(current, total), Files: files}
		}
	}
	if m := maintenance(s.Archive, false); m != nil {
		out.Maintenance = m
	} else if m := maintenance(s.Cache, true); m != nil {
		out.Maintenance = m
	}
	for id, rawSet := range s.FileTodo {
		if !validID(id) {
			continue
		}
		var set struct {
			Scanning bool                       `json:"scanning"`
			Selected bool                       `json:"selectedForBackup"`
			Files    json.RawMessage            `json:"numFilesScanned"`
			Bytes    json.RawMessage            `json:"numBytesScanned"`
			RemFiles json.RawMessage            `json:"numRemainingFilesToBackup"`
			RemBytes json.RawMessage            `json:"numRemainingSourceBytes"`
			Last     json.RawMessage            `json:"lastBackupTimestamp"`
			Next     json.RawMessage            `json:"nextBackupTimestamp"`
			Resume   json.RawMessage            `json:"resumeTime"`
			Sending  map[string]json.RawMessage `json:"sendingFileTodo"`
		}
		if json.Unmarshal(rawSet, &set) != nil {
			return DestinationProgress{}, &FieldError{Field: "backupStats.fileTodoStats"}
		}
		item := SetProgress{BackupSetID: id, Scanning: set.Scanning, Selected: set.Selected,
			FilesScanned: orZero(decimal(set.Files)), BytesScanned: orZero(decimal(set.Bytes)),
			RemainingFiles: orZero(decimal(set.RemFiles)), RemainingBytes: orZero(decimal(set.RemBytes)),
			LastBackup: signed(set.Last), NextBackup: signed(set.Next), ResumeTime: signed(set.Resume)}
		if set.Sending != nil {
			var file struct {
				Backup struct {
					Path string `json:"sourcePath"`
				} `json:"backupFile"`
			}
			if json.Unmarshal(set.Sending["fileTodoIndex"], &file) == nil && len(file.Backup.Path) < 4096 && !strings.ContainsAny(file.Backup.Path, "\x00\r\n") {
				item.SendingPath = file.Backup.Path
			}
		}
		out.Sets = append(out.Sets, item)
	}
	return out, nil
}

func maintenance(raw map[string]json.RawMessage, cache bool) *MaintenanceStatus {
	if raw == nil {
		return nil
	}
	var running bool
	if json.Unmarshal(raw["running"], &running) != nil || !running {
		return nil
	}
	m := &MaintenanceStatus{Cache: cache, Percent: -1}
	_ = json.Unmarshal(raw["deepMaintenance"], &m.Deep)
	var verifying bool
	_ = json.Unmarshal(raw["verifyingBlockManifest"], &verifying)
	state := text(raw["reduceState"])
	switch {
	case verifying:
		m.Step = "verifying_blocks"
	case state != "":
		m.Step = strings.ToLower(state)
	default:
		m.Step = "verifying_files"
	}
	// The desktop app reports the step whose counters are active.
	for _, name := range []string{"step5", "step4", "step3", "step2", "step1", "verify"} {
		var stats map[string]json.RawMessage
		if json.Unmarshal(raw[name], &stats) == nil && stats != nil && decimal(stats["total"]) != "" && decimal(stats["total"]) != "0" {
			done := sum(decimal(stats["start"]), decimal(stats["numCompleted"]))
			m.Percent = ratioPercent(done, decimal(stats["total"]))
			break
		}
	}
	return m
}

// FieldError names the native field that did not match. It never carries values.
type FieldError struct{ Field string }

func (e *FieldError) Error() string        { return "NATIVE_PROTOCOL_MISMATCH: " + e.Field }
func (e *FieldError) Is(target error) bool { return target == ErrProtocol }

func decimal(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	s = strings.Trim(s, `"`)
	if s == "" || s == "null" || len(s) > 40 {
		return ""
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return s
}

// decimalFloor accepts a non-negative JSON number with a fraction and keeps the integer part.
func decimalFloor(raw json.RawMessage) string {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	return decimal(json.RawMessage(s))
}

func signed(raw json.RawMessage) string {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if strings.HasPrefix(s, "-") && decimal(json.RawMessage(s[1:])) != "" {
		return "-1"
	}
	if d := decimal(raw); d != "" {
		return d
	}
	return "-1"
}

func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil || strings.ContainsAny(s, "\x00\r\n") {
		return ""
	}
	return s
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func sum(a, b string) string {
	x, y := new(big.Int), new(big.Int)
	x.SetString(orZero(a), 10)
	y.SetString(orZero(b), 10)
	return x.Add(x, y).String()
}

func ratioPercent(current, total string) float64 {
	t, ok := new(big.Int).SetString(orZero(total), 10)
	if !ok || t.Sign() <= 0 {
		return -1
	}
	c, ok := new(big.Int).SetString(orZero(current), 10)
	if !ok {
		return -1
	}
	if c.Cmp(t) > 0 {
		// The engine counts past its estimate; a percentage would be wrong.
		return -1
	}
	if c.Cmp(t) == 0 {
		return 100
	}
	r, _ := new(big.Rat).SetFrac(new(big.Int).Mul(c, big.NewInt(10000)), t).Float64()
	return float64(int64(r)) / 100
}
