package restore

import (
	"context"
	"errors"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"testing"
)

func TestArchivedRestoreKeepsItsReplayBarrierAcrossRestart(t *testing.T) {
	service, store, _, adds, _ := serviceFixture(t, false, "completed-job")
	req := restoreRequest()
	if _, err := service.Start(context.Background(), "g1", req); err != nil {
		t.Fatal(err)
	}
	if count, err := service.ArchiveOperations(context.Background(), []string{req.RequestID}); err != nil || count != 1 || len(service.Operations()) != 0 {
		t.Fatal("completed history was not archived", count, err)
	}
	restarted, err := NewService(service.native, service.roots, store, "root")
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.Operations()) != 0 {
		t.Fatal("active history index was not persisted")
	}
	if op, err := restarted.Start(context.Background(), "g2", req); err != nil || op.State != "accepted" || adds.Load() != 1 {
		t.Fatal("archival enabled replay", op, err, adds.Load())
	}
	req.Existing = "SKIP_ALL"
	if _, err := restarted.Start(context.Background(), "g2", req); !errors.Is(err, engine.ErrInvalidRequest) {
		t.Fatal("archived request ID accepted a changed payload", err)
	}
	if err := store.Write("restore-saved-"+req.RequestID+".json", []byte(`{"requestId":"`+req.RequestID+`","requestDigest":"broken"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Start(context.Background(), "g2", req); !errors.Is(err, ErrJournal) || adds.Load() != 1 {
		t.Fatal("corrupt permanent barrier was ignored", err)
	}
}

func TestPendingRestoreCannotBeArchived(t *testing.T) {
	service, _, _, _, _ := serviceFixture(t, false)
	req := restoreRequest()
	if _, err := service.Start(context.Background(), "g1", req); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ArchiveOperations(context.Background(), []string{req.RequestID}); err == nil || len(service.Operations()) != 1 {
		t.Fatal("active restore was archived")
	}
}
