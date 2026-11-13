package engine

import (
	"context"
	"errors"
)

var ErrSessionChanged = errors.New("NATIVE_SESSION_CHANGED")

type sessionKey struct{}

// Client.mu protects the binding, including advancement by its own login.
type sessionBinding struct {
	revision       uint64
	token          string
	identity       *accountIdentity
	selfTransition string
}

func (c *Client) SessionRevision() uint64 { c.mu.RLock(); defer c.mu.RUnlock(); return c.revision }
func (c *Client) BindSession(ctx context.Context, revision uint64) (context.Context, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if revision != c.revision {
		return nil, errors.Join(ErrNotDispatched, ErrSessionChanged)
	}
	return context.WithValue(ctx, sessionKey{}, &sessionBinding{revision: c.revision, token: c.token}), nil
}

// Pin each public operation once. Nested native calls retain that same binding.
func (c *Client) operationContext(ctx context.Context) context.Context {
	if ctx.Value(sessionKey{}) != nil {
		return ctx
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if ctx.Value(generationKey{}) == nil && c.lifetime != nil {
		ctx = context.WithValue(ctx, generationKey{}, c.lifetime.Value(generationKey{}))
	}
	return context.WithValue(ctx, sessionKey{}, &sessionBinding{revision: c.revision, token: c.token})
}
func (c *Client) sessionCurrentLocked(ctx context.Context) bool {
	binding, _ := ctx.Value(sessionKey{}).(*sessionBinding)
	return binding == nil || (binding.revision == c.revision && binding.token == c.token)
}
func (c *Client) sessionCurrent(ctx context.Context) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionCurrentLocked(ctx)
}

func (c *Client) boundRevision(ctx context.Context) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if binding, ok := ctx.Value(sessionKey{}).(*sessionBinding); ok {
		return binding.revision
	}
	return c.revision
}
