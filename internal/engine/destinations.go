package engine

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// saveDestination is private. Product routes expose named, admitted operations,
// never arbitrary native destination or configuration objects.
func (c *Client) saveDestination(ctx context.Context, id string, destination any, config any, before func() error) error {
	if !validID(id) {
		return ErrInvalidRequest
	}
	if before != nil {
		if err := before(); err != nil {
			return err
		}
	}
	body := map[string]any{"guid": id, "destination": destination}
	if config != nil {
		body["config"] = config
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ErrInvalidRequest
	}
	raw, err := c.legacyRequest(ctx, "SaveDestinationResponseMessage", "PUT", "/v0/SaveDestination", payload, true, true)
	if err != nil {
		return err
	}
	_, ids, decodeErr := responseEnvelope(raw, "SaveDestinationResponseMessage")
	if decodeErr != nil || len(ids) > 0 {
		return ErrIndeterminate
	}
	return nil
}
func replaceConfigValue(config map[string]json.RawMessage, name string, value any) error {
	var item map[string]json.RawMessage
	raw, ok := config[name]
	if ok {
		if json.Unmarshal(raw, &item) != nil || item == nil {
			return ErrProtocol
		}
		var locked *bool
		if json.Unmarshal(item["locked"], &locked) != nil || locked == nil {
			return ErrProtocol
		}
		if *locked {
			return ErrSettingLocked
		}
	} else {
		item = map[string]json.RawMessage{"locked": json.RawMessage(`false`)}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ErrInvalidRequest
	}
	item["value"] = encoded
	config[name], err = json.Marshal(item)
	return err
}

// RelocateMissingArchive updates the native configuration only. It never calls
// MoveJobRequest or queues a FileCopier job. The caller pins source absence.
func (c *Client) RelocateMissingArchive(ctx context.Context, device LocalArchiveDevice, path, expectedOld string, validate func(string) error) (LocalArchiveResult, error) {
	ctx = c.operationContext(ctx)
	if !validID(device.ID) {
		return LocalArchiveResult{}, ErrInvalidRequest
	}
	raw, err := c.request(ctx, "GET", "/v1/SocialNetworkConfig", nil, true, false)
	if err != nil {
		return LocalArchiveResult{}, err
	}
	old, err := c.localArchivePathFromConfig(ctx, device, raw)
	if err != nil {
		return LocalArchiveResult{}, err
	}
	if old != expectedOld {
		return LocalArchiveResult{}, ErrRejected
	}
	var social struct {
		Computers struct {
			Map map[string]json.RawMessage `json:"map"`
		} `json:"computerConfigs"`
	}
	if json.Unmarshal(raw, &social) != nil {
		return LocalArchiveResult{}, ErrProtocol
	}
	config := map[string]json.RawMessage{}
	if row, ok := social.Computers.Map[device.ID]; ok {
		if json.Unmarshal(row, &config) != nil || config == nil {
			return LocalArchiveResult{}, ErrProtocol
		}
		var guid struct {
			Value json.Number `json:"value"`
		}
		if json.Unmarshal(config["guid"], &guid) != nil || guid.Value.String() != device.ID {
			return LocalArchiveResult{}, ErrProtocol
		}
	} else {
		config["guid"], _ = json.Marshal(map[string]any{"value": device.ID, "locked": false})
	}
	if err := replaceConfigValue(config, "manifestPath", path); err != nil {
		return LocalArchiveResult{}, err
	}
	if err := replaceConfigValue(config, "manifestPathDefault", false); err != nil {
		return LocalArchiveResult{}, err
	}
	// The serialized preimage is kept private and preserves all unrelated fields.
	// A path and its default flag are the only values changed by this operation.
	if err := c.saveDestination(ctx, device.ID, map[string]any{}, config, func() error {
		if validate(path) != nil {
			return ErrRestorePath
		}
		return nil
	}); err != nil {
		return LocalArchiveResult{}, err
	}
	current, err := c.LocalArchivePath(ctx, device)
	if err != nil || current != path || validate(path) != nil {
		return LocalArchiveResult{}, ErrIndeterminate
	}
	return LocalArchiveResult{State: "relocated", DestinationID: device.ID, SourceMissing: true}, nil
}

// ChangeDevice exposes only the two fields supported by native preferences.
func (c *Client) ChangeDevice(ctx context.Context, id, action, name string, before func() error) (LocalArchiveResult, error) {
	ctx = c.operationContext(ctx)
	changes := map[string]any{}
	switch action {
	case "rename":
		if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 30 || strings.ContainsAny(name, "\x00\r\n") {
			return LocalArchiveResult{}, ErrInvalidRequest
		}
		changes["name"] = name
	case "remove":
		if name != "" {
			return LocalArchiveResult{}, ErrInvalidRequest
		}
		changes["deleted"] = true
	default:
		return LocalArchiveResult{}, ErrInvalidRequest
	}
	if err := c.saveDestination(ctx, id, changes, nil, before); err != nil {
		return LocalArchiveResult{}, err
	}
	devices, err := c.Devices(ctx)
	if err != nil {
		return LocalArchiveResult{}, ErrIndeterminate
	}
	for _, device := range devices {
		if device.ID == id {
			if action == "rename" && device.Name == name && !device.Deleted {
				return LocalArchiveResult{State: "completed", DestinationID: id, Name: device.Name}, nil
			}
			if action == "remove" && device.Deleted {
				return LocalArchiveResult{State: "completed", DestinationID: id, Deleted: true}, nil
			}
			return LocalArchiveResult{}, ErrIndeterminate
		}
	}
	if action == "remove" {
		return LocalArchiveResult{State: "completed", DestinationID: id, Deleted: true}, nil
	}
	return LocalArchiveResult{}, ErrIndeterminate
}
