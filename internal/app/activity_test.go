package app

import (
	"errors"
	"testing"

	"github.com/traktuner/crashplan-headless/internal/engine"
)

func TestHistoryLimitParsing(t *testing.T) {
	for raw, want := range map[string]int{"": 0, "1": 1, "5": 5, "50000": 50000} {
		if got, err := historyLimit(raw); err != nil || got != want {
			t.Errorf("%q: got %d %v", raw, got, err)
		}
	}
	for _, raw := range []string{"0", "-1", "50001", "05", "+5", "five", "1e3"} {
		if _, err := historyLimit(raw); !errors.Is(err, engine.ErrInvalidRequest) {
			t.Errorf("%q accepted", raw)
		}
	}
}

func TestBackupControlInputAcceptsDestinationIDAndGUID(t *testing.T) {
	got, err := backupControlInput{BackupSetID: "9007199254740993", DestinationID: "9007199254740995"}.control()
	if err != nil || got != (engine.BackupControl{BackupSetID: "9007199254740993", DestinationID: "9007199254740995"}) {
		t.Fatal(got, err)
	}
	got, err = backupControlInput{DestinationGUID: "8", DurationMinutes: "60"}.control()
	if err != nil || got != (engine.BackupControl{DestinationID: "8", DurationMinutes: "60"}) {
		t.Fatal(got, err)
	}
	if _, err := (backupControlInput{DestinationID: "8", DestinationGUID: "9"}).control(); !errors.Is(err, engine.ErrInvalidRequest) {
		t.Fatal("conflicting destinations accepted", err)
	}
}
