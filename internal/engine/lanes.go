package engine

import (
	"context"
	"errors"
	"time"
)

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
// marks the lane uncertain like any legacy request, but a later answer that
// verifiably belongs to this request (matching target) clears it again, so a
// closed page or one timeout does not disable the class until a restart.
func (c *Client) legacyProbe(ctx context.Context, class, method, path string, body []byte, auth bool, verify func([]byte) probeResult) ([]byte, error) {
	c.mu.Lock()
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
	c.mu.Unlock()
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
	if err != nil {
		if !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrNotDispatched) {
			lane.uncertain = true
		}
		return raw, err
	}
	switch verify(raw) {
	case probeInStep:
		lane.uncertain = false
		return raw, nil
	case probeUnknown:
		return raw, nil
	default:
		lane.uncertain = true
		return raw, ErrProtocol
	}
}
