package engine

import (
	"context"
	"encoding/json"
)

type MigrationDevice struct {
	ID           string `json:"computerGuid"`
	Name         string `json:"name"`
	Hostname     string `json:"hostname,omitempty"`
	OS           string `json:"osName,omitempty"`
	ArchiveBytes string `json:"archiveBytes,omitempty"`
}
type MigrationState struct {
	Status  string            `json:"status"`
	Reason  string            `json:"reason,omitempty"`
	Devices []MigrationDevice `json:"devices"`
}

func parseMigration(raw []byte) (MigrationState, error) {
	var wire struct {
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Devices []struct {
			ID       string       `json:"computerGuid"`
			Name     string       `json:"name"`
			Hostname string       `json:"hostname"`
			OS       string       `json:"osName"`
			Bytes    *json.Number `json:"archiveBytes"`
		} `json:"devices"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Status == "" || len(wire.Status) > 64 || len(wire.Reason) > 128 || len(wire.Devices) > 10000 {
		return MigrationState{}, ErrProtocol
	}
	state := MigrationState{Status: wire.Status, Reason: wire.Reason, Devices: []MigrationDevice{}}
	seen := map[string]bool{}
	for _, d := range wire.Devices {
		if !validID(d.ID) || seen[d.ID] || len(d.Name) > 4096 || len(d.Hostname) > 4096 || len(d.OS) > 256 {
			return MigrationState{}, ErrProtocol
		}
		seen[d.ID] = true
		device := MigrationDevice{ID: d.ID, Name: d.Name, Hostname: d.Hostname, OS: d.OS}
		if d.Bytes != nil {
			if !validTimestamp(d.Bytes.String()) {
				return MigrationState{}, ErrProtocol
			}
			device.ArchiveBytes = d.Bytes.String()
		}
		state.Devices = append(state.Devices, device)
	}
	return state, nil
}
func (c *Client) Migration() MigrationState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.migration == nil {
		return MigrationState{Status: "NONE", Devices: []MigrationDevice{}}
	}
	state := *c.migration
	state.Devices = append([]MigrationDevice{}, state.Devices...)
	return state
}
func (c *Client) SelectMigration(ctx context.Context, selected string) (LoginResult, error) {
	ctx = c.generationContext(ctx)
	if !c.loginMu.TryLock() {
		return LoginResult{}, ErrLoginInProgress
	}
	defer c.loginMu.Unlock()
	pending := c.Migration()
	if pending.Status != "SELECTION_REQUIRED" || (selected != "" && !validID(selected)) {
		return LoginResult{}, ErrInvalidRequest
	}
	if selected != "" {
		offered := false
		for _, d := range pending.Devices {
			if d.ID == selected {
				offered = true
			}
		}
		if !offered {
			return LoginResult{}, ErrInvalidRequest
		}
	}
	body, _ := json.Marshal(map[string]string{"selectedComputerGuid": selected})
	raw, err := c.request(ctx, "PUT", "/v1/DeviceMigrationSelfService", body, true, true)
	return c.finishLogin(ctx, raw, err)
}
func (c *Client) MigrationCandidates(ctx context.Context) (MigrationState, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.request(ctx, "GET", "/v1/DeviceMigrationAuthorized", nil, true, false)
	if err != nil {
		return MigrationState{}, err
	}
	return parseMigration(raw)
}
func (c *Client) AuthorizeMigration(ctx context.Context, id, confirm string) error {
	ctx = c.operationContext(ctx)
	if !validID(id) || id != confirm {
		return ErrInvalidRequest
	}
	if err := c.pinIdentity(ctx); err != nil {
		return err
	}
	state, err := c.MigrationCandidates(ctx)
	if err != nil {
		return err
	}
	offered := false
	for _, d := range state.Devices {
		if d.ID == id {
			offered = true
		}
	}
	if !offered {
		return ErrInvalidRequest
	}
	if err := c.allowSelfTransition(ctx, id); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"sourceComputerGuid": id})
	raw, err := c.request(ctx, "POST", "/v1/DeviceMigrationAuthorized", body, true, true)
	if err != nil {
		return err
	}
	var result struct {
		Success *bool `json:"success"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Success == nil {
		return ErrIndeterminate
	}
	if !*result.Success {
		return ErrRejected
	}
	return nil
}
