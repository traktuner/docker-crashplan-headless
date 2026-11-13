package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/traktuner/crashplan-headless/internal/statefile"
)

var ErrScanInProgress = errors.New("SCAN_ALREADY_PENDING")

type ScanOperation struct {
	Generation         string    `json:"generation"`
	ID                 string    `json:"id"`
	BackupSetID        string    `json:"backupSetId"`
	State              string    `json:"state"`
	EngineVariant      string    `json:"engineVariant,omitempty"`
	Reason             string    `json:"reason,omitempty"`
	CompletionEvidence string    `json:"completionEvidence,omitempty"`
	Baseline           int64     `json:"baseline"`
	Completion         int64     `json:"completion,omitempty"`
	Created            time.Time `json:"created"`
	Updated            time.Time `json:"updated"`
	binding            *scanBinding
}

// The journal keeps attribution private; public scan DTOs omit this binding.
type scanBinding struct {
	Revision uint64 `json:"revision"`
	User     string `json:"user"`
	Self     string `json:"self"`
}

type scanRecord struct {
	ScanOperation
	Binding *scanBinding `json:"context,omitempty"`
}

func (c *Client) pinScan(ctx context.Context) (*scanBinding, error) {
	if err := c.pinIdentity(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	binding, _ := ctx.Value(sessionKey{}).(*sessionBinding)
	if binding == nil || binding.identity == nil || !c.sessionCurrentLocked(ctx) {
		return nil, ErrSessionChanged
	}
	return &scanBinding{Revision: binding.revision, User: binding.identity.user, Self: binding.identity.self}, nil
}

func (c *Client) scanContext(ctx context.Context, saved *scanBinding) (context.Context, error) {
	if saved == nil {
		return nil, ErrSessionChanged
	}
	bound, err := c.BindSession(ctx, saved.Revision)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	bound.Value(sessionKey{}).(*sessionBinding).identity = &accountIdentity{user: saved.User, self: saved.Self}
	c.mu.Unlock()
	return bound, nil
}

type ScanStats struct {
	BackupSetID string `json:"backupSetId"`
	Scanning    bool   `json:"scanning"`
	// LastCompleted is the unamended/native true timestamp, not a schedule reset.
	LastCompleted int64  `json:"lastCompletedScanDate"`
	EngineVariant string `json:"engineVariant"`
	Duration      int64  `json:"durationMs"`
	TotalFiles    int64  `json:"totalFiles,string"`
	TotalSize     int64  `json:"totalSize,string"`
}

func (c *Client) ScanStats(ctx context.Context, setID string) (ScanStats, error) {
	ctx = c.operationContext(ctx)
	if !validID(setID) {
		return ScanStats{}, ErrInvalidRequest
	}
	body, _ := json.Marshal(struct {
		ID string `json:"backupSetId"`
	}{setID})
	raw, err := c.Call(ctx, "scan.stats", OperationRequest{Body: body})
	if err != nil {
		return ScanStats{}, err
	}
	var wire struct {
		BackupSetID   json.Number  `json:"backupSetId"`
		Scanning      *bool        `json:"scanning"`
		LastCompleted *json.Number `json:"lastCompletedScanDate"`
		Unamended     *json.Number `json:"unamendedLastCompletedScanDate"`
		TrueCompleted *json.Number `json:"trueLastCompletedScanDate"`
		Duration      *json.Number `json:"scanDurationInMs"`
		TotalFiles    *json.Number `json:"totalFiles"`
		TotalSize     *json.Number `json:"totalSize"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Scanning == nil || wire.LastCompleted == nil || wire.Duration == nil || wire.BackupSetID.String() != setID {
		return ScanStats{}, ErrProtocol
	}
	last, err := wire.LastCompleted.Int64()
	if err != nil || last < 0 {
		return ScanStats{}, ErrProtocol
	}
	duration, err := wire.Duration.Int64()
	if err != nil || duration < -1 {
		return ScanStats{}, ErrProtocol
	}
	variant := "v1"
	completion := wire.Unamended
	if completion == nil {
		variant = "v3"
		completion = wire.TrueCompleted
	} else if wire.TrueCompleted != nil {
		return ScanStats{}, ErrProtocol
	}
	if completion == nil {
		return ScanStats{}, ErrProtocol
	}
	last, err = completion.Int64()
	if err != nil || last < 0 {
		return ScanStats{}, ErrProtocol
	}
	stats := ScanStats{BackupSetID: setID, Scanning: *wire.Scanning, LastCompleted: last, EngineVariant: variant, Duration: duration}
	for _, entry := range []struct {
		raw *json.Number
		out *int64
	}{{wire.TotalFiles, &stats.TotalFiles}, {wire.TotalSize, &stats.TotalSize}} {
		if entry.raw != nil {
			value, e := entry.raw.Int64()
			if e != nil || value < 0 {
				return ScanStats{}, ErrProtocol
			}
			*entry.out = value
		}
	}
	return stats, nil
}

// KnownScanSet prevents the native command's silent success for unknown IDs.
func (c *Client) KnownScanSet(ctx context.Context, id string) error {
	ctx = c.operationContext(ctx)
	raw, err := c.Call(ctx, "backupSets.list", OperationRequest{ID: id})
	if err != nil {
		return err
	}
	var set struct {
		ID json.Number `json:"id"`
	}
	if json.Unmarshal(raw, &set) != nil || set.ID.String() != id {
		return ErrProtocol
	}
	return nil
}

type Scans struct {
	mu         sync.Mutex
	actionMu   sync.Mutex
	generation string
	client     *Client
	store      *statefile.Store
	operations []ScanOperation
}

func NewScans(client *Client, store *statefile.Store) (*Scans, error) {
	s := &Scans{client: client, store: store, operations: []ScanOperation{}, generation: "unbound"}
	raw, err := store.Read("operations.json")
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var records []scanRecord
	if json.Unmarshal(raw, &records) != nil || records == nil || len(records) > 256 {
		return nil, ErrProtocol
	}
	seen := make(map[string]bool)
	for _, record := range records {
		op := record.ScanOperation
		op.binding = record.Binding
		if op.binding != nil && (op.binding.Revision == 0 || !validID(op.binding.User) || !validID(op.binding.Self)) {
			return nil, ErrProtocol
		}
		if len(op.ID) != 32 || seen[op.ID] || !validID(op.BackupSetID) || op.Baseline < 0 || op.Created.IsZero() || op.Updated.IsZero() {
			return nil, ErrProtocol
		}
		if _, err := hex.DecodeString(op.ID); err != nil {
			return nil, ErrProtocol
		}
		switch op.State {
		case "accepted", "running", "completed", "indeterminate", "stalled", "interrupted", "acknowledged", "failed":
		default:
			return nil, ErrProtocol
		}
		seen[op.ID] = true
		s.operations = append(s.operations, op)
	}
	return s, nil
}

func (s *Scans) save() error {
	records := make([]scanRecord, len(s.operations))
	for i, op := range s.operations {
		records[i] = scanRecord{ScanOperation: op, Binding: op.binding}
	}
	raw, err := json.Marshal(records)
	if err != nil {
		return ErrProtocol
	}
	return s.store.Write("operations.json", raw)
}
func pending(state string) bool {
	return state == "accepted" || state == "running" || state == "indeterminate" || state == "stalled"
}

// Start durably records uncertainty BEFORE dispatch. A crash at any following
// instruction cannot turn an already issued command into a fresh automatic try.
func (s *Scans) Start(ctx context.Context, setID string) (ScanOperation, error) {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	s.mu.Lock()
	if err := s.client.CheckScan(setID); err != nil {
		s.mu.Unlock()
		return ScanOperation{}, err
	}
	for _, op := range s.operations {
		if op.BackupSetID == setID && pending(op.State) {
			s.mu.Unlock()
			return ScanOperation{}, ErrScanInProgress
		}
	}
	s.mu.Unlock()
	ctx = s.client.operationContext(ctx)
	binding, err := s.client.pinScan(ctx)
	if err != nil {
		return ScanOperation{}, err
	}
	if err := s.client.KnownScanSet(ctx, setID); err != nil {
		return ScanOperation{}, err
	}
	baseline, err := s.client.ScanStats(ctx, setID)
	if err != nil {
		return ScanOperation{}, err
	}
	if baseline.Scanning {
		return ScanOperation{}, ErrScanInProgress
	}
	s.mu.Lock()
	if len(s.operations) >= 256 {
		index := -1
		for i, op := range s.operations {
			if !pending(op.State) {
				index = i
				break
			}
		}
		if index < 0 {
			s.mu.Unlock()
			return ScanOperation{}, ErrScanInProgress
		}
		s.operations = append(s.operations[:index], s.operations[index+1:]...)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		s.mu.Unlock()
		return ScanOperation{}, ErrUnavailable
	}
	now := time.Now().UTC()
	op := ScanOperation{Generation: s.generation, ID: hex.EncodeToString(id[:]), BackupSetID: setID, State: "indeterminate", Baseline: baseline.LastCompleted, EngineVariant: baseline.EngineVariant, Created: now, Updated: now, binding: binding}
	s.operations = append(s.operations, op)
	if err := s.save(); err != nil {
		s.operations = s.operations[:len(s.operations)-1]
		s.mu.Unlock()
		return ScanOperation{}, err
	}
	s.mu.Unlock()
	err = s.client.Scan(ctx, setID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		op.State = "accepted"
	} else if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrNotDispatched) {
		op.State = "interrupted"
	}
	op.Updated = time.Now().UTC()
	s.operations[len(s.operations)-1] = op
	if saveErr := s.save(); saveErr != nil {
		return op, errors.Join(ErrIndeterminate, saveErr)
	}
	return op, err
}

func (s *Scans) Get(id string) (ScanOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range s.operations {
		if op.ID == id {
			return op, nil
		}
	}
	return ScanOperation{}, ErrInvalidRequest
}
func (s *Scans) List() []ScanOperation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ScanOperation{}, s.operations...)
}

// Observe is a read of native counters, never a scan dispatch. Idle state or
// elapsed time alone cannot establish successful completion.
func (s *Scans) Observe(ctx context.Context) error {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	s.mu.Lock()
	snapshot := append([]ScanOperation{}, s.operations...)
	generation := s.generation
	s.mu.Unlock()
	var failures []error
	for _, op := range snapshot {
		if !pending(op.State) {
			continue
		}
		next, err := s.observeOperation(ctx, op, generation)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if next.State != op.State || next.Reason != op.Reason {
			s.mu.Lock()
			for i, current := range s.operations {
				if current.ID == op.ID && current.State == op.State {
					next.Updated = time.Now().UTC()
					s.operations[i] = next
					if err := s.save(); err != nil {
						failures = append(failures, err)
					}
					break
				}
			}
			s.mu.Unlock()
		}
	}
	return errors.Join(failures...)
}

func (s *Scans) observeOperation(ctx context.Context, op ScanOperation, generation string) (ScanOperation, error) {
	if op.Generation != generation {
		op.State, op.Reason = "interrupted", "ENGINE_GENERATION_CHANGED"
		return op, nil
	}
	bound, err := s.client.scanContext(ctx, op.binding)
	var stats ScanStats
	if err == nil {
		stats, err = s.client.ScanStats(bound, op.BackupSetID)
		if err == nil && !s.client.sessionCurrent(bound) {
			err = ErrSessionChanged
		}
	}
	if errors.Is(err, ErrSessionChanged) {
		// A same-JVM login or identity change does not prove old work ended.
		op.State, op.Reason = "indeterminate", "NATIVE_SESSION_CHANGED"
		return op, nil
	}
	if err != nil {
		return op, err
	}
	switch {
	case op.EngineVariant != stats.EngineVariant:
		op.State, op.Reason = "interrupted", "ENGINE_GENERATION_CHANGED"
	case !stats.Scanning && stats.LastCompleted > op.Baseline && stats.Duration >= 0 && (stats.EngineVariant != "v3" || stats.Duration > 0) && stats.LastCompleted-stats.Duration >= op.Created.UnixMilli()-2000:
		op.State, op.Reason = "completed", ""
		op.Completion, op.CompletionEvidence = stats.LastCompleted, "native_recorded"
	case stats.Scanning:
		op.State, op.Reason = "running", ""
	case op.State == "running" || (stats.LastCompleted > op.Baseline && stats.Duration < 1 && stats.EngineVariant == "v3"):
		op.State, op.Reason = "failed", "STOPPED_WITHOUT_COMPLETION"
		if stats.EngineVariant == "v3" && stats.Duration < 1 {
			op.Reason = "NATIVE_RESET_WITHOUT_SUCCESS"
		}
	case time.Since(op.Updated) > 30*time.Minute && op.State != "indeterminate":
		op.State, op.Reason = "stalled", "SCAN_START_NOT_OBSERVED"
	}
	return op, nil
}
func (s *Scans) BeginGeneration(id string) error {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation = id
	for i := range s.operations {
		if s.operations[i].Generation != id && s.operations[i].State != "indeterminate" && pending(s.operations[i].State) {
			s.operations[i].State = "interrupted"
			s.operations[i].Updated = time.Now().UTC()
		}
	}
	return s.save()
}
func (s *Scans) Interrupt() error {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.operations {
		if pending(s.operations[i].State) {
			s.operations[i].State = "interrupted"
			s.operations[i].Updated = time.Now().UTC()
		}
	}
	return s.save()
}
func (s *Scans) Acknowledge(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, op := range s.operations {
		if op.ID == id {
			if op.State != "indeterminate" && op.State != "stalled" && op.State != "interrupted" {
				return ErrInvalidRequest
			}
			s.operations[i].State = "acknowledged"
			s.operations[i].Updated = time.Now().UTC()
			return s.save()
		}
	}
	return ErrInvalidRequest
}
