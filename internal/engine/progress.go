package engine

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
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
	// Connected is computer_model.isConnected: self, a child (local folder)
	// or properties.DCODE == CONNECTED.
	Connected bool `json:"connected"`
	// DisconnectCode is properties.DCODE when not connected, omitted when unknown.
	DisconnectCode string `json:"disconnectCode,omitempty"`
	// SystemResumeTime is the epoch millisecond time a system-wide pause ends
	// (BackupStatsSummary.resumeSystemTime), "0" when not paused.
	SystemResumeTime string        `json:"systemResumeTime"`
	Sets             []SetProgress `json:"sets"`
	// Error is set when this destination could not be read; others still are.
	Error string `json:"error,omitempty"`
	// usedBytes is backupStats.remoteManifestSize, for the device list.
	usedBytes string
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
	BackupSetID string `json:"backupSetId"`
	// Scanning is the stock rule scanStats.scanning || fileTodoStats.scanning.
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
	// TotalFiles and TotalBytes are the selection size (scanStats.totalFiles /
	// totalSize); without scan statistics they fall back to the scanned counts.
	TotalFiles string `json:"totalFiles"`
	TotalBytes string `json:"totalBytes"`
	// ScheduledBytes is numScheduledBytes (files waiting in the holding queue).
	ScheduledBytes string `json:"scheduledBytes"`
	// InFlightBytes is the done part of the file being sent or analyzed.
	InFlightBytes string `json:"inFlightBytes"`
	// Percent is the stock completion (0..100, two decimals) or -1 when unknown.
	Percent            float64 `json:"percent"`
	Complete           bool    `json:"complete"`
	CheckingForDeletes bool    `json:"checkingForDeletes"`
	EverBackedUp       bool    `json:"everBackedUp"`
	todo               setTodo
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
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	revision := c.SessionRevision()
	if c.progressRev == revision && time.Since(c.progressAt) < progressFresh && (c.progressCache != nil || c.progressErr != nil) {
		return c.progressCache, c.progressErr
	}
	out, err := c.readProgress(ctx, revision)
	c.progressCache, c.progressErr, c.progressAt, c.progressRev = out, err, time.Now(), revision
	return out, err
}

// progressTargets returns the computer list, reused for targetsFresh within
// one session revision so a steady poll costs one GetBackupStats per target.
func (c *Client) progressTargets(native context.Context, revision uint64) ([]Device, error) {
	if devices, ok := c.cachedTargets(revision); ok {
		return devices, nil
	}
	var devices []Device
	epoch := c.targetsEpochNow()
	_, err := c.legacyProbe(native, "ComputersMessage", "GET", "/v0/GetComputers", nil, true, func(raw []byte) probeResult {
		decoded, e := restoreResponse(raw, nil, "ComputersMessage")
		if e != nil {
			return probeMismatch
		}
		if devices, e = parseDevices(decoded); e != nil {
			return probeMismatch
		}
		// The request has no parameters: any computer list answers it.
		return probeInStep
	})
	if err != nil {
		return nil, err
	}
	c.storeTargets(revision, epoch, devices)
	return devices, nil
}

func (c *Client) readProgress(ctx context.Context, revision uint64) ([]DestinationProgress, error) {
	// A closed page must not abandon a native request halfway: the engine
	// answer is still read, so the response lane stays in step.
	native, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	devices, err := c.progressTargets(native, revision)
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
		progress.Connected = device.Connected
		progress.DisconnectCode = device.DisconnectCode
		out = append(out, progress)
	}
	if len(out) == 0 {
		return out, nil
	}
	// The system pause applies to every destination. Without it the status
	// would claim "not paused", so a failed read fails the whole reading.
	resume, err := c.systemResumeTime(native)
	if err != nil {
		return nil, err
	}
	scans := map[string]setScan{}
	for i := range out {
		out[i].SystemResumeTime = resume
		for j := range out[i].Sets {
			set := &out[i].Sets[j]
			scan, ok := scans[set.BackupSetID]
			if !ok {
				scan = c.setScanStats(native, set.BackupSetID)
				scans[set.BackupSetID] = scan
			}
			finishSet(set, scan)
		}
	}
	return out, nil
}

// finishSet applies the stock rules once the scan statistics are known.
func finishSet(set *SetProgress, scan setScan) {
	t := set.todo
	set.Scanning = t.scanning || (scan.present && scan.scanning)
	set.CheckingForDeletes = scan.present && scan.checkingForDeletes
	if scan.present {
		set.TotalFiles, set.TotalBytes = nonNegative(scan.totalFiles), nonNegative(scan.totalSize)
	} else {
		set.TotalFiles, set.TotalBytes = set.FilesScanned, set.BytesScanned
	}
	set.InFlightBytes = nonNegative(t.inFlightBytes)
	set.Percent = completionPercent(t, scan)
	set.Complete = backupComplete(t, scan, true)
}

// systemResumeTime reads BackupStatsSummary.resumeSystemTime and returns it
// only while it lies in the future (stock getRemainingSystemResumeInMs > 0).
func (c *Client) systemResumeTime(ctx context.Context) (string, error) {
	raw, err := c.legacyRead(ctx, "BackupStatsSummaryMessage", "GET", "/v0/GetBackupStatsSummary", nil, anyAnswer)
	raw, err = restoreResponse(raw, err, "BackupStatsSummaryMessage")
	if err != nil {
		return "", err
	}
	return parseSystemResume(raw, time.Now().UnixMilli())
}

func parseSystemResume(raw []byte, now int64) (string, error) {
	var wire struct {
		Stats *struct {
			Resume json.RawMessage `json:"resumeSystemTime"`
		} `json:"stats"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Stats == nil {
		return "", &FieldError{Field: "stats"}
	}
	resume := bigOf(wire.Stats.Resume)
	if len(wire.Stats.Resume) > 0 && string(wire.Stats.Resume) != "null" && resume == nil {
		return "", &FieldError{Field: "stats.resumeSystemTime"}
	}
	if resume == nil || resume.Cmp(big.NewInt(now)) <= 0 {
		return "0", nil
	}
	return resume.String(), nil
}

// setScanStats reads GetScanStats for one backup set. Only an answer that
// echoes the set is used; anything else counts as "no scan statistics", which
// makes the percentage unknown instead of borrowing another set's numbers.
func (c *Client) setScanStats(ctx context.Context, setID string) setScan {
	body, _ := json.Marshal(struct {
		ID string `json:"backupSetId"`
	}{setID})
	var parsed setScan
	_, err := c.legacyRead(ctx, "ScanStatsMessage", "PUT", "/v0/GetScanStats", body, func(decoded []byte) probeResult {
		scan, echo, ok := parseScanStats(decoded)
		switch {
		case !ok:
			return probeMismatch
		case echo == setID:
			parsed = scan
			return probeInStep
		case echo != "":
			return probeMismatch
		default:
			// No statistics yet: an empty answer proves nothing either way.
			return probeUnknown
		}
	})
	if err != nil {
		return setScan{}
	}
	return parsed
}

// parseScanStats reads the flattened ScanStatsMessage. echo is the answered
// backup set; ok is false when the answer is not a scan statistics object.
func parseScanStats(raw []byte) (setScan, string, bool) {
	var wire map[string]json.RawMessage
	if json.Unmarshal(raw, &wire) != nil || wire == nil {
		return setScan{}, "", false
	}
	scanning, has := wire["scanning"]
	if !has || string(scanning) == "null" {
		return setScan{}, "", true
	}
	var s setScan
	if json.Unmarshal(scanning, &s.scanning) != nil {
		return setScan{}, "", false
	}
	echo := decimal(wire["backupSetId"])
	if echo == "" || !validID(echo) {
		return setScan{}, "", false
	}
	s.present = true
	s.checkingForDeletes = jsonTrue(wire["checkingForDeletes"])
	s.totalFiles, s.totalSize = bigOf(wire["totalFiles"]), bigOf(wire["totalSize"])
	return s, echo, true
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
	var parseErr error
	_, err := c.legacyProbe(ctx, "BackupStatsMessage", "PUT", "/v0/GetBackupStats", body, true, func(raw []byte) probeResult {
		decoded, e := restoreResponse(raw, nil, "BackupStatsMessage")
		if e != nil {
			parseErr = e
			return probeMismatch
		}
		if parsed, parseErr = parseDestinationProgress(decoded, target); parseErr != nil {
			return probeMismatch
		}
		// Only an answer that names this target proves the lane is in step;
		// an empty one ("no stats yet") neither proves nor disproves it.
		var named struct {
			Stats *struct {
				Target json.RawMessage `json:"targetId"`
			} `json:"backupStats"`
		}
		if json.Unmarshal(decoded, &named) == nil && named.Stats != nil && decimal(named.Stats.Target) == target {
			return probeInStep
		}
		return probeUnknown
	})
	if parseErr != nil {
		return DestinationProgress{}, parseErr
	}
	if err != nil {
		return DestinationProgress{}, err
	}
	return parsed, nil
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
			Manifest   json.RawMessage            `json:"remoteManifestSize"`
			FileTodo   map[string]json.RawMessage `json:"fileTodoStats"`
		} `json:"backupStats"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return DestinationProgress{}, &FieldError{Field: "backupStats"}
	}
	out := DestinationProgress{TargetID: target, Sets: []SetProgress{}, RateBytes: "0", SystemResumeTime: "0"}
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
	out.usedBytes = decimal(s.Manifest)
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
	out.Maintenance = maintenanceStatus(s.Archive, s.Cache)
	for id, rawSet := range s.FileTodo {
		if !validID(id) {
			continue
		}
		var set struct {
			Scanning  bool                       `json:"scanning"`
			Selected  bool                       `json:"selectedForBackup"`
			Files     json.RawMessage            `json:"numFilesScanned"`
			Bytes     json.RawMessage            `json:"numBytesScanned"`
			RemFiles  json.RawMessage            `json:"numRemainingFilesToBackup"`
			RemBytes  json.RawMessage            `json:"numRemainingSourceBytes"`
			SchFiles  json.RawMessage            `json:"numScheduledFiles"`
			SchBytes  json.RawMessage            `json:"numScheduledBytes"`
			Last      json.RawMessage            `json:"lastBackupTimestamp"`
			Next      json.RawMessage            `json:"nextBackupTimestamp"`
			Resume    json.RawMessage            `json:"resumeTime"`
			Sending   map[string]json.RawMessage `json:"sendingFileTodo"`
			Analyzing map[string]json.RawMessage `json:"analyzingFileTodo"`
		}
		if json.Unmarshal(rawSet, &set) != nil {
			return DestinationProgress{}, &FieldError{Field: "backupStats.fileTodoStats"}
		}
		item := SetProgress{BackupSetID: id, Scanning: set.Scanning, Selected: set.Selected,
			FilesScanned: orZero(decimal(set.Files)), BytesScanned: orZero(decimal(set.Bytes)),
			RemainingFiles: orZero(decimal(set.RemFiles)), RemainingBytes: orZero(decimal(set.RemBytes)),
			LastBackup: signed(set.Last), NextBackup: signed(set.Next), ResumeTime: signed(set.Resume),
			ScheduledBytes: nonNegative(bigOf(set.SchBytes))}
		item.EverBackedUp = item.LastBackup != "-1" && item.LastBackup != "0"
		item.todo = setTodo{scanning: set.Scanning, selected: set.Selected,
			filesScanned: bigOf(set.Files), bytesScanned: bigOf(set.Bytes),
			remainingFiles: bigOf(set.RemFiles), remainingBytes: bigOf(set.RemBytes),
			scheduledFiles: bigOf(set.SchFiles), inFlightBytes: inFlightBytes(set.Sending, set.Analyzing)}
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
		// Until scan statistics are applied, the set has the stock "no basis" values.
		finishSet(&item, setScan{})
		out.Sets = append(out.Sets, item)
	}
	sort.Slice(out.Sets, func(i, j int) bool { return lessID(out.Sets[i].BackupSetID, out.Sets[j].BackupSetID) })
	return out, nil
}

// lessID orders exact decimal IDs numerically.
func lessID(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
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
