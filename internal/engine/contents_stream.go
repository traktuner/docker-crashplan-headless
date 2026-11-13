package engine

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

var ErrContentsPending = errors.New("ARCHIVE_STATISTICS_PENDING")

// One native count stream bounds background work. It outlives an HTTP caller.
// Only its ordered terminal event permits another query to take this lane.
type contentsStream struct {
	mu                           sync.Mutex
	query                        ArchiveQuery
	revision                     uint64
	epoch                        uint64
	snapshot                     *ArchiveContents
	closed, delivered, uncertain bool
	failure                      error
}
type contentsTracker struct {
	mu     sync.Mutex
	active *contentsStream
	bus    *Events
	cancel context.CancelFunc
}

func (c *Client) resetContents() {
	c.contentsMu.Lock()
	defer c.contentsMu.Unlock()
	if c.contents != nil {
		c.contents.cancel()
		c.contents = nil
	}
}
func (c *Client) contentsTracker(ctx context.Context) (*contentsTracker, error) {
	if _, err := c.OpenEvents(ctx); err != nil {
		return nil, err
	}
	c.contentsMu.Lock()
	defer c.contentsMu.Unlock()
	if c.contents != nil {
		return c.contents, nil
	}
	bus := c.EventBus()
	events, unsubscribe, err := bus.Subscribe("GetFileContentsResultMessage")
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	life := c.lifetime
	c.mu.RUnlock()
	if life == nil {
		life = context.Background()
	}
	life, cancel := context.WithCancel(life)
	tracker := &contentsTracker{bus: bus, cancel: cancel}
	c.contents = tracker
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-life.Done():
				return
			case <-bus.Done():
				return
			case raw := <-events:
				tracker.mu.Lock()
				active := tracker.active
				tracker.mu.Unlock()
				if active != nil {
					active.consume(raw, tracker.bus.Epoch(), true)
				}
			}
		}
	}()
	return tracker, nil
}

// Only complete native query identity may attribute a result. A setless V1
// restore-totals event is unrelated to a product request for a selected set.
func matchArchiveQuery(q ArchiveQuery, raw []byte) (bool, error) {
	var wire struct {
		Query *archiveQueryWire `json:"backupQueryData"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Query == nil {
		return false, ErrProtocol
	}
	got, want := wire.Query, q.wire()
	if got.Source != want.Source || got.Target != want.Target || got.File != want.File || got.Set != want.Set || got.Timestamp != want.Timestamp || got.Deleted != want.Deleted || got.Limit != want.Limit || (got.Last == nil) != (want.Last == nil) {
		return false, nil
	}
	if got.Last != nil && *got.Last != *want.Last {
		return false, nil
	}
	if got.Error != "" {
		return true, ErrRejected
	}
	return true, nil
}
func (s *contentsStream) consume(raw []byte, epoch uint64, terminal bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if epoch != s.epoch {
		s.uncertain = true
		return
	}
	match, err := matchArchiveQuery(s.query, raw)
	if err != nil {
		if errors.Is(err, ErrProtocol) {
			s.failure = err
			s.uncertain = true
		}
		if match && errors.Is(err, ErrRejected) {
			s.failure = err
			s.closed = terminal
		}
		return
	}
	if !match {
		return
	}
	snapshot, err := decodeArchiveContents(s.query, raw)
	if err != nil {
		s.failure = err
		s.uncertain = true
		return
	}
	snapshot.Verified = true
	if terminal && snapshot.Done {
		s.closed = true
	}
	snapshot.Done = s.closed
	s.snapshot = &snapshot
}
func (s *contentsStream) result() (ArchiveContents, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return ArchiveContents{}, s.failure
	}
	if s.uncertain || s.snapshot == nil {
		return ArchiveContents{}, ErrContentsPending
	}
	result := *s.snapshot
	result.Done = s.closed
	if result.Done {
		s.delivered = true
	}
	return result, nil
}
func (c *Client) countArchive(ctx context.Context, q ArchiveQuery) (ArchiveContents, error) {
	if !c.sessionCurrent(ctx) || !c.currentGeneration(ctx) {
		return ArchiveContents{}, ErrSessionChanged
	}
	tracker, err := c.contentsTracker(ctx)
	if err != nil {
		return ArchiveContents{}, err
	}
	tracker.mu.Lock()
	active := tracker.active
	epoch := tracker.bus.Epoch()
	revision := c.SessionRevision()
	if active != nil {
		active.mu.Lock()
		if active.epoch != epoch && !active.closed {
			active.uncertain = true
		}
		replace := active.closed && active.delivered
		if active.failure != nil && active.closed {
			replace = true
		}
		same := active.query == q && active.revision == revision
		active.mu.Unlock()
		if !replace {
			tracker.mu.Unlock()
			if !same {
				return ArchiveContents{}, ErrContentsPending
			}
			return active.result()
		}
	}
	active = &contentsStream{query: q, epoch: epoch, revision: revision}
	tracker.active = active
	tracker.mu.Unlock()
	raw, requestErr := c.request(ctx, "POST", "/v0/GetFileContents", q.body(""), true, false)
	raw, requestErr = restoreResponse(raw, requestErr, "GetFileContentsResultMessage")
	if requestErr == nil {
		active.consume(raw, tracker.bus.Epoch(), false)
	}
	if errors.Is(requestErr, ErrNotDispatched) || errors.Is(requestErr, ErrUnauthorized) {
		tracker.mu.Lock()
		if tracker.active == active {
			tracker.active = nil
		}
		tracker.mu.Unlock()
	}
	if !c.sessionCurrent(ctx) || !c.currentGeneration(ctx) {
		return ArchiveContents{}, ErrSessionChanged
	}
	result, err := active.result()
	if requestErr != nil && errors.Is(err, ErrContentsPending) {
		return ArchiveContents{}, requestErr
	}
	return result, err
}
