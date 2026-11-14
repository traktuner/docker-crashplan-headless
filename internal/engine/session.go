package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/traktuner/crashplan-headless/internal/statefile"
)

// UseSessionStore binds the private product state before starting a generation.
func (c *Client) UseSessionStore(store *statefile.Store) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store = store
}

func (c *Client) saveSession(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if marker := ctx.Value(generationKey{}); marker != nil && (ctx.Err() != nil || c.lifetime == nil || c.lifetime.Value(generationKey{}) != marker) {
		return ErrIndeterminate
	}
	if c.store == nil {
		return nil
	}
	if !c.sessionCurrentLocked(ctx) {
		return ErrSessionChanged
	}
	token := c.token
	if binding, ok := ctx.Value(sessionKey{}).(*sessionBinding); ok {
		token = binding.token
	}
	return c.store.Write("native-session", []byte(token))
}

func (c *Client) autoLoginEligible(ctx context.Context, token string) (bool, error) {
	body, _ := json.Marshal(struct {
		Token string `json:"token"`
	}{token})
	raw, err := c.request(ctx, "POST", "/v1/AuthToken", body, false, false)
	if err != nil {
		return false, err
	}
	var response struct {
		Valid *bool `json:"valid"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Valid == nil {
		return false, ErrProtocol
	}
	return *response.Valid, nil
}

// RestoreSession requires native auto-login eligibility. A false flag alone
// does not imply an already active token lost HTTP authorization.
func (c *Client) RestoreSession(ctx context.Context) error {
	ctx = c.generationContext(ctx)
	if !c.loginMu.TryLock() {
		return ErrLoginInProgress
	}
	defer c.loginMu.Unlock()
	if c.Authorized() {
		return nil
	}
	c.mu.RLock()
	store := c.store
	c.mu.RUnlock()
	if store == nil {
		return nil
	}
	raw, err := store.Read("native-session")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	token := string(raw)
	if len(token) > 512 || strings.ContainsAny(token, "\r\n\t ") {
		return ErrProtocol
	}
	valid, err := c.autoLoginEligible(ctx, token)
	if err != nil {
		return err
	}
	if !valid {
		// Keep the saved token: valid:false can be transient while the engine
		// starts, and only a successful sign-in replaces the saved session.
		// The token is not used; the account needs a sign-in for now.
		return nil
	}
	if err := c.acceptToken(ctx, token); err != nil {
		return err
	}
	return c.authenticateEvents(ctx, token)
}

// TouchSession refreshes native idle access. It never changes auto-login
// settings and never interprets valid:false as an active authorization denial.
func (c *Client) TouchSession(ctx context.Context) error {
	ctx = c.operationContext(ctx)
	token := c.nativeToken()
	if token == "" {
		return nil
	}
	_, err := c.autoLoginEligible(ctx, token)
	return err
}
