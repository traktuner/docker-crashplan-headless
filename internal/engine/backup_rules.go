package engine

import (
	"encoding/json"
	"math/big"
	"strings"
)

// The rules in this file mirror the stock 12.1.1 desktop app so the web UI
// reports the same completion and percentage:
//   - shared/utils/cp_rule.js (isBackupComplete, hasBackupStarted,
//     getBackupCompletionRatio, getBackupCompletedSize)
//   - shared/models/file_todo_stats_model.js (getCompletionRatio,
//     getNumRemainingSourceBytesAdjustedWithTodo)
//   - shared/models/archive_maintenance_stats_model.js and step_stats_model.js
//     (getCurrentStepStats, getProgressPercent)
//   - shared/utils/backup_state.js (OFF/QUEUED hide the maintenance percent)

// setTodo is the part of one fileTodoStats entry the rules need.
type setTodo struct {
	scanning       bool
	selected       bool
	filesScanned   *big.Int
	bytesScanned   *big.Int
	remainingFiles *big.Int
	remainingBytes *big.Int
	scheduledFiles *big.Int
	inFlightBytes  *big.Int
}

// setScan is the per-set answer of GetScanStats. present is false when the
// engine has no scan statistics for the set (or the read failed).
type setScan struct {
	present            bool
	scanning           bool
	checkingForDeletes bool
	totalFiles         *big.Int
	totalSize          *big.Int
}

// bigOf reads a JSON integer given as number or string, with an optional sign.
// It returns nil when the field is absent or not an integer.
func bigOf(raw json.RawMessage) *big.Int {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" || len(s) > 41 {
		return nil
	}
	digits := strings.TrimPrefix(s, "-")
	if digits == "" {
		return nil
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return nil
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil
	}
	return v
}

func zeroIfNil(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return v
}

// nonNegative renders a count as an exact decimal string, never below zero.
func nonNegative(v *big.Int) string {
	if v == nil || v.Sign() < 0 {
		return "0"
	}
	return v.String()
}

// inFlightBytes is getNumRemainingSourceBytesAdjustedWithTodo's correction:
// the bytes already done of the file being sent, or else of the file being
// analyzed, while that file is not finished.
func inFlightBytes(sending, analyzing map[string]json.RawMessage) *big.Int {
	if sending != nil && !jsonTrue(sending["doneSending"]) {
		return zeroIfNil(bigOf(sending["numSourceBytesCompleted"]))
	}
	if analyzing != nil && !jsonTrue(analyzing["doneAnalyzing"]) {
		return zeroIfNil(bigOf(analyzing["numSourceBytesCompleted"]))
	}
	return new(big.Int)
}

func jsonTrue(raw json.RawMessage) bool {
	var v bool
	return json.Unmarshal(raw, &v) == nil && v
}

// adjustedRemainingBytes is numRemainingSourceBytes minus the in-flight bytes.
func (t setTodo) adjustedRemainingBytes() *big.Int {
	return new(big.Int).Sub(zeroIfNil(t.remainingBytes), zeroIfNil(t.inFlightBytes))
}

// hasBackupStarted: fileTodoStats.getCompletionRatio() > 0 || selectedForBackup.
// getCompletionRatio (without todo bytes) is (scanned - remaining) / scanned,
// 1 when the difference exceeds scanned, and BigMath.getRatio's 1 for 0/0.
func (t setTodo) hasBackupStarted() bool {
	if t.selected {
		return true
	}
	scanned := zeroIfNil(t.bytesScanned)
	done := new(big.Int).Sub(scanned, zeroIfNil(t.remainingBytes))
	switch {
	case done.Cmp(scanned) > 0:
		return true
	case scanned.Sign() > 0:
		return done.Sign() > 0
	default:
		return done.Sign() == 0 && scanned.Sign() == 0
	}
}

// backupComplete is CpRule.isBackupComplete.
func backupComplete(t setTodo, scan setScan, includeScheduled bool) bool {
	if !scan.present || scan.scanning {
		return false
	}
	if zeroIfNil(t.filesScanned).Sign() == 0 || t.scanning {
		return false
	}
	remaining := new(big.Int).Set(zeroIfNil(t.remainingFiles))
	if includeScheduled {
		remaining.Add(remaining, zeroIfNil(t.scheduledFiles))
	}
	return remaining.Sign() == 0 && t.hasBackupStarted()
}

// completionPercent is CpRule.getBackupCompletionRatio as 0..100 with two
// decimals (rounded down, so an unfinished set never shows 100). It is -1 when
// the stock app has no basis for a percentage: no scan statistics, or a
// selection size of zero for a set that is not complete.
func completionPercent(t setTodo, scan setScan) float64 {
	if !scan.present {
		return -1
	}
	if scan.scanning || t.scanning {
		return 0
	}
	if backupComplete(t, scan, false) {
		return 100
	}
	total := zeroIfNil(scan.totalSize)
	if total.Sign() <= 0 {
		return -1
	}
	completed := new(big.Int)
	if t.hasBackupStarted() {
		completed.Sub(total, t.adjustedRemainingBytes())
		if completed.Sign() < 0 {
			completed.SetInt64(0)
		}
	}
	// Very small files: sizes agree although files remain (stock caps at 0.99).
	if total.Cmp(completed) < 0 {
		return 99
	}
	if completed.Sign() == 0 {
		return 0
	}
	// floor(completed * 10000 / total) / 100, with the stock minimum of 0.0001.
	hundredths := new(big.Int).Quo(new(big.Int).Mul(completed, big.NewInt(10000)), total)
	if hundredths.Sign() == 0 {
		return 0.01
	}
	return float64(hundredths.Int64()) / 100
}

// maintenanceStatus picks the stats the stock status line shows: archive
// maintenance when running and not flagged as cache, else cache maintenance
// when running and flagged as cache (backup_stats_model.js isMaintaining /
// isMaintainingCache).
func maintenanceStatus(archive, cache map[string]json.RawMessage) *MaintenanceStatus {
	if archive != nil && jsonTrue(archive["running"]) && !jsonTrue(archive["cache"]) {
		return maintenanceOf(archive)
	}
	if cache != nil && jsonTrue(cache["running"]) && jsonTrue(cache["cache"]) {
		return maintenanceOf(cache)
	}
	return nil
}

func maintenanceOf(raw map[string]json.RawMessage) *MaintenanceStatus {
	m := &MaintenanceStatus{Cache: jsonTrue(raw["cache"]), Deep: jsonTrue(raw["deepMaintenance"]), Percent: -1}
	verifying := jsonTrue(raw["verifyingBlockManifest"])
	state := text(raw["reduceState"])
	if len(state) > 64 {
		state = ""
	}
	progress := true
	switch {
	case verifying:
		m.Step = "verifying_blocks"
	case state != "":
		m.Step = strings.ToLower(state)
		if state == "OFF" || state == "QUEUED" {
			progress = false
		}
	default:
		m.Step = "verifying_files"
	}
	if !progress {
		return m
	}
	step := "step1"
	switch state {
	case "STEP_1_PRUNE_FILES":
		if verifying {
			step = "verify"
		}
	case "STEP_2_PRUNE_BLOCKS":
		step = "step2"
	case "STEP_3_COMPACT_FILES":
		step = "step3"
	case "STEP_4_COMPACT_BLOCKS":
		step = "step4"
	case "STEP_5_VERIFY_FILES":
		step = "step5"
	}
	var stats map[string]json.RawMessage
	if json.Unmarshal(raw[step], &stats) == nil && stats != nil {
		m.Percent = stepPercent(stats)
	}
	return m
}

// stepPercent is StepStats.getProgressPercent: (start + numCompleted) / total,
// capped at 100 %, and BigMath.getRatio's 100 % for 0/0.
func stepPercent(stats map[string]json.RawMessage) float64 {
	total := bigOf(stats["total"])
	if total == nil || total.Sign() < 0 {
		return -1
	}
	done := new(big.Int).Add(zeroIfNil(bigOf(stats["start"])), zeroIfNil(bigOf(stats["numCompleted"])))
	if done.Cmp(total) >= 0 {
		return 100
	}
	if total.Sign() == 0 || done.Sign() <= 0 {
		return 0
	}
	hundredths := new(big.Int).Quo(new(big.Int).Mul(done, big.NewInt(10000)), total)
	return float64(hundredths.Int64()) / 100
}
