package engine

import (
	"context"
	"encoding/json"
	"errors"
)

// CheckMaintenance matches native destination preferences and proves that a
// BackupClient exists. A normal command response alone also covers native no-ops.
func (c *Client) CheckMaintenance(ctx context.Context, source, target string) error {
	ctx = c.operationContext(ctx)
	if !validID(source) || !validID(target) || source == target {
		return ErrInvalidRequest
	}
	policy, err := c.LocalArchivePolicy(ctx)
	if err != nil {
		return err
	}
	if policy.SelfID != source {
		return ErrRejected
	}
	connected := false
	for _, device := range policy.Devices {
		if device.ID == target && !device.Deleted {
			connected = device.Connected
		}
	}
	if !connected {
		return ErrRejected
	}
	body, _ := json.Marshal(map[string]string{"targetId": target})
	raw, err := c.legacyRead(ctx, "BackupStatsMessage", "PUT", "/v0/GetBackupStats", body, func(decoded []byte) probeResult {
		var named struct {
			Stats *struct {
				Target json.RawMessage `json:"targetId"`
			} `json:"backupStats"`
		}
		if json.Unmarshal(decoded, &named) == nil && named.Stats != nil && decimal(named.Stats.Target) == target {
			return probeInStep
		}
		return probeUnknown
	})
	raw, err = restoreResponse(raw, err, "BackupStatsMessage")
	if err != nil {
		return err
	}
	var wire struct {
		Stats *struct {
			Source json.Number `json:"sourceId"`
			Target json.Number `json:"targetId"`
			Client *bool       `json:"isBackupTarget"`
			Bytes  json.Number `json:"remoteManifestSize"`
		} `json:"backupStats"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return ErrProtocol
	}
	if wire.Stats == nil {
		return ErrRejected
	}
	stats := wire.Stats
	if stats.Source.String() != source || stats.Target.String() != target || stats.Client == nil || !*stats.Client {
		return ErrProtocol
	}
	size, err := stats.Bytes.Int64()
	if err != nil {
		return ErrProtocol
	}
	if size <= 0 {
		return ErrRejected
	}
	return nil
}

// MaintainArchive submits the native preferences command once. Acceptance does
// not prove that background maintenance started or completed after dispatch.
func (c *Client) MaintainArchive(ctx context.Context, source, target string) error {
	ctx = c.operationContext(ctx)
	if err := c.CheckMaintenance(ctx, source, target); err != nil {
		return errors.Join(ErrNotDispatched, err)
	}
	return c.namedCommand(ctx, "backup.maint", "backup.maint true "+source+" "+target)
}

// Reconnect is the stock destination row's "reconnect" link: the engine
// command "reconnect" (Command {command:"reconnect"}, backup_set_destination.js).
// The stock app sends it without a GUID, which reconnects every destination;
// the route names one destination, which must be a current backup target.
func (c *Client) Reconnect(ctx context.Context, target string) error {
	ctx = c.operationContext(ctx)
	if !validID(target) {
		return ErrInvalidRequest
	}
	devices, err := c.Devices(ctx)
	if err != nil {
		return errors.Join(ErrNotDispatched, err)
	}
	known := false
	for _, device := range devices {
		if device.ID == target && device.Target && !device.Self && !device.Deleted {
			known = true
		}
	}
	if !known {
		return ErrInvalidRequest
	}
	if err := c.namedCommand(ctx, "reconnect", "reconnect"); err != nil {
		return err
	}
	// The connection state changes after the command; do not serve the old list.
	c.InvalidateTargets()
	return nil
}
