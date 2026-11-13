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
	raw, err := c.legacyRequest(ctx, "BackupStatsMessage", "PUT", "/v0/GetBackupStats", body, true, false)
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
