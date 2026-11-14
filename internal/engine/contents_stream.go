package engine

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var ErrContentsPending = errors.New("ARCHIVE_STATISTICS_PENDING")

const contentsClass = "GetFileContentsResultMessage"

// contentsDrain is how long a count whose request failed, or whose stream
// became uncertain, may stay silent before another query may take the lane.
// Results carry the complete query identity, so a late result of the old
// query is never attributed to a different one.
var contentsDrain = 45 * time.Second

// One native count stream bounds background work. It outlives an HTTP caller.
// Its ordered terminal event permits another query to take this lane; a
// failed or uncertain stream releases it after a bounded silent drain.
type contentsStream struct {
	mu                           sync.Mutex
	query                        ArchiveQuery
	revision                     uint64
	epoch                        uint64
	snapshot                     *ArchiveContents
	closed, delivered, uncertain bool
	requestFailed                bool
	activity                     time.Time
	failure                      error
}

// expired reports a failed or uncertain stream without a terminal event that
// stayed silent for the whole drain. The caller holds s.mu.
func (s *contentsStream) expired(now time.Time) bool {
	return !s.closed && (s.requestFailed || s.uncertain) && now.Sub(s.activity) >= contentsDrain
}

// contentsUncertain reports an unresolved count for health output.
func (c *Client) contentsUncertain() bool {
	c.contentsMu.Lock()
	tracker := c.contents
	c.contentsMu.Unlock()
	if tracker == nil {
		return false
	}
	tracker.mu.Lock()
	active := tracker.active
	tracker.mu.Unlock()
	if active == nil {
		return false
	}
	active.mu.Lock()
	defer active.mu.Unlock()
	return !active.closed && (active.requestFailed || active.uncertain) && !active.expired(time.Now())
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
	events, unsubscribe, err := bus.Subscribe(contentsClass)
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
	if match {
		s.activity = time.Now()
	}
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
		if active.expired(time.Now()) {
			// No terminal event arrived during the drain. Release the lane
			// instead of blocking every count until the engine restarts.
			replace = true
			logLane("native_lane_recovered", contentsClass, "read", causeDrain, nil, nil)
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
	active = &contentsStream{query: q, epoch: epoch, revision: revision, activity: time.Now()}
	tracker.active = active
	tracker.mu.Unlock()
	raw, requestErr := c.request(ctx, "POST", "/v0/GetFileContents", q.body(""), true, false)
	// A native answer with error ids ends the count: no stream follows.
	rejected := requestErr != nil && receivedRejection(raw, contentsClass)
	raw, requestErr = restoreResponse(raw, requestErr, contentsClass)
	if requestErr == nil {
		active.consume(raw, tracker.bus.Epoch(), false)
	}
	if errors.Is(requestErr, ErrNotDispatched) || errors.Is(requestErr, ErrUnauthorized) {
		tracker.mu.Lock()
		if tracker.active == active {
			tracker.active = nil
		}
		tracker.mu.Unlock()
	} else if rejected {
		active.mu.Lock()
		if !active.closed {
			active.failure = requestErr
			active.closed = true
		}
		active.mu.Unlock()
	} else if requestErr != nil {
		active.mu.Lock()
		if !active.closed {
			active.requestFailed = true
			active.activity = time.Now()
			logLane("native_lane_uncertain", contentsClass, "read", lossCause(requestErr), requestErr, raw)
		}
		active.mu.Unlock()
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
