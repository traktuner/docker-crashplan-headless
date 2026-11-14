package restore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/traktuner/crashplan-headless/internal/engine"
)

// Archived records remain permanent replay barriers. Only their history index
// entries are removed. No native job or restored file is deleted.
func (s *Service) archived(id string) (record, error) {
	raw, err := s.store.Read("restore-saved-" + id + ".json")
	if err != nil {
		return record{}, err
	}
	var r record
	if json.Unmarshal(raw, &r) != nil || r.RequestID != id || len(r.Digest) != 64 || r.Generation == "" || r.Created.IsZero() {
		return record{}, ErrJournal
	}
	if _, err := hex.DecodeString(r.Digest); err != nil {
		return record{}, ErrJournal
	}
	seen := map[string]bool{}
	for _, child := range r.Children {
		if !requestID.MatchString(child) || child == id || seen[child] {
			return record{}, ErrJournal
		}
		seen[child] = true
	}
	if r.State != "accepted" && r.State != "rejected" && r.State != "acknowledged" {
		return record{}, ErrJournal
	}
	return r, nil
}
func (s *Service) archivedReplay(id, digest string) (record, bool, error) {
	r, err := s.archived(id)
	if errors.Is(err, os.ErrNotExist) {
		return record{}, false, nil
	}
	if err != nil {
		return record{}, false, ErrJournal
	}
	if r.Digest != digest {
		return record{}, false, engine.ErrInvalidRequest
	}
	return r, true, nil
}

func (s *Service) ArchiveOperations(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 || len(ids) > 1024 {
		return 0, engine.ErrInvalidRequest
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if !requestID.MatchString(id) || selected[id] {
			return 0, engine.ErrInvalidRequest
		}
		selected[id] = true
	}
	s.batchMu.Lock()
	defer s.batchMu.Unlock()
	s.startMu.Lock()
	defer s.startMu.Unlock()
	jobs, err := s.native.RestoreJobs(ctx)
	if err != nil {
		return 0, err
	}
	terminal := map[string]bool{}
	for _, job := range jobs {
		terminal[job.ID] = job.Completed || job.Canceled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]record, 0, len(ids))
	for _, r := range s.records {
		if !selected[r.RequestID] {
			for _, child := range r.Children {
				if selected[child] {
					return 0, engine.ErrInvalidRequest
				}
			}
			continue
		}
		if r.State != "accepted" && r.State != "rejected" && r.State != "acknowledged" {
			return 0, engine.ErrIndeterminate
		}
		if done, present := terminal[r.RestoreID]; r.State != "rejected" && r.RestoreID != "" && present && !done {
			return 0, engine.ErrInvalidRequest
		}
		for _, child := range r.Children {
			if !selected[child] {
				return 0, engine.ErrInvalidRequest
			}
		}
		records = append(records, r)
	}
	for id := range selected {
		found := false
		for _, r := range records {
			if r.RequestID == id {
				found = true
				break
			}
		}
		if !found {
			if _, err := s.archived(id); err != nil {
				return 0, engine.ErrInvalidRequest
			}
		}
	}
	// Every permanent record must be durable before the active index changes.
	for _, r := range records {
		if old, err := s.archived(r.RequestID); err == nil {
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(r)
			if string(a) != string(b) {
				return 0, ErrJournal
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, ErrJournal
		}
		raw, _ := json.Marshal(r)
		if s.store.Write("restore-saved-"+r.RequestID+".json", raw) != nil {
			return 0, ErrJournal
		}
	}
	previous := s.records
	next := make([]record, 0, len(previous)-len(records))
	for _, r := range previous {
		if !selected[r.RequestID] {
			next = append(next, r)
		}
	}
	s.records = next
	if err := s.save(); err != nil {
		s.records = previous
		return 0, err
	}
	return len(records), nil
}
