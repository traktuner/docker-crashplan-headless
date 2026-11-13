package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrAlreadyCompleted = errors.New("LOGIN_ATTEMPT_ALREADY_COMPLETED")
	ErrLoginInProgress  = errors.New("NATIVE_LOGIN_IN_PROGRESS")
)

// LoginInfo describes a native login, not authentication to this product's UI.
type LoginInfo struct {
	Username        string `json:"username"`
	ServerAddress   string `json:"serverAddress"`
	RegistrationKey string `json:"registrationKey,omitempty"`
	OrganizationKey string `json:"organizationKey,omitempty"`
	Register        bool   `json:"register,omitempty"`
	FirstName       string `json:"firstName,omitempty"`
	LastName        string `json:"lastName,omitempty"`
}

func (i LoginInfo) String() string { return "engine.LoginInfo{redacted}" }

type Credentials struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	ServerAddress   string `json:"serverAddress"`
	RegistrationKey string `json:"registrationKey,omitempty"`
	Register        bool   `json:"register,omitempty"`
	FirstName       string `json:"firstName,omitempty"`
	LastName        string `json:"lastName,omitempty"`
	SSOToken        string `json:"ssoAuthToken,omitempty"`
	MigrationGUID   string `json:"selfServiceMigrationGuid,omitempty"`
}

func (i Credentials) String() string { return "engine.Credentials{redacted}" }

// LoginResult deliberately excludes the native HTTP token. Migration may need
// another user decision even though the native engine issued a temporary token.
type LoginResult struct {
	Complete       bool            `json:"complete"`
	Persisted      bool            `json:"persisted"`
	EventsReady    bool            `json:"eventsReady"`
	MigrationCheck json.RawMessage `json:"migrationCheck,omitempty"`
}

func validLogin(user, server string) bool {
	return strings.TrimSpace(user) != "" && server != "" && len(user) <= 4096 && len(server) <= 4096 && !strings.ContainsAny(user+server, "\r\n\x00")
}

func (c *Client) Login(ctx context.Context, in Credentials) (LoginResult, error) {
	ctx = c.operationContext(ctx)
	if !c.loginMu.TryLock() {
		return LoginResult{}, ErrLoginInProgress
	}
	defer c.loginMu.Unlock()
	return c.login(ctx, in)
}

func (c *Client) login(ctx context.Context, in Credentials) (LoginResult, error) {
	ctx = c.generationContext(ctx)
	if !validLogin(in.Username, in.ServerAddress) || in.Password == "" || len(in.Password) > 65536 {
		return LoginResult{}, ErrInvalidRequest
	}
	info, err := c.registrationContext(ctx, LoginInfo{Username: in.Username, ServerAddress: in.ServerAddress, RegistrationKey: in.RegistrationKey, Register: in.Register})
	if err != nil {
		return LoginResult{}, err
	}
	in.RegistrationKey = info.RegistrationKey
	body, err := json.Marshal(in)
	if err != nil {
		return LoginResult{}, ErrInvalidRequest
	}
	for attempt := 0; attempt < LoginAttempts; attempt++ {
		raw, err := c.request(ctx, "POST", "/v0/LoginRequest", body, false, true)
		// Only the native REATTEMPT_LOGIN response authorizes another login.
		// A timeout, unknown error, or partially successful response never does.
		_, ids, decodeErr := responseEnvelope(raw, "LoginResponseMessage")
		if decodeErr == nil && len(ids) == 1 && ids[0] == "REATTEMPT_LOGIN" && attempt < 3 {
			if ctx.Err() != nil || !c.currentGeneration(ctx) {
				return LoginResult{}, ErrUnavailable
			}
			continue
		}
		return c.finishLogin(ctx, raw, err)
	}
	return LoginResult{}, ErrRejected
}

func (c *Client) finishLogin(ctx context.Context, raw []byte, err error) (LoginResult, error) {
	var result LoginResult
	if len(raw) > 0 {
		decoded, ids, decodeErr := responseEnvelope(raw, "LoginResponseMessage")
		if decodeErr == nil && len(ids) > 0 {
			return result, loginRejection(ids)
		}
		if err == nil && decodeErr != nil {
			return result, decodeErr
		}
		raw = decoded
	}
	if err != nil {
		return result, err
	}
	var response struct {
		Errors    []json.RawMessage `json:"errors"`
		HTTPToken string            `json:"httpToken"`
		Migration string            `json:"selfServiceCheckJson"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return result, ErrProtocol
	}
	if len(response.Errors) > 0 {
		return result, ErrUnauthorized
	}
	if response.HTTPToken == "" {
		return result, errors.Join(ErrIndeterminate, ErrProtocol)
	}
	result.Complete = true
	var migration *MigrationState
	if response.Migration != "" {
		state, err := parseMigration([]byte(response.Migration))
		if err != nil {
			return LoginResult{}, err
		}
		result.Complete = state.Status != "SELECTION_REQUIRED"
		result.MigrationCheck, _ = json.Marshal(state)
		if !result.Complete {
			migration = &state
		}
	}
	if err := c.acceptToken(ctx, response.HTTPToken); err != nil {
		return LoginResult{}, errors.Join(ErrIndeterminate, err)
	}
	c.mu.Lock()
	if c.token == response.HTTPToken {
		c.migration = migration
	}
	c.mu.Unlock()
	result.EventsReady = c.authenticateEvents(ctx, response.HTTPToken) == nil
	c.mu.RLock()
	hasStore := c.store != nil
	c.mu.RUnlock()
	result.Persisted = hasStore && c.saveSession(ctx) == nil
	return result, nil
}
