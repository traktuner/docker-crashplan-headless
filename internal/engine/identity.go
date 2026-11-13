package engine

import (
	"context"
	"encoding/json"
)

// Native HTTP tokens are not account IDs. Pin the current user and self device
// for sensitive operations, without exposing either identity in product output.
type accountIdentity struct{ user, self string }

func (c *Client) readIdentity(ctx context.Context) (accountIdentity, error) {
	raw, err := c.request(ctx, "GET", "/v0/GetComputers", nil, true, false)
	if err != nil {
		return accountIdentity{}, err
	}
	users, ids, err := responseEnvelope(raw, "UsersMessage")
	if err != nil {
		return accountIdentity{}, err
	}
	if len(ids) != 0 {
		return accountIdentity{}, ErrRejected
	}
	var u struct {
		ID json.Number `json:"myUserId"`
	}
	if json.Unmarshal(users, &u) != nil || !validID(u.ID.String()) {
		return accountIdentity{}, ErrProtocol
	}
	computers, ids, err := responseEnvelope(raw, "ComputersMessage")
	if err != nil {
		return accountIdentity{}, err
	}
	if len(ids) != 0 {
		return accountIdentity{}, ErrRejected
	}
	var wire struct {
		Items []struct {
			ID         json.Number       `json:"peerId"`
			User       json.Number       `json:"userId"`
			Properties map[string]string `json:"properties"`
		} `json:"computers"`
	}
	if json.Unmarshal(computers, &wire) != nil || len(wire.Items) > 10000 {
		return accountIdentity{}, ErrProtocol
	}
	identity := accountIdentity{user: u.ID.String()}
	for _, item := range wire.Items {
		if item.Properties["ME"] == "true" {
			if identity.self != "" || !validID(item.ID.String()) || item.User.String() != identity.user {
				return accountIdentity{}, ErrProtocol
			}
			identity.self = item.ID.String()
		}
	}
	if identity.self == "" {
		return accountIdentity{}, ErrProtocol
	}
	return identity, nil
}
func (c *Client) pinIdentity(ctx context.Context) error {
	c.mu.RLock()
	binding, _ := ctx.Value(sessionKey{}).(*sessionBinding)
	already := binding != nil && binding.identity != nil
	c.mu.RUnlock()
	if already {
		return c.checkIdentity(ctx)
	}
	identity, err := c.readIdentity(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	binding, _ = ctx.Value(sessionKey{}).(*sessionBinding)
	if binding == nil || !c.sessionCurrentLocked(ctx) {
		return ErrSessionChanged
	}
	if binding.identity != nil && *binding.identity != identity {
		return ErrSessionChanged
	}
	binding.identity = &identity
	return nil
}
func (c *Client) checkIdentity(ctx context.Context) error {
	c.mu.RLock()
	binding, _ := ctx.Value(sessionKey{}).(*sessionBinding)
	var pinned accountIdentity
	has := binding != nil && binding.identity != nil
	allowed := ""
	if has {
		pinned = *binding.identity
		allowed = binding.selfTransition
	}
	c.mu.RUnlock()
	if !has {
		return nil
	}
	current, err := c.readIdentity(ctx)
	if err != nil {
		return err
	}
	if current != pinned {
		if current.user != pinned.user || current.self != allowed {
			return ErrSessionChanged
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.sessionCurrentLocked(ctx) {
			return ErrSessionChanged
		}
		c.revision++
		binding.revision = c.revision
		binding.identity = &current
		binding.selfTransition = ""
		c.archiveKeys = nil
		c.migration = nil
	}
	return nil
}

// Only named, confirmed adoption operations may change the self device.
func (c *Client) allowSelfTransition(ctx context.Context, id string) error {
	if !validID(id) {
		return ErrInvalidRequest
	}
	if err := c.checkIdentity(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	binding, _ := ctx.Value(sessionKey{}).(*sessionBinding)
	if binding == nil || binding.identity == nil || !c.sessionCurrentLocked(ctx) {
		return ErrSessionChanged
	}
	binding.selfTransition = id
	return nil
}
