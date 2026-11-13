package engine

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
)

type DerivedKeyInput struct {
	SourceID   string `json:"sourceId,omitempty"`
	Passphrase string `json:"passphrase"`
}

func (DerivedKeyInput) String() string     { return "engine.DerivedKeyInput{private}" }
func (i DerivedKeyInput) GoString() string { return i.String() }

// Native derivation uses the current account and its configured V2 algorithm.
// DataKeyRestHandler does not derive for arbitrary foreign users. The caller's
// source must therefore belong to that same account before any derivation.
func (c *Client) deriveArchiveKey(ctx context.Context, in DerivedKeyInput) (string, error) {
	ctx = c.generationContext(ctx)
	if nativeBlankPassphrase(in.Passphrase) || len(in.Passphrase) > 65536 || (in.SourceID != "" && !validID(in.SourceID)) {
		return "", ErrInvalidRequest
	}
	if err := c.pinIdentity(ctx); err != nil {
		return "", err
	}
	policy, err := c.LocalArchivePolicy(ctx)
	if err != nil {
		return "", err
	}
	source := in.SourceID
	if source == "" {
		source = policy.SelfID
	}
	allowed := false
	for _, device := range policy.Devices {
		if device.ID == source && (device.Self || (device.Own && device.UserID == policy.UserID)) {
			allowed = true
		}
	}
	if !allowed || !validID(policy.UserID) {
		return "", ErrRejected
	}
	body, _ := json.Marshal(map[string]string{"passphrase": in.Passphrase, "userid": policy.UserID})
	raw, err := c.request(ctx, "POST", "/v1/DataKey", body, true, false)
	raw, err = restoreResponse(raw, err, "")
	if err != nil {
		return "", err
	}
	var wire struct {
		Key string `json:"dataKey"`
	}
	if json.Unmarshal(raw, &wire) != nil || !validArchiveKey(wire.Key) {
		return "", ErrProtocol
	}
	if !c.sessionCurrent(ctx) || ctx.Err() != nil || !c.currentGeneration(ctx) {
		return "", ErrUnavailable
	}
	return wire.Key, nil
}

// Match Java Character.isWhitespace, used by native LangUtils.hasValue.
// A blank value selects random generation instead of passphrase derivation.
// Preserve all bytes of a nonblank passphrase, including surrounding spaces.
func nativeBlankPassphrase(value string) bool {
	return strings.TrimFunc(value, func(r rune) bool {
		return (r >= '\t' && r <= '\r') || (r >= 0x1c && r <= 0x20) ||
			r == 0x1680 || (r >= 0x2000 && r <= 0x2006) || (r >= 0x2008 && r <= 0x200a) ||
			r == 0x2028 || r == 0x2029 || r == 0x205f || r == 0x3000
	}) == ""
}
func (c *Client) ExportDerivedKey(ctx context.Context, in DerivedKeyInput) (KeyExport, error) {
	ctx = c.operationContext(ctx)
	key, err := c.deriveArchiveKey(ctx, in)
	if err != nil {
		return KeyExport{}, err
	}
	return KeyExport{key: key}, nil
}
func (c *Client) customKeyInput(ctx context.Context, source, key, passphrase string) (string, error) {
	if (key == "") == (passphrase == "") {
		return "", ErrInvalidRequest
	}
	if passphrase != "" {
		return c.deriveArchiveKey(ctx, DerivedKeyInput{SourceID: source, Passphrase: passphrase})
	}
	if !validArchiveKey(key) {
		return "", ErrInvalidRequest
	}
	return key, nil
}

type CustomKeyChange struct {
	Confirm    string `json:"confirm"`
	Key        string `json:"key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

func (CustomKeyChange) String() string     { return "engine.CustomKeyChange{private}" }
func (i CustomKeyChange) GoString() string { return i.String() }

// Replacement is distinct from an upgrade. Its readback must confirm the key
// bytes because an unchanged CustomKey type cannot prove replacement.
func (c *Client) ReplaceCustomKey(ctx context.Context, in CustomKeyChange) (SecurityState, error) {
	ctx = c.generationContext(ctx)
	if in.Confirm != "REPLACE CUSTOM KEY" {
		return SecurityState{}, ErrInvalidRequest
	}
	before, err := c.ExportArchiveKey(ctx)
	if err != nil {
		return SecurityState{}, err
	}
	key, err := c.customKeyInput(ctx, "", in.Key, in.Passphrase)
	if err != nil {
		return SecurityState{}, err
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(before.key)) == 1 {
		return SecurityState{}, ErrInvalidRequest
	}
	body, _ := json.Marshal(map[string]any{"type": "CustomKey", "privateKey": map[string]string{"array": key}})
	c.clearArchiveKeys()
	if err := c.securityMutation(ctx, "SecurityKeyTypeResponseMessage", "PUT", "/v0/SecurityKeyTypeRequest", body); err != nil {
		return SecurityState{}, errors.Join(ErrIndeterminate, err)
	}
	after, err := c.ExportArchiveKey(ctx)
	if err != nil || subtle.ConstantTimeCompare([]byte(key), []byte(after.key)) != 1 {
		return SecurityState{}, errors.Join(ErrIndeterminate, err)
	}
	return SecurityState{Type: "CustomKey"}, nil
}
func (c *Client) clearArchiveKeys() { c.mu.Lock(); c.archiveKeys = nil; c.mu.Unlock() }
