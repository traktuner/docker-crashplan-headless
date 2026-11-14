package engine

import (
	"context"
	"encoding/json"
	"strings"
)

// Only these shared reads use this adapter. Mutating and domain-specific
// workflows have typed methods; there is no parallel descriptive catalog.
type readOperation struct {
	method, path                 string
	auth, resource               bool
	responseClass, requiredField string
}

var readOperations = map[string]readOperation{
	"status":           {method: "GET", path: "/v1/ApplicationStatus"},
	"backupSets.basic": {method: "GET", path: "/v1/BasicBackupSet"},
	"backupSets.list":  {method: "GET", path: "/v1/BackupSet", auth: true, resource: true},
	"scan.stats":       {method: "PUT", path: "/v0/GetScanStats", responseClass: "ScanStatsMessage", requiredField: "scanning"},
}

type OperationRequest struct {
	ID    string            `json:"id,omitempty"`
	Query map[string]string `json:"query,omitempty"`
	Body  json.RawMessage   `json:"body,omitempty"`
}

func (c *Client) Call(ctx context.Context, name string, in OperationRequest) (json.RawMessage, error) {
	ctx = c.operationContext(ctx)
	op, ok := readOperations[name]
	if !ok || len(in.Query) != 0 {
		return nil, ErrInvalidRequest
	}
	path := op.path
	if in.ID != "" {
		if !op.resource || !validID(in.ID) {
			return nil, ErrInvalidRequest
		}
		path += "/" + in.ID
	}
	body := in.Body
	if len(body) > 0 {
		var object map[string]json.RawMessage
		if op.method == "GET" || json.Unmarshal(body, &object) != nil || object == nil {
			return nil, ErrInvalidRequest
		}
	} else if op.method == "PUT" {
		body = json.RawMessage(`{}`)
	}
	var raw []byte
	var err error
	if op.responseClass != "" {
		// Status reads stay usable after a lost answer. An answer that echoes
		// the requested backup set proves the lane in step.
		raw, err = c.legacyReadAuth(ctx, op.responseClass, op.method, path, body, op.auth, func(decoded []byte) probeResult {
			var asked, echo struct {
				ID json.Number `json:"backupSetId"`
			}
			if json.Unmarshal(body, &asked) != nil || asked.ID == "" || json.Unmarshal(decoded, &echo) != nil || echo.ID == "" {
				return probeUnknown
			}
			if echo.ID == asked.ID {
				return probeInStep
			}
			return probeMismatch // another set's answer: never shown
		})
	} else {
		raw, err = c.request(ctx, op.method, path, body, op.auth, false)
	}
	if len(raw) > 0 && strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		_, ids, decodeErr := responseEnvelope(raw, "")
		if decodeErr == nil && len(ids) > 0 {
			return nil, ErrRejected
		}
		if err == nil && decodeErr != nil {
			return nil, decodeErr
		}
	}
	if err != nil {
		return nil, err
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil || decoded == nil {
		return nil, ErrProtocol
	}
	switch decoded.(type) {
	case map[string]any, []any:
	default:
		return nil, ErrProtocol
	}
	if op.requiredField != "" {
		var fields map[string]json.RawMessage
		var value bool
		if json.Unmarshal(raw, &fields) != nil || len(fields[op.requiredField]) == 0 || string(fields[op.requiredField]) == "null" || json.Unmarshal(fields[op.requiredField], &value) != nil {
			return nil, ErrProtocol
		}
	}
	return raw, nil
}
