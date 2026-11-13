package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// KeyExport can only be written deliberately. JSON and formatting cannot expose it.
type KeyExport struct{ key string }

func (KeyExport) String() string   { return "engine.KeyExport{private}" }
func (KeyExport) GoString() string { return "engine.KeyExport{private}" }
func (k KeyExport) WriteTo(w io.Writer) (int64, error) {
	n, err := io.WriteString(w, k.key+"\n")
	return int64(n), err
}
func (c *Client) ExportArchiveKey(ctx context.Context) (KeyExport, error) {
	ctx = c.operationContext(ctx)
	if err := c.pinIdentity(ctx); err != nil {
		return KeyExport{}, err
	}
	raw, err := c.request(ctx, "GET", "/v1/DataKey", nil, true, false)
	raw, err = restoreResponse(raw, err, "")
	if err != nil {
		return KeyExport{}, err
	}
	var wire struct {
		Type string `json:"securityKeyType"`
		Key  string `json:"dataKey"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return KeyExport{}, ErrProtocol
	}
	if wire.Type != "CustomKey" || !validArchiveKey(wire.Key) {
		return KeyExport{}, ErrArchiveKey
	}
	if !c.sessionCurrent(ctx) || ctx.Err() != nil || !c.currentGeneration(ctx) {
		return KeyExport{}, ErrUnavailable
	}
	return KeyExport{key: wire.Key}, nil
}

// Archive keys remain inside the current native session. No key is persisted
// in product state or included in a public DTO.
type SecurityState struct {
	Type     string `json:"type"`
	Question string `json:"question,omitempty"`
}

func securityRank(kind string) int {
	switch kind {
	case "AccountPassword":
		return 0
	case "PrivatePassword":
		return 1
	case "CustomKey":
		return 2
	}
	return -1
}
func (c *Client) Security(ctx context.Context) (SecurityState, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.request(ctx, "GET", "/v1/DataKey", nil, true, false)
	raw, err = restoreResponse(raw, err, "")
	if err != nil {
		return SecurityState{}, err
	}
	var wire struct {
		Type     string `json:"securityKeyType"`
		Question string `json:"question"`
	}
	if json.Unmarshal(raw, &wire) != nil || securityRank(wire.Type) < 0 || len(wire.Question) > 4096 {
		return SecurityState{}, ErrProtocol
	}
	return SecurityState{Type: wire.Type, Question: wire.Question}, nil
}

type ArchiveUnlock struct {
	SourceID   string `json:"sourceId"`
	Type       string `json:"type"`
	Secret     string `json:"secret,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

func (ArchiveUnlock) String() string   { return "engine.ArchiveUnlock{private}" }
func (ArchiveUnlock) GoString() string { return "engine.ArchiveUnlock{private}" }

func validArchiveKey(key string) bool {
	if key == "" || len(key) > 65536 {
		return false
	}
	b, err := base64.StdEncoding.Strict().DecodeString(key)
	return err == nil && len(b) == 56
}
func (c *Client) archiveKey(source string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.archiveKeys[source]
}
func (c *Client) UnlockArchive(ctx context.Context, in ArchiveUnlock) error {
	ctx = c.generationContext(ctx)
	if !validID(in.SourceID) || len(in.Secret) > 65536 || len(in.Passphrase) > 65536 {
		return ErrInvalidRequest
	}
	token := c.nativeToken()
	route := "/v1/PrivatePasswordDataKey"
	body := map[string]string{"deviceGuid": in.SourceID, "privatePassword": in.Secret}
	switch in.Type {
	case "PrivatePassword":
		if in.Secret == "" || in.Passphrase != "" {
			return ErrInvalidRequest
		}
	case "CustomKey":
		key, err := c.customKeyInput(ctx, in.SourceID, in.Secret, in.Passphrase)
		if err != nil {
			return err
		}
		in.Secret = key
		if !validArchiveKey(in.Secret) {
			return ErrInvalidRequest
		}
		route = "/v1/CustomDataKey"
		body = map[string]string{"deviceGuid": in.SourceID, "dataKey": in.Secret}
	default:
		return ErrInvalidRequest
	}
	if err := c.pinIdentity(ctx); err != nil {
		return err
	}
	encoded, _ := json.Marshal(body)
	raw, err := c.request(ctx, "POST", route, encoded, true, true)
	raw, err = restoreResponse(raw, err, "")
	if err != nil {
		return err
	}
	var result struct {
		Key   string `json:"dataKey"`
		Valid *bool  `json:"valid"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return ErrProtocol
	}
	key := result.Key
	if in.Type == "CustomKey" {
		if result.Valid == nil {
			return ErrProtocol
		}
		if !*result.Valid {
			return ErrRejected
		}
		key = in.Secret
	}
	if !validArchiveKey(key) {
		return ErrProtocol
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.token == "" || c.token != token || !c.sessionCurrentLocked(ctx) {
		return ErrUnavailable
	}
	if marker := ctx.Value(generationKey{}); marker != nil && (c.lifetime == nil || marker != c.lifetime.Value(generationKey{})) {
		return ErrUnavailable
	}
	if c.archiveKeys == nil {
		c.archiveKeys = map[string]string{}
	}
	if len(c.archiveKeys) >= 1024 && c.archiveKeys[in.SourceID] == "" {
		return ErrUnavailable
	}
	c.archiveKeys[in.SourceID] = key
	return nil
}

// UnlockBackup supplies a missing local engine key, rather than a restore-only
// key for another device. Native permission and key-type checks remain intact.
func (c *Client) UnlockBackup(ctx context.Context, secret string) error {
	ctx = c.operationContext(ctx)
	if secret == "" || len(secret) > 65536 {
		return ErrInvalidRequest
	}
	body, _ := json.Marshal(map[string]string{"dataPassword": secret})
	return c.securityMutation(ctx, "UseDataPasswordResponseMessage", "POST", "/v0/UseDataPasswordRequest", body)
}

type ArchivePassword struct {
	Action          string `json:"action"`
	CurrentPassword string `json:"currentPassword,omitempty"`
	CurrentAnswer   string `json:"currentAnswer,omitempty"`
	Password        string `json:"password,omitempty"`
	Question        string `json:"question,omitempty"`
	Answer          string `json:"answer,omitempty"`
}

func (ArchivePassword) String() string   { return "engine.ArchivePassword{private}" }
func (ArchivePassword) GoString() string { return "engine.ArchivePassword{private}" }
func (c *Client) ArchivePassword(ctx context.Context, in ArchivePassword) error {
	ctx = c.operationContext(ctx)
	for _, value := range []string{in.CurrentPassword, in.CurrentAnswer, in.Password, in.Question, in.Answer} {
		if len(value) > 65536 {
			return ErrInvalidRequest
		}
	}
	if in.Action != "question" && len(in.Password) < 8 {
		return ErrInvalidRequest
	}
	method := "POST"
	path := ""
	var body map[string]string
	switch in.Action {
	case "create":
		if (in.Question == "") != (in.Answer == "") {
			return ErrInvalidRequest
		}
		path = "CreateArchivePasswordRequest"
		body = map[string]string{"password": in.Password, "question": in.Question, "answer": in.Answer}
	case "change":
		if in.CurrentPassword == "" {
			return ErrInvalidRequest
		}
		path = "ChangeArchivePasswordRequest"
		body = map[string]string{"currentPassword": in.CurrentPassword, "newPassword": in.Password}
	case "reset":
		if in.CurrentAnswer == "" {
			return ErrInvalidRequest
		}
		method = "PUT"
		path = "ResetArchivePasswordRequest"
		body = map[string]string{"currentAnswer": in.CurrentAnswer, "newPassword": in.Password}
	case "question":
		if in.CurrentPassword == "" || strings.TrimSpace(in.Question) == "" || strings.TrimSpace(in.Answer) == "" {
			return ErrInvalidRequest
		}
		method = "PUT"
		path = "ChangeArchiveQuestionRequest"
		body = map[string]string{"currentPassword": in.CurrentPassword, "question": in.Question, "answer": in.Answer}
	default:
		return ErrInvalidRequest
	}
	encoded, _ := json.Marshal(body)
	return c.securityMutation(ctx, "SimpleResponseMessage", method, "/v0/"+path, encoded)
}

type SecurityUpgrade struct {
	Type       string `json:"type"`
	Confirm    string `json:"confirm"`
	Password   string `json:"password,omitempty"`
	Question   string `json:"question,omitempty"`
	Answer     string `json:"answer,omitempty"`
	Key        string `json:"key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

func (SecurityUpgrade) String() string   { return "engine.SecurityUpgrade{private}" }
func (SecurityUpgrade) GoString() string { return "engine.SecurityUpgrade{private}" }
func (c *Client) UpgradeSecurity(ctx context.Context, in SecurityUpgrade) (SecurityState, error) {
	ctx = c.operationContext(ctx)
	if securityRank(in.Type) < 1 || in.Confirm != "UPGRADE TO "+in.Type || len(in.Question) > 4096 || len(in.Answer) > 65536 || len(in.Password) > 65536 {
		return SecurityState{}, ErrInvalidRequest
	}
	if in.Type == "PrivatePassword" && (len(in.Password) < 8 || (in.Question == "") != (in.Answer == "")) {
		return SecurityState{}, ErrInvalidRequest
	}
	if in.Type == "CustomKey" && ((in.Key == "") == (in.Passphrase == "") || len(in.Passphrase) > 65536 || (in.Key != "" && !validArchiveKey(in.Key))) {
		return SecurityState{}, ErrInvalidRequest
	}
	if err := c.pinIdentity(ctx); err != nil {
		return SecurityState{}, err
	}
	before, err := c.Security(ctx)
	if err != nil {
		return SecurityState{}, errors.Join(ErrNotDispatched, err)
	}
	if securityRank(in.Type) <= securityRank(before.Type) {
		return SecurityState{}, ErrInvalidRequest
	}
	if in.Type != "CustomKey" && (in.Key != "" || in.Passphrase != "") {
		return SecurityState{}, ErrInvalidRequest
	}
	if in.Type == "CustomKey" {
		key, err := c.customKeyInput(ctx, "", in.Key, in.Passphrase)
		if err != nil {
			return SecurityState{}, err
		}
		in.Key = key
	}
	body := map[string]any{"type": in.Type}
	if in.Type == "PrivatePassword" {
		body["dataPassword"] = in.Password
		body["dataQuestion"] = in.Question
		body["dataAnswer"] = in.Answer
	} else {
		body["privateKey"] = map[string]string{"array": in.Key}
	}
	encoded, _ := json.Marshal(body)
	c.clearArchiveKeys()
	if err := c.securityMutation(ctx, "SecurityKeyTypeResponseMessage", "PUT", "/v0/SecurityKeyTypeRequest", encoded); err != nil {
		return SecurityState{}, err
	}
	after, err := c.Security(ctx)
	if err != nil {
		return SecurityState{}, errors.Join(ErrIndeterminate, err)
	}
	if after.Type != in.Type {
		return after, ErrIndeterminate
	}
	c.mu.Lock()
	c.archiveKeys = nil
	c.mu.Unlock()
	return after, nil
}
func (c *Client) securityMutation(ctx context.Context, class, method, path string, body []byte) error {
	if err := c.pinIdentity(ctx); err != nil {
		return err
	}
	raw, err := c.legacyRequest(ctx, class, method, path, body, true, true)
	raw, err = restoreResponse(raw, err, class)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return ErrIndeterminate
	}
	return nil
}
