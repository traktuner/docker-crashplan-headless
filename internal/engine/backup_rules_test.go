package engine

import (
	"encoding/json"
	"math/big"
	"testing"
)

func n(v int64) *big.Int { return big.NewInt(v) }

func scanOf(total int64) setScan {
	return setScan{present: true, totalFiles: n(10), totalSize: n(total)}
}

func TestCompletionPercentFollowsStockRules(t *testing.T) {
	started := setTodo{selected: true, filesScanned: n(10), bytesScanned: n(1000), remainingFiles: n(4), remainingBytes: n(400)}
	cases := []struct {
		name string
		todo setTodo
		scan setScan
		want float64
	}{
		{"no scan statistics", started, setScan{}, -1},
		{"set scan running", started, setScan{present: true, scanning: true, totalSize: n(1000)}, 0},
		{"destination scanning", setTodo{scanning: true, selected: true, filesScanned: n(10), remainingBytes: n(400)}, scanOf(1000), 0},
		{"empty selection", started, scanOf(0), -1},
		// (total - remaining) / total uses the scan total, not numBytesScanned.
		{"scan total is the base", started, scanOf(2000), 80},
		{"two decimals rounded down", setTodo{selected: true, filesScanned: n(3), remainingFiles: n(1), remainingBytes: n(1)}, scanOf(3), 66.66},
		// The file in flight counts as done (getNumRemainingSourceBytesAdjustedWithTodo).
		{"in-flight bytes", setTodo{selected: true, filesScanned: n(10), remainingFiles: n(1), remainingBytes: n(400), inFlightBytes: n(200)}, scanOf(1000), 80},
		{"stock minimum", setTodo{selected: true, filesScanned: n(10), remainingFiles: n(1), remainingBytes: n(999999)}, scanOf(1000000), 0.01},
		// Adjusted remaining below zero: completed exceeds total, stock shows 0.99.
		{"sizes disagree", setTodo{selected: true, filesScanned: n(10), remainingFiles: n(1), remainingBytes: n(10), inFlightBytes: n(20)}, scanOf(1000), 99},
		{"not started", setTodo{filesScanned: n(10), bytesScanned: n(100), remainingFiles: n(10), remainingBytes: n(100)}, scanOf(100), 0},
		{"complete", setTodo{filesScanned: n(10), bytesScanned: n(100), remainingFiles: n(0), remainingBytes: n(0)}, scanOf(100), 100},
		// Scheduled files do not lower the percent (stock passes includeScheduled=false).
		{"scheduled only", setTodo{filesScanned: n(10), bytesScanned: n(100), scheduledFiles: n(3)}, scanOf(100), 100},
	}
	for _, tc := range cases {
		if got := completionPercent(tc.todo, tc.scan); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestBackupCompleteFollowsStockRules(t *testing.T) {
	done := setTodo{filesScanned: n(10), bytesScanned: n(100), remainingFiles: n(0), remainingBytes: n(0)}
	if !backupComplete(done, scanOf(100), true) {
		t.Fatal("finished set not complete")
	}
	cases := map[string]struct {
		todo setTodo
		scan setScan
	}{
		"no scan statistics": {done, setScan{}},
		"scan running":       {done, setScan{present: true, scanning: true}},
		"never scanned":      {setTodo{bytesScanned: n(100)}, scanOf(100)},
		"destination scan":   {setTodo{scanning: true, filesScanned: n(10), bytesScanned: n(100)}, scanOf(100)},
		"files remaining":    {setTodo{filesScanned: n(10), bytesScanned: n(100), remainingFiles: n(1)}, scanOf(100)},
		"files scheduled":    {setTodo{filesScanned: n(10), bytesScanned: n(100), scheduledFiles: n(1)}, scanOf(100)},
		// Not selected and nothing done (remaining == scanned): not started.
		"not started": {setTodo{filesScanned: n(10), bytesScanned: n(100), remainingBytes: n(100)}, scanOf(100)},
	}
	for name, tc := range cases {
		if backupComplete(tc.todo, tc.scan, true) {
			t.Errorf("%s: reported complete", name)
		}
	}
}

func TestHasBackupStartedMatchesCompletionRatio(t *testing.T) {
	cases := []struct {
		todo setTodo
		want bool
	}{
		{setTodo{selected: true}, true},
		{setTodo{bytesScanned: n(100), remainingBytes: n(40)}, true},
		{setTodo{bytesScanned: n(100), remainingBytes: n(100)}, false},
		// BigMath.getRatio(0, 0) is 1.
		{setTodo{}, true},
		{setTodo{remainingBytes: n(5)}, false},
		// Negative remaining: done exceeds scanned, ratio 1.
		{setTodo{bytesScanned: n(100), remainingBytes: n(-1)}, true},
	}
	for i, tc := range cases {
		if got := tc.todo.hasBackupStarted(); got != tc.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}

func TestInFlightBytes(t *testing.T) {
	raw := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got := inFlightBytes(raw(`{"doneSending":false,"numSourceBytesCompleted":"9007199254740993"}`), nil); got.String() != "9007199254740993" {
		t.Fatal(got)
	}
	if got := inFlightBytes(raw(`{"doneSending":true,"numSourceBytesCompleted":5}`), raw(`{"doneAnalyzing":false,"numSourceBytesCompleted":7}`)); got.String() != "7" {
		t.Fatal("analyzing todo not used", got)
	}
	if got := inFlightBytes(raw(`{"doneSending":true,"numSourceBytesCompleted":5}`), raw(`{"doneAnalyzing":true,"numSourceBytesCompleted":7}`)); got.Sign() != 0 {
		t.Fatal(got)
	}
	if got := inFlightBytes(nil, nil); got.Sign() != 0 {
		t.Fatal(got)
	}
}

func TestMaintenanceStepFollowsReduceState(t *testing.T) {
	parse := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	steps := `"step1":{"total":10,"start":0,"numCompleted":1},"step2":{"total":4,"start":1,"numCompleted":1},"step3":{"total":0,"numCompleted":0},"step5":{"total":3,"numCompleted":9},"verify":{"total":8,"numCompleted":2}`
	cases := []struct {
		raw     string
		step    string
		percent float64
	}{
		{`{"running":true,"reduceState":"STEP_2_PRUNE_BLOCKS",` + steps + `}`, "step_2_prune_blocks", 50},
		{`{"running":true,"reduceState":"STEP_1_PRUNE_FILES",` + steps + `}`, "step_1_prune_files", 10},
		{`{"running":true,"reduceState":"STEP_1_PRUNE_FILES","verifyingBlockManifest":true,` + steps + `}`, "verifying_blocks", 25},
		// Counting past the total is capped at 100 %.
		{`{"running":true,"reduceState":"STEP_5_VERIFY_FILES",` + steps + `}`, "step_5_verify_files", 100},
		// 0 of 0 is 100 % in BigMath.getRatio.
		{`{"running":true,"reduceState":"STEP_3_COMPACT_FILES",` + steps + `}`, "step_3_compact_files", 100},
		// No stats for the step: unknown.
		{`{"running":true,"reduceState":"STEP_4_COMPACT_BLOCKS",` + steps + `}`, "step_4_compact_blocks", -1},
		// OFF and QUEUED hide the percent.
		{`{"running":true,"reduceState":"QUEUED",` + steps + `}`, "queued", -1},
		{`{"running":true,"reduceState":"OFF",` + steps + `}`, "off", -1},
		// No state: step 1 counters.
		{`{"running":true,` + steps + `}`, "verifying_files", 10},
	}
	for _, tc := range cases {
		m := maintenanceStatus(parse(tc.raw), nil)
		if m == nil || m.Step != tc.step || m.Percent != tc.percent || m.Cache {
			t.Errorf("%s: got %+v", tc.raw, m)
		}
	}
	// Archive stats flagged as cache are not archive maintenance; cache stats
	// count only when flagged as cache.
	if m := maintenanceStatus(parse(`{"running":true,"cache":true}`), parse(`{"running":true,"cache":false}`)); m != nil {
		t.Fatalf("got %+v", m)
	}
	if m := maintenanceStatus(parse(`{"running":false}`), parse(`{"running":true,"cache":true,"deepMaintenance":true,"reduceState":"STEP_2_PRUNE_BLOCKS","step2":{"total":2,"numCompleted":1}}`)); m == nil || !m.Cache || !m.Deep || m.Percent != 50 {
		t.Fatalf("cache maintenance %+v", m)
	}
}

func TestParseSystemResume(t *testing.T) {
	const now = 1700000000000
	cases := map[string]string{
		`{"stats":{"resumeSystemTime":1700000060000}}`:   "1700000060000",
		`{"stats":{"resumeSystemTime":"1700000060000"}}`: "1700000060000",
		`{"stats":{"resumeSystemTime":1690000000000}}`:   "0",
		`{"stats":{"resumeSystemTime":0}}`:               "0",
		`{"stats":{}}`:                                   "0",
	}
	for raw, want := range cases {
		got, err := parseSystemResume([]byte(raw), now)
		if err != nil || got != want {
			t.Errorf("%s: got %q %v", raw, got, err)
		}
	}
	for _, raw := range []string{`{}`, `{"stats":{"resumeSystemTime":"soon"}}`} {
		if _, err := parseSystemResume([]byte(raw), now); err == nil {
			t.Errorf("%s: want error", raw)
		}
	}
}

func TestParseScanStats(t *testing.T) {
	s, echo, ok := parseScanStats([]byte(`{"backupSetId":"9007199254740993","scanning":false,"checkingForDeletes":true,"totalFiles":12,"totalSize":"9007199254740995"}`))
	if !ok || echo != "9007199254740993" || !s.present || !s.checkingForDeletes || s.totalFiles.String() != "12" || s.totalSize.String() != "9007199254740995" {
		t.Fatalf("%+v %q %v", s, echo, ok)
	}
	if s, echo, ok := parseScanStats([]byte(`{}`)); !ok || echo != "" || s.present {
		t.Fatal("empty answer must mean no statistics")
	}
	if _, _, ok := parseScanStats([]byte(`{"scanning":true}`)); ok {
		t.Fatal("statistics without a backup set accepted")
	}
	if _, _, ok := parseScanStats([]byte(`[]`)); ok {
		t.Fatal("non-object accepted")
	}
}
