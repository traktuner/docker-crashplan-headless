package engine

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"
)

// Native legacy catchers correlate by response class rather than request ID.
// Same-class calls stay serial. A lost answer leaves the class uncertain:
//
//   - uncertain is read uncertainty. Recoverable reads set it and clear it
//     again with an answer that verifiably belongs to the request. Actions do
//     not look at it, so a failed read never blocks an action it cannot clear.
//   - strict is action uncertainty: an action (or a read without a provable
//     echo) lost its answer. Only a new engine generation clears it, so a
//     mutation is never sent while an earlier answer of its class is unknown.
//
// A received answer that decodes as the expected class with native error ids
// is an answer, not a loss: it never makes a lane uncertain. A new JVM
// generation cannot retain the old JVM's catchers, so BindGeneration drops all
// lanes.
type legacyLane struct {
	gate      chan struct{}
	uncertain bool
	strict    bool
	// expires, when set, lifts read uncertainty at that time. Used where a
	// bounded drain already waited and the answers carry their own echo.
	expires time.Time
}

func (l *legacyLane) readUncertain(now time.Time) bool {
	if l.uncertain && !l.expires.IsZero() && !now.Before(l.expires) {
		l.uncertain = false
		l.expires = time.Time{}
	}
	return l.uncertain
}

// lane returns the class lane, creating it. The caller holds no lock.
func (c *Client) lane(ctx context.Context, class string) (context.Context, *legacyLane) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Value(generationKey{}) == nil && c.lifetime != nil {
		ctx = context.WithValue(ctx, generationKey{}, c.lifetime.Value(generationKey{}))
	}
	if c.legacy == nil {
		c.legacy = make(map[string]*legacyLane)
	}
	lane := c.legacy[class]
	if lane == nil {
		lane = &legacyLane{gate: make(chan struct{}, 1)}
		c.legacy[class] = lane
	}
	return ctx, lane
}

// UncertainLanes lists the response classes whose answers are unresolved in
// this generation, for health output. Class names only, never payloads.
func (c *Client) UncertainLanes() map[string]string {
	out := map[string]string{}
	// The count tracker locks before c.mu elsewhere; read it first.
	if c.contentsUncertain() {
		out[contentsClass] = "uncertain"
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for class, lane := range c.legacy {
		if lane.strict || lane.readUncertain(now) {
			out[class] = "uncertain"
		}
	}
	return out
}

// laneCauses are fixed strings; logs never carry native values or bodies.
const (
	causeTransport = "transport"
	causeStatus    = "http_status"
	causeMismatch  = "foreign_answer"
	causeDrain     = "drain_timeout"
	causeSession   = "session_changed"
)

// loggedErrorIDs are native error ids that may appear in logs. Anything else
// is reported as OTHER so a vendor string never reaches the log.
var loggedErrorIDs = map[string]bool{
	"SYSTEM": true, "TIMED_OUT": true, "CPC_UNAVAILABLE": true, "TOO_BUSY": true,
	"PERMISSION_DENIED": true, "NOT_FOUND": true, "INVALID_REQUEST": true,
	"NOT_AUTHORIZED": true, "NO_SPACE": true, "READ_ONLY": true, "PATH_DOESNT_EXIST": true,
}

func logLane(event, class, kind, cause string, err error, raw []byte) {
	attrs := []any{"class", class, "lane", kind}
	if cause != "" {
		attrs = append(attrs, "cause", cause)
	}
	var status *nativeStatusError
	if errors.As(err, &status) {
		attrs = append(attrs, "status", status.status)
	}
	if len(raw) > 0 {
		if _, ids, e := responseEnvelope(raw, class); e == nil && len(ids) > 0 {
			safe := make([]string, 0, len(ids))
			for _, id := range ids {
				if !loggedErrorIDs[id] {
					id = "OTHER"
				}
				safe = append(safe, id)
			}
			sort.Strings(safe)
			attrs = append(attrs, "error_ids", safe)
		}
	}
	if event == "native_lane_uncertain" {
		slog.Warn(event, attrs...)
		return
	}
	slog.Info(event, attrs...)
}

// receivedRejection reports whether raw is a received native answer of the
// class that carries error ids. Such an answer closes the native catcher.
func receivedRejection(raw []byte, class string) bool {
	if len(raw) == 0 {
		return false
	}
	_, ids, err := responseEnvelope(raw, class)
	return err == nil && len(ids) > 0
}

func lossCause(err error) string {
	var status *nativeStatusError
	switch {
	case errors.Is(err, ErrSessionChanged):
		return causeSession
	case errors.As(err, &status):
		return causeStatus
	default:
		return causeTransport
	}
}

// legacyRead is the recoverable path for read-only legacy classes. check
// receives the decoded answer and says whether it proves the lane in step.
func (c *Client) legacyRead(ctx context.Context, class, method, path string, body []byte, check func([]byte) probeResult) ([]byte, error) {
	return c.legacyReadAuth(ctx, class, method, path, body, true, check)
}

func (c *Client) legacyReadAuth(ctx context.Context, class, method, path string, body []byte, auth bool, check func([]byte) probeResult) ([]byte, error) {
	return c.legacyProbe(ctx, class, method, path, body, auth, func(raw []byte) probeResult {
		decoded, err := restoreResponse(raw, nil, class)
		if err != nil {
			if errors.Is(err, ErrProtocol) {
				return probeMismatch
			}
			// A rejection of the right class is an answer, but proves nothing.
			return probeUnknown
		}
		return check(decoded)
	})
}

// anyAnswer suits requests without parameters: every answer of the class fits.
func anyAnswer([]byte) probeResult { return probeInStep }

type probeResult int

const (
	probeMismatch probeResult = iota // not this request's answer: lane uncertain
	probeUnknown                     // usable, but proves nothing: lane unchanged
	probeInStep                      // verifiably this request's answer: lane certain
)

// legacyProbe is legacyRequest for read-only status classes. A lost answer
// marks the class read-uncertain, but a later answer that verifiably belongs
// to this request (matching target) clears it again, so a closed page or one
// timeout does not disable the class until a restart. A received rejection
// leaves the lane as it was: it is an answer, but proves nothing.
func (c *Client) legacyProbe(ctx context.Context, class, method, path string, body []byte, auth bool, verify func([]byte) probeResult) ([]byte, error) {
	ctx, lane := c.lane(ctx, class)
	select {
	case lane.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
	defer func() { <-lane.gate }()
	if !c.currentGeneration(ctx) || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	// Once sent, the answer is read even if the caller is cancelled (a closed
	// page, an ended worker wait): an abandoned answer is what makes a lane
	// uncertain. The caller's deadline still applies; the HTTP client caps
	// every request at its own timeout as well.
	deadline := time.Now().Add(2 * time.Minute)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	ctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	raw, err := c.request(ctx, method, path, body, auth, false)
	if err == nil && !c.currentGeneration(ctx) {
		err = ErrUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.laneCurrentLocked(class, lane) {
		// BindGeneration replaced the lanes while this answer was in flight.
		if err == nil {
			err = ErrUnavailable
		}
		return raw, err
	}
	was := lane.readUncertain(time.Now())
	if err != nil {
		if !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrNotDispatched) && !receivedRejection(raw, class) {
			lane.uncertain = true
			lane.expires = time.Time{}
			if !was {
				logLane("native_lane_uncertain", class, "read", lossCause(err), err, raw)
			}
		}
		return raw, err
	}
	switch verify(raw) {
	case probeInStep:
		lane.uncertain = false
		lane.expires = time.Time{}
		if was {
			logLane("native_lane_recovered", class, "read", "", nil, nil)
		}
		return raw, nil
	case probeUnknown:
		return raw, nil
	default:
		lane.uncertain = true
		lane.expires = time.Time{}
		if !was {
			logLane("native_lane_uncertain", class, "read", causeMismatch, nil, nil)
		}
		return raw, ErrProtocol
	}
}

// laneCurrentLocked reports whether lane is still this generation's lane.
func (c *Client) laneCurrentLocked(class string, lane *legacyLane) bool {
	return c.legacy != nil && c.legacy[class] == lane
}

// legacyRequest is the strict lane for actions and for reads whose answers
// echo nothing provable. A lost answer blocks later strict requests of the
// class until a new generation; a received rejection does not.
func (c *Client) legacyRequest(ctx context.Context, class, method, path string, body []byte, auth, mutation bool) ([]byte, error) {
	return c.legacyStrict(ctx, class, method, path, body, auth, mutation, true)
}

// legacyAction is an authenticated mutation on the strict lane. mark false is
// only for actions that verify their own outcome with a fresh read and take
// nothing from the answer (ControlRestore): their lost answer is not marked.
func (c *Client) legacyAction(ctx context.Context, class, method, path string, body []byte, mark bool) ([]byte, error) {
	return c.legacyStrict(ctx, class, method, path, body, true, true, mark)
}

func (c *Client) legacyStrict(ctx context.Context, class, method, path string, body []byte, auth, mutation, mark bool) ([]byte, error) {
	ctx, lane := c.lane(ctx, class)
	select {
	case lane.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
	defer func() { <-lane.gate }()
	if !c.currentGeneration(ctx) || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	c.mu.RLock()
	strict := lane.strict
	c.mu.RUnlock()
	if strict {
		return nil, ErrLegacyPending
	}
	raw, err := c.request(ctx, method, path, body, auth, mutation)
	if err == nil && !c.currentGeneration(ctx) {
		err = ErrUnavailable
	}
	if mark && err != nil && !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrNotDispatched) && !receivedRejection(raw, class) {
		c.markStrict(class, lane, lossCause(err), err, raw)
	}
	return raw, err
}

func (c *Client) markStrict(class string, lane *legacyLane, cause string, err error, raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.laneCurrentLocked(class, lane) || lane.strict {
		return
	}
	lane.strict = true
	logLane("native_lane_uncertain", class, "action", cause, err, raw)
}

// markReadUncertain marks a class read-uncertain until expiry (zero: until a
// verified answer or a new generation). Used by event-driven classes whose
// drain ended without a terminal answer.
func (c *Client) markReadUncertain(ctx context.Context, class, cause string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if marker := ctx.Value(generationKey{}); marker != nil && (c.lifetime == nil || c.lifetime.Value(generationKey{}) != marker) {
		return
	}
	if c.legacy == nil {
		c.legacy = make(map[string]*legacyLane)
	}
	lane := c.legacy[class]
	if lane == nil {
		lane = &legacyLane{gate: make(chan struct{}, 1)}
		c.legacy[class] = lane
	}
	was := lane.readUncertain(time.Now())
	lane.uncertain = true
	lane.expires = time.Time{}
	if ttl > 0 {
		lane.expires = time.Now().Add(ttl)
	}
	if !was {
		logLane("native_lane_uncertain", class, "read", cause, nil, nil)
	}
}

// readLaneUncertain reports read uncertainty of a class without a request.
func (c *Client) readLaneUncertain(class string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	lane := c.legacy[class]
	return lane != nil && lane.readUncertain(time.Now())
}

// clearReadUncertain ends read uncertainty after a verified terminal answer.
func (c *Client) clearReadUncertain(class string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lane := c.legacy[class]; lane != nil && lane.uncertain {
		lane.uncertain = false
		lane.expires = time.Time{}
		logLane("native_lane_recovered", class, "read", "", nil, nil)
	}
}
