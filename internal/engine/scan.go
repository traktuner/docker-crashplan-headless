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
	if json.Unmarshal(raw, &s.operations) != nil || s.operations == nil || len(s.operations) > 256 {
		return nil, ErrProtocol
	}
	seen := make(map[string]bool)
	for _, op := range s.operations {
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
	}
	return s, nil
}

func (s *Scans) save() error {
	raw, err := json.Marshal(s.operations)
	if err != nil {
		return ErrProtocol
	}
	return s.store.Write("operations.json", raw)
}
func pending(state string) bool {
	return state == "accepted" || state == "running" || state == "indeterminate"
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
	op := ScanOperation{Generation: s.generation, ID: hex.EncodeToString(id[:]), BackupSetID: setID, State: "indeterminate", Baseline: baseline.LastCompleted, EngineVariant: baseline.EngineVariant, Created: now, Updated: now}
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
		if !pending(op.State) && op.State != "stalled" {
			continue
		}
		stats, err := s.client.ScanStats(ctx, op.BackupSetID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		next := op.State
		if op.Generation != generation || op.EngineVariant != stats.EngineVariant {
			next = "interrupted"
			op.Reason = "ENGINE_GENERATION_CHANGED"
		} else if !stats.Scanning && stats.LastCompleted > op.Baseline && stats.Duration >= 0 && (stats.EngineVariant != "v3" || stats.Duration > 0) && stats.LastCompleted-stats.Duration >= op.Created.UnixMilli()-2000 {
			next = "completed"
			op.Completion = stats.LastCompleted
			op.CompletionEvidence = "native_recorded"
		} else if stats.Scanning {
			next = "running"
		} else if op.State == "running" || (stats.LastCompleted > op.Baseline && stats.Duration < 1 && stats.EngineVariant == "v3") {
			next = "failed"
			op.Reason = "STOPPED_WITHOUT_COMPLETION"
			if stats.EngineVariant == "v3" && stats.Duration < 1 {
				op.Reason = "NATIVE_RESET_WITHOUT_SUCCESS"
			}
		} else if time.Since(op.Updated) > 30*time.Minute && op.State != "indeterminate" {
			next = "stalled"
			op.Reason = "SCAN_START_NOT_OBSERVED"
		}
		if next != op.State {
			s.mu.Lock()
			for i, current := range s.operations {
				if current.ID == op.ID && current.State == op.State {
					op.State = next
					op.Updated = time.Now().UTC()
					s.operations[i] = op
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
func (s *Scans) BeginGeneration(id string) error {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation = id
	for i := range s.operations {
		if s.operations[i].Generation != id && s.operations[i].State != "indeterminate" && (pending(s.operations[i].State) || s.operations[i].State == "stalled") {
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
		if pending(s.operations[i].State) || s.operations[i].State == "stalled" {
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
