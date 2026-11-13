package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type ReplacementState struct {
	Available  bool   `json:"available"`
	InProgress bool   `json:"inProgress"`
	Selected   string `json:"deviceSelectedGuid"`
	Screen     string `json:"screenState"`
}

func (c *Client) Replacement(ctx context.Context) (ReplacementState, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.request(ctx, "GET", "/v1/ReplaceDevice", nil, false, false)
	if err != nil {
		return ReplacementState{}, err
	}
	var wire struct {
		Available  *bool  `json:"available"`
		InProgress *bool  `json:"inProgress"`
		Selected   string `json:"deviceSelectedGuid"`
		Screen     string `json:"screenState"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Available == nil || wire.InProgress == nil || len(wire.Screen) > 4096 || strings.ContainsRune(wire.Screen, 0) {
		return ReplacementState{}, ErrProtocol
	}
	if wire.Selected == "null" {
		wire.Selected = ""
	}
	if wire.Selected != "" && !validID(wire.Selected) {
		return ReplacementState{}, ErrProtocol
	}
	return ReplacementState{*wire.Available, *wire.InProgress, wire.Selected, wire.Screen}, nil
}

// These native finish/cancel actions close saved wizard state. They do not
// undo an identity change or stop active backup/restore jobs.
func (c *Client) CompleteReplacement(ctx context.Context, action, confirm string) (ReplacementState, error) {
	ctx = c.operationContext(ctx)
	if (action != "finish" && action != "cancel") || confirm != strings.ToUpper(action)+" REPLACEMENT" {
		return ReplacementState{}, ErrInvalidRequest
	}
	ctx = c.generationContext(ctx)
	if err := c.pinIdentity(ctx); err != nil {
		return ReplacementState{}, err
	}
	token := c.nativeToken()
	before, err := c.Replacement(ctx)
	if err != nil {
		return ReplacementState{}, err
	}
	if !before.InProgress && !before.Available && before.Selected == "" && before.Screen == "" {
		return ReplacementState{}, ErrInvalidRequest
	}
	if token == "" || token != c.nativeToken() || !c.currentGeneration(ctx) {
		return ReplacementState{}, ErrUnavailable
	}
	raw, err := c.request(ctx, "PUT", "/v1/ReplaceDevice", []byte(`{"inProgress":false}`), true, true)
	if len(raw) > 0 {
		_, err = restoreResponse(raw, err, "")
	}
	if err != nil {
		return ReplacementState{}, errors.Join(ErrIndeterminate, err)
	}
	after, err := c.Replacement(ctx)
	if err != nil || after.InProgress || after.Available || after.Selected != "" || after.Screen != "" || token != c.nativeToken() || !c.currentGeneration(ctx) {
		return ReplacementState{}, errors.Join(ErrIndeterminate, err)
	}
	return after, nil
}
func (c *Client) ReplaceDevice(ctx context.Context, id, confirm string) error {
	ctx = c.operationContext(ctx)
	if !validID(id) || id != confirm {
		return ErrInvalidRequest
	}
	if err := c.pinIdentity(ctx); err != nil {
		return err
	}
	devices, err := c.Devices(ctx)
	if err != nil {
		return err
	}
	offered := false
	for _, d := range devices {
		if d.ID == id && d.Source && !d.Self {
			offered = true
		}
	}
	if !offered {
		return ErrInvalidRequest
	}
	if err := c.allowSelfTransition(ctx, id); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"guid": id})
	raw, err := c.legacyRequest(ctx, "ReplaceDeviceResponseMessage", "POST", "/v0/ReplaceDeviceRequest", body, true, true)
	_, err = restoreResponse(raw, err, "ReplaceDeviceResponseMessage")
	return err
}
