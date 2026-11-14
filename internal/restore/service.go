package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/traktuner/crashplan-headless/internal/engine"
	"github.com/traktuner/crashplan-headless/internal/statefile"
)

var ErrJournal = errors.New("RESTORE_JOURNAL_UNAVAILABLE")
var ErrJournalFull = errors.New("RESTORE_JOURNAL_FULL")
var requestID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Target struct {
	RootID   string `json:"rootId"`
	Relative string `json:"relative"`
}
type StartRequest struct {
	RequestID      string                    `json:"requestId"`
	SourceID       string                    `json:"sourceId"`
	TargetID       string                    `json:"targetId"`
	Target         Target                    `json:"target"`
	Existing       string                    `json:"existing"`
	Permissions    string                    `json:"permissions"`
	IncludeDeleted bool                      `json:"includeDeleted"`
	Items          []engine.RestoreSelection `json:"items"`
	Excluded       []engine.RestoreSelection `json:"excluded,omitempty"`
}
type Operation struct {
	RequestID  string    `json:"requestId"`
	Generation string    `json:"generation"`
	RestoreID  string    `json:"restoreId,omitempty"`
	Path       string    `json:"restorePath"`
	State      string    `json:"state"`
	Created    time.Time `json:"created"`
	Children   []string  `json:"children,omitempty"`
}
type record struct {
	Operation
	Digest string `json:"requestDigest"`
}
type Job struct {
	engine.RestoreJob
	Path       string `json:"restorePath"`
	Imported   bool   `json:"imported"`
	Admissible bool   `json:"admissible"`
}
type RootInfo struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Writable bool   `json:"writable"`
}
type Service struct {
	mu      sync.Mutex
	startMu sync.Mutex
	batchMu sync.Mutex
	native  *engine.Client
	roots   *Destinations
	store   *statefile.Store
	user    string
	records []record
	statsMu sync.Mutex
	stats   map[string]observedStats
}

// observedStats is the newest live statistics event of one restore job.
type observedStats struct {
	stats engine.RestoreStats
	at    time.Time
}

// statsFresh bounds how long a statistics event describes a job. The engine
// sends them every few seconds while a restore runs.
const statsFresh = 2 * time.Minute

// ResetRestoreStats forgets live statistics when an engine generation begins.
func (s *Service) ResetRestoreStats() {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.stats = nil
}

// ObserveRestoreStats keeps the newest RestoreStatsMessage or
// RestoreStoppedMessage statistics per job. Anything malformed is ignored.
func (s *Service) ObserveRestoreStats(raw []byte) {
	stats, ok := engine.ParseRestoreStats(raw)
	if !ok {
		return
	}
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if s.stats == nil {
		s.stats = make(map[string]observedStats)
	}
	now := time.Now()
	for id, old := range s.stats {
		if now.Sub(old.at) > statsFresh {
			delete(s.stats, id)
		}
	}
	if len(s.stats) >= 1024 {
		return
	}
	s.stats[stats.RestoreID] = observedStats{stats: stats, at: now}
}

func (s *Service) liveStats(id string, now time.Time) *engine.RestoreStats {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	observed, ok := s.stats[id]
	if !ok || now.Sub(observed.at) > statsFresh {
		return nil
	}
	stats := observed.stats
	return &stats
}

func NewService(native *engine.Client, roots *Destinations, store *statefile.Store, user string) (*Service, error) {
	if native == nil || roots == nil || store == nil || user == "" {
		return nil, ErrJournal
	}
	s := &Service{native: native, roots: roots, store: store, user: user, records: []record{}}
	raw, err := store.Read("restore-operations.json")
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil || json.Unmarshal(raw, &s.records) != nil || s.records == nil || len(s.records) > 1024 {
		return nil, ErrJournal
	}
	seen := map[string]bool{}
	for _, r := range s.records {
		if !requestID.MatchString(r.RequestID) || seen[r.RequestID] || r.Generation == "" || r.Created.IsZero() || len(r.Digest) != 64 {
			return nil, ErrJournal
		}
		if _, err := hex.DecodeString(r.Digest); err != nil {
			return nil, ErrJournal
		}
		switch r.State {
		case "accepted", "rejected", "indeterminate", "acknowledged":
		default:
			return nil, ErrJournal
		}
		seen[r.RequestID] = true
		childIDs := map[string]bool{}
		for _, child := range r.Children {
			if !requestID.MatchString(child) || child == r.RequestID || childIDs[child] {
				return nil, ErrJournal
			}
			childIDs[child] = true
		}
	}
	return s, nil
}
func (s *Service) save() error {
	raw, err := json.Marshal(s.records)
	if err != nil || s.store.Write("restore-operations.json", raw) != nil {
		return ErrJournal
	}
	return nil
}
func (s *Service) Operations() []Operation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Operation, len(s.records))
	for i, r := range s.records {
		out[i] = r.Operation
		out[i].Children = slices.Clone(r.Children)
	}
	return out
}
func (s *Service) Roots() []RootInfo {
	paths := s.roots.Paths()
	out := make([]RootInfo, 0, len(paths))
	for i, path := range paths {
		id := strconv.Itoa(i)
		root := RootInfo{ID: id, Path: path}
		if d, err := s.roots.Resolve(id, ""); err == nil {
			root.Writable = d.Validate() == nil && d.Writable()
			d.Close()
		}
		out = append(out, root)
	}
	return out
}

// Start runs inside the generation owner. Its request ID is a permanent replay
// barrier, including after a lost HTTP response or a daemon restart.
func (s *Service) Start(ctx context.Context, generation string, req StartRequest) (op Operation, result error) {
	if !requestID.MatchString(req.RequestID) || generation == "" {
		return op, engine.ErrInvalidRequest
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return op, engine.ErrInvalidRequest
	}
	digest := sha256.Sum256(raw)
	digestText := hex.EncodeToString(digest[:])
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	for _, r := range s.records {
		if r.RequestID == req.RequestID {
			if r.Digest != digestText {
				s.mu.Unlock()
				return op, engine.ErrInvalidRequest
			}
			s.mu.Unlock()
			return r.Operation, nil
		}
	}
	if r, found, err := s.archivedReplay(req.RequestID, digestText); err != nil {
		s.mu.Unlock()
		return op, err
	} else if found {
		s.mu.Unlock()
		return r.Operation, nil
	}
	if len(s.records) >= 1024 {
		s.mu.Unlock()
		return op, ErrJournalFull
	}
	s.mu.Unlock()
	destination, err := s.roots.Resolve(req.Target.RootID, req.Target.Relative)
	if err != nil {
		return op, err
	}
	defer destination.Close()
	in := engine.RestoreSubmission{SourceID: req.SourceID, TargetID: req.TargetID, Destination: destination.Path(), Existing: req.Existing, Permissions: req.Permissions, IncludeDeleted: req.IncludeDeleted, Items: req.Items, Excluded: req.Excluded}
	if err := in.Validate(); err != nil {
		return op, err
	}
	if destination.Validate() != nil || !destination.Writable() {
		return op, ErrDestination
	}
	credentials, err := s.native.RestoreContext(ctx, req.SourceID)
	if err != nil {
		return op, err
	}
	if destination.Validate() != nil || !destination.Writable() {
		return op, ErrDestination
	}
	op = Operation{RequestID: req.RequestID, Generation: generation, Path: destination.Path(), State: "indeterminate", Created: time.Now().UTC()}
	s.mu.Lock()
	index := len(s.records)
	s.records = append(s.records, record{Operation: op, Digest: digestText})
	if err := s.save(); err != nil {
		s.records = s.records[:index]
		s.mu.Unlock()
		return Operation{}, err
	}
	s.mu.Unlock()
	// Always retain uncertainty before a native mutation. Never remove this
	// record to make a failed request eligible for an automatic retry.
	defer func() {
		if result == nil {
			op.State = "accepted"
		} else if definiteRejection(result) {
			op.State = "rejected"
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.records[index].Operation = op
		if err := s.save(); err != nil {
			result = errors.Join(engine.ErrIndeterminate, err)
		}
	}()
	if credentials.RequiresMarker() {
		check, err := s.native.CheckRestore(ctx)
		if err != nil {
			return op, err
		}
		in.ID = check.ID
		op.RestoreID = check.ID
		marker, err := CreateMarker(check.Marker)
		if err != nil {
			return op, err
		}
		defer func() {
			if err := marker.Close(); err != nil {
				result = errors.Join(result, err)
			}
		}()
	}
	if destination.Validate() != nil || !destination.Writable() {
		return op, ErrDestination
	}
	id, err := s.native.AddRestore(ctx, credentials, in)
	if err != nil {
		return op, err
	}
	op.RestoreID = id
	return op, nil
}

// definiteRejection reports a failure after which no native restore job can
// exist: nothing was sent, or the engine answered with a parsed rejection
// (no space, read-only, missing path, permission check, archive key). An
// indeterminate outcome never counts, even when it carries a rejection.
func definiteRejection(err error) bool {
	if errors.Is(err, engine.ErrIndeterminate) {
		return false
	}
	for _, definite := range []error{engine.ErrUnauthorized, engine.ErrNotDispatched, ErrDestination, ErrMarker, engine.ErrLegacyPending, engine.ErrInvalidRequest, engine.ErrRejected, engine.ErrRestoreSpace, engine.ErrRestoreReadOnly, engine.ErrRestorePath, engine.ErrRestoreMarker, engine.ErrArchiveKey} {
		if errors.Is(err, definite) {
			return true
		}
	}
	return false
}

// AcknowledgeOperation records that the user reviewed an indeterminate
// submission. It stays a permanent replay barrier but can then be archived.
func (s *Service) AcknowledgeOperation(id string) (Operation, error) {
	if !requestID.MatchString(id) {
		return Operation{}, engine.ErrInvalidRequest
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.records {
		if r.RequestID != id {
			continue
		}
		if r.State != "indeterminate" {
			return Operation{}, engine.ErrInvalidRequest
		}
		previous := s.records[i].State
		s.records[i].State = "acknowledged"
		if err := s.save(); err != nil {
			s.records[i].State = previous
			return Operation{}, err
		}
		op := s.records[i].Operation
		op.Children = slices.Clone(s.records[i].Children)
		return op, nil
	}
	return Operation{}, engine.ErrInvalidRequest
}

// ClearCompleted removes completed jobs from the native restore queue, as the
// desktop app's "Clear all" does. Restored files are not touched. Each job is
// removed once; the first failure ends the batch, later jobs stay, and the
// failure is returned together with the number already removed.
func (s *Service) ClearCompleted(ctx context.Context) (int, error) {
	jobs, err := s.native.RestoreJobs(ctx)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, job := range jobs {
		if !job.Completed || job.Canceled {
			continue
		}
		if err := s.native.ControlRestore(ctx, job.ID, "REMOVE"); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func (s *Service) admissible(job engine.RestoreJob) bool {
	if job.UsesHelper() && job.UserName() != s.user {
		return false
	}
	for index, root := range s.roots.Paths() {
		path := job.Destination()
		if path == root || len(path) > len(root) && path[:len(root)+1] == root+"/" {
			relative := path[len(root):]
			if len(relative) > 0 {
				relative = relative[1:]
			}
			d, err := s.roots.Resolve(strconv.Itoa(index), relative)
			if err != nil {
				return false
			}
			ok := d.Validate() == nil && d.Writable()
			d.Close()
			return ok
		}
	}
	return false
}
func (s *Service) Jobs(ctx context.Context) ([]Job, error) {
	jobs, err := s.native.RestoreJobs(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	owned := map[string]string{}
	for _, r := range s.records {
		if r.RestoreID != "" {
			owned[r.RestoreID] = r.Path
		}
	}
	s.mu.Unlock()
	out := make([]Job, 0, len(jobs))
	now := time.Now()
	for _, job := range jobs {
		job.ApplyStats(s.liveStats(job.ID, now), now)
		path, known := owned[job.ID]
		out = append(out, Job{RestoreJob: job, Path: job.Destination(), Imported: !known || path != job.Destination(), Admissible: s.admissible(job)})
	}
	return out, nil
}
