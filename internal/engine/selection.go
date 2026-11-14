package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
)

type SourceQuery struct {
	Parent string `json:"parent"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}
type SourceItem struct {
	Path           string `json:"path"`
	Directory      bool   `json:"directory"`
	Exists         bool   `json:"exists"`
	Alias          bool   `json:"alias"`
	Selection      string `json:"selectionCode"`
	Hidden         bool   `json:"hidden"`
	ExcludedSystem bool   `json:"excludedForSystem"`
	ExcludedUser   bool   `json:"excludedForUser"`
	ExcludedHost   bool   `json:"excludedForHost"`
}
type SourcePage struct {
	Items  []SourceItem `json:"items"`
	Total  int          `json:"total"`
	Offset int          `json:"offset"`
	Parent string       `json:"parent"`
}

func (c *Client) BrowseSource(ctx context.Context, id string, q SourceQuery) (SourcePage, error) {
	ctx = c.operationContext(ctx)
	if q.Limit == 0 {
		q.Limit = 200
	}
	if !validID(id) || q.Offset < 0 || q.Offset > 2147483647 || q.Limit < 1 || q.Limit > 1000 || (q.Parent != "" && !validSelectionPath(q.Parent)) {
		return SourcePage{}, ErrInvalidRequest
	}
	set, err := c.setRecord(ctx, id)
	if err != nil {
		return SourcePage{}, err
	}
	raw, err := c.request(ctx, "GET", "/v1/BackupConfig", nil, true, false)
	if err != nil {
		return SourcePage{}, err
	}
	var global map[string]struct {
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &global) != nil {
		return SourcePage{}, ErrProtocol
	}
	for _, key := range []string{"systemExcludes", "hostSystemExcludes"} {
		value, ok := global[key]
		if !ok || len(value.Value) == 0 {
			return SourcePage{}, ErrProtocol
		}
		set.Rules[key] = value.Value
	}
	body := map[string]any{"backupSetId": id, "rules": set.Rules, "startIndex": q.Offset, "batchSize": q.Limit}
	path, class := "/v0/GetRootPaths", "GetRootPathsResponseMessage"
	if q.Parent != "" {
		path = "/v0/GetChildrenPaths"
		class = "GetChildrenPathsResultMessage"
		body["parentPath"] = map[string]string{"path": q.Parent}
	}
	encoded, _ := json.Marshal(body)
	raw, err = c.legacyRead(ctx, class, "POST", path, encoded, func(decoded []byte) probeResult {
		if q.Parent == "" {
			return probeInStep // roots do not depend on the request
		}
		var echo struct {
			Parent struct {
				Path string `json:"path"`
			} `json:"parentPath"`
		}
		if json.Unmarshal(decoded, &echo) == nil && filepath.Clean(echo.Parent.Path) == filepath.Clean(q.Parent) {
			return probeInStep
		}
		return probeMismatch
	})
	raw, err = restoreResponse(raw, err, class)
	if err != nil {
		return SourcePage{}, err
	}
	var response struct {
		Parent struct {
			Path string `json:"path"`
		} `json:"parentPath"`
		Children *[]SourceItem `json:"children"`
		Roots    *[]SourceItem `json:"roots"`
		Total    *int          `json:"totalCount"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return SourcePage{}, ErrProtocol
	}
	items := response.Children
	total := 0
	if q.Parent == "" {
		items = response.Roots
		if items != nil {
			total = len(*items)
		}
	} else {
		if response.Total == nil || filepath.Clean(response.Parent.Path) != filepath.Clean(q.Parent) {
			return SourcePage{}, ErrProtocol
		}
		total = *response.Total
	}
	if items == nil || len(*items) > q.Limit || total < 0 {
		return SourcePage{}, ErrProtocol
	}
	seen := map[string]bool{}
	for i := range *items {
		item := &(*items)[i]
		// Native directory paths carry a trailing slash. The product keeps one
		// canonical path and retains the separate directory flag.
		clean := filepath.Clean(item.Path)
		if !validSelectionPath(clean) || seen[clean] {
			return SourcePage{}, ErrProtocol
		}
		if q.Parent != "" && filepath.Dir(clean) != filepath.Clean(q.Parent) {
			return SourcePage{}, ErrProtocol
		}
		seen[clean] = true
		item.Path = clean
		switch item.Selection {
		case "SELECTED", "NOT_SELECTED", "EXCLUDED":
		default:
			return SourcePage{}, ErrProtocol
		}
	}
	return SourcePage{Items: *items, Total: total, Offset: q.Offset, Parent: q.Parent}, nil
}
func (c *Client) SetExcludes(ctx context.Context, id string, patterns []Pattern) (SetResult, error) {
	ctx = c.operationContext(ctx)
	if !validID(id) || patterns == nil {
		return SetResult{}, ErrInvalidRequest
	}
	raw, err := json.Marshal(patterns)
	if err != nil {
		return SetResult{}, ErrInvalidRequest
	}
	value, err := normalizeSetting(SettingSpec{Kind: "patterns"}, raw)
	if err != nil {
		return SetResult{}, err
	}
	before, err := c.BackupSet(ctx, id)
	if err != nil {
		return SetResult{}, err
	}
	if before.ExcludesLocked {
		return SetResult{}, ErrSettingLocked
	}
	after, err := c.putSet(ctx, id, map[string]any{"backupPaths": map[string]any{"userExcludes": map[string]json.RawMessage{"value": value}}})
	if err != nil {
		return SetResult{}, err
	}
	observed, _ := json.Marshal(after.Excludes)
	ok := string(observed) == string(value)
	return SetResult{Set: after, Complete: ok, Applied: map[string]bool{"excludes": ok}}, nil
}
func (c *Client) SetDestination(ctx context.Context, id, target string, attached bool) (SetResult, error) {
	ctx = c.operationContext(ctx)
	if !validID(id) || !validID(target) {
		return SetResult{}, ErrInvalidRequest
	}
	before, err := c.BackupSet(ctx, id)
	if err != nil {
		return SetResult{}, err
	}
	if before.DestinationsLocked {
		return SetResult{}, ErrSettingLocked
	}
	for _, dest := range before.Destinations {
		if dest.ID == target && dest.Locked {
			return SetResult{}, ErrSettingLocked
		}
	}
	after, err := c.putSet(ctx, id, map[string]any{"destinations": map[string]any{"map": map[string]any{target: map[string]any{"guid": target, "removed": !attached}}}})
	if err != nil {
		return SetResult{}, err
	}
	found := false
	for _, dest := range after.Destinations {
		if dest.ID == target {
			found = true
		}
	}
	ok := found == attached
	return SetResult{Set: after, Complete: ok, Applied: map[string]bool{"destination": ok}}, nil
}
