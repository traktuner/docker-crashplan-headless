package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/traktuner/crashplan-headless/internal/engine"
)

type OriginalRequest struct {
	RequestID      string                    `json:"requestId"`
	SourceID       string                    `json:"sourceId"`
	TargetID       string                    `json:"targetId"`
	Existing       string                    `json:"existing"`
	Permissions    string                    `json:"permissions"`
	IncludeDeleted bool                      `json:"includeDeleted"`
	Items          []engine.RestoreSelection `json:"items"`
	Excluded       []engine.RestoreSelection `json:"excluded,omitempty"`
}
type OriginalResult struct {
	Operation Operation   `json:"operation"`
	Jobs      []Operation `json:"jobs"`
	Complete  bool        `json:"complete"`
}

func (s *Service) originalResult(r record) OriginalResult {
	r.Children = append([]string(nil), r.Children...)
	result := OriginalResult{Operation: r.Operation, Jobs: []Operation{}, Complete: r.State == "accepted"}
	for _, id := range r.Children {
		for _, child := range s.records {
			if child.RequestID == id {
				result.Jobs = append(result.Jobs, child.Operation)
				break
			}
		}
	}
	return result
}

// StartOriginal validates all explicit parent directories before the first
// native request. The batch and each child retain independent replay barriers.
func (s *Service) StartOriginal(ctx context.Context, generation string, req OriginalRequest) (result OriginalResult, outcome error) {
	if !requestID.MatchString(req.RequestID) || generation == "" || len(req.Items) == 0 || len(req.Items) > 10000 || len(req.Excluded) > 10000 {
		return result, engine.ErrInvalidRequest
	}
	s.batchMu.Lock()
	defer s.batchMu.Unlock()
	raw, _ := json.Marshal(req)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	s.mu.Lock()
	for _, r := range s.records {
		if r.RequestID == req.RequestID {
			if r.Digest != digest {
				s.mu.Unlock()
				return result, engine.ErrInvalidRequest
			}
			result = s.originalResult(r)
			s.mu.Unlock()
			return result, nil
		}
	}
	if r, found, err := s.archivedReplay(req.RequestID, digest); err != nil {
		s.mu.Unlock()
		return result, err
	} else if found {
		r.Children = append([]string(nil), r.Children...)
		result = OriginalResult{Operation: r.Operation, Jobs: []Operation{}, Complete: r.State == "accepted"}
		for _, id := range r.Children {
			child, err := s.archived(id)
			if err != nil {
				s.mu.Unlock()
				return OriginalResult{}, ErrJournal
			}
			result.Jobs = append(result.Jobs, child.Operation)
		}
		s.mu.Unlock()
		return result, nil
	}
	s.mu.Unlock()
	groups := map[string]*StartRequest{}
	for _, item := range req.Items {
		if !filepath.IsAbs(item.Path) || filepath.Clean(item.Path) != item.Path || item.Path == "/" {
			return result, engine.ErrInvalidRequest
		}
		parent := filepath.Dir(item.Path)
		group := groups[parent]
		if group == nil {
			group = &StartRequest{SourceID: req.SourceID, TargetID: req.TargetID, Existing: req.Existing, Permissions: req.Permissions, IncludeDeleted: req.IncludeDeleted}
			groups[parent] = group
		}
		group.Items = append(group.Items, item)
	}
	for _, excluded := range req.Excluded {
		found := false
		for _, group := range groups {
			for _, item := range group.Items {
				if item.IsFolder && item.BackupSetID == excluded.BackupSetID && strings.HasPrefix(excluded.Path, strings.TrimRight(item.Path, "/")+"/") {
					group.Excluded = append(group.Excluded, excluded)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return result, engine.ErrInvalidRequest
		}
	}
	parents := make([]string, 0, len(groups))
	for parent := range groups {
		parents = append(parents, parent)
	}
	sort.Strings(parents)
	children := make([]string, 0, len(parents))
	for i, parent := range parents {
		group := groups[parent]
		best := ""
		for index, root := range s.roots.Paths() {
			if (parent == root || strings.HasPrefix(parent, root+"/")) && len(root) > len(best) {
				relative, err := filepath.Rel(root, parent)
				if err != nil {
					return result, ErrDestination
				}
				group.Target = Target{RootID: strconv.Itoa(index), Relative: relative}
				best = root
			}
		}
		if best == "" {
			return result, ErrDestination
		}
		destination, err := s.roots.Resolve(group.Target.RootID, group.Target.Relative)
		if err != nil {
			return result, err
		}
		err = destination.Validate()
		writable := destination.Writable()
		destination.Close()
		if err != nil || !writable {
			return result, ErrDestination
		}
		in := engine.RestoreSubmission{SourceID: group.SourceID, TargetID: group.TargetID, Destination: parent, Existing: group.Existing, Permissions: group.Permissions, Items: group.Items, Excluded: group.Excluded}
		if err := in.Validate(); err != nil {
			return result, err
		}
		sum := sha256.Sum256([]byte("original/" + req.RequestID + "/" + strconv.Itoa(i)))
		group.RequestID = hex.EncodeToString(sum[:16])
		children = append(children, group.RequestID)
	}
	s.mu.Lock()
	// A normal submission could have used this ID during directory validation.
	for _, r := range s.records {
		if r.RequestID == req.RequestID {
			s.mu.Unlock()
			return result, engine.ErrInvalidRequest
		}
	}
	if len(s.records)+1+len(children) > 1024 {
		s.mu.Unlock()
		return result, ErrJournalFull
	}
	index := len(s.records)
	batch := record{Operation: Operation{RequestID: req.RequestID, Generation: generation, State: "indeterminate", Created: time.Now().UTC(), Children: children}, Digest: digest}
	s.records = append(s.records, batch)
	if err := s.save(); err != nil {
		s.records = s.records[:index]
		s.mu.Unlock()
		return result, err
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if outcome == nil {
			s.records[index].State = "accepted"
		}
		if err := s.save(); err != nil {
			outcome = errors.Join(engine.ErrIndeterminate, err)
		}
		result = s.originalResult(s.records[index])
	}()
	for _, parent := range parents {
		op, err := s.Start(ctx, generation, *groups[parent])
		if err != nil {
			return result, err
		}
		if op.State != "accepted" {
			return result, engine.ErrIndeterminate
		}
	}
	return result, nil
}
