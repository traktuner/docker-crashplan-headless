package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

var backupSetDefinition = settingsArea{Fields: []SettingSpec{
	{"name", "Backup set name", "text", ""},
	{"priority", "Priority", "integer", ""},
	{"scanInterval", "Full scan interval", "long", "ms"},
	{"scanTime", "Full scan start time", "time", ""},
	{"backupRunWindow", "Backup schedule", "window", ""},
	{"compression", "Compression", "compression", ""},
	{"encryptionEnabled", "Encryption", "boolean", ""},
	{"backupOpenFiles", "Back up open files", "boolean", ""},
	{"dataDeDuplication", "Deduplication", "dedup", ""},
	{"dataDeDupAutoMaxFileSize", "Deduplication file-size threshold", "long", "bytes"},
	{"dataDeDupAutoMaxFileSizeForWan", "WAN deduplication file-size threshold", "long", "bytes"},
}}

type SelectionPath struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Selected  bool   `json:"selected"`
}
type SetDestination struct {
	ID     string `json:"id"`
	Locked bool   `json:"locked"`
}
type BackupSet struct {
	ID                 string           `json:"id"`
	Locked             bool             `json:"locked"`
	Fields             []Setting        `json:"fields"`
	Paths              []SelectionPath  `json:"paths"`
	PathsLocked        bool             `json:"pathsLocked"`
	Excludes           []Pattern        `json:"excludes"`
	ExcludesLocked     bool             `json:"excludesLocked"`
	Destinations       []SetDestination `json:"destinations"`
	DestinationsLocked bool             `json:"destinationsLocked"`
	Retention          []Setting        `json:"retention"`
}
type SetResult struct {
	Set      BackupSet       `json:"set"`
	Complete bool            `json:"complete"`
	Applied  map[string]bool `json:"applied"`
}
type setRecord struct {
	Set         BackupSet
	Raw         map[string]json.RawMessage
	PathEntries []map[string]json.RawMessage
	Rules       map[string]json.RawMessage
}

func validSelectionPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 65536 && !strings.ContainsAny(path, "\x00\r\n")
}
func decodeSet(raw []byte) (setRecord, error) {
	var wire struct {
		ID          json.Number `json:"id"`
		Locked      bool        `json:"locked"`
		BackupPaths map[string]struct {
			Value  json.RawMessage `json:"value"`
			Locked bool            `json:"locked"`
		} `json:"backupPaths"`
		Destinations struct {
			Locked bool `json:"locked"`
			Map    map[string]struct {
				ID      json.Number `json:"guid"`
				Locked  bool        `json:"locked"`
				Removed bool        `json:"removed"`
			} `json:"map"`
		} `json:"destinations"`
		Retention json.RawMessage `json:"retentionPolicy"`
	}
	if json.Unmarshal(raw, &wire) != nil || !validID(wire.ID.String()) {
		return setRecord{}, ErrProtocol
	}
	fields, err := projectSettings(raw, backupSetDefinition)
	if err != nil {
		return setRecord{}, err
	}
	for i := range fields {
		fields[i].Locked = fields[i].Locked || wire.Locked
	}
	r := setRecord{Set: BackupSet{ID: wire.ID.String(), Locked: wire.Locked, Fields: fields, Paths: []SelectionPath{}, Destinations: []SetDestination{}, Excludes: []Pattern{}, Retention: []Setting{}}, Rules: map[string]json.RawMessage{}}
	if json.Unmarshal(raw, &r.Raw) != nil {
		return r, ErrProtocol
	}
	pathSet, ok := wire.BackupPaths["pathSet"]
	if !ok {
		return r, ErrProtocol
	}
	var paths struct {
		Paths []map[string]json.RawMessage `json:"paths"`
	}
	if json.Unmarshal(pathSet.Value, &paths) != nil || paths.Paths == nil || len(paths.Paths) > 10000 {
		return r, ErrProtocol
	}
	r.PathEntries = paths.Paths
	seen := map[string]bool{}
	for _, entry := range paths.Paths {
		body, _ := json.Marshal(entry)
		var path SelectionPath
		if json.Unmarshal(body, &path) != nil {
			return r, ErrProtocol
		}
		originalPath := path.Path
		path.Path = filepath.Clean(path.Path)
		if originalPath != path.Path && (!path.Directory || originalPath != path.Path+"/") {
			return r, ErrProtocol
		}
		if !validSelectionPath(path.Path) || seen[path.Path] {
			return r, ErrProtocol
		}
		seen[path.Path] = true
		r.Set.Paths = append(r.Set.Paths, path)
	}
	r.Set.PathsLocked = pathSet.Locked || wire.Locked
	for _, name := range []string{"pathSet", "userExcludes"} {
		field, ok := wire.BackupPaths[name]
		if !ok {
			return r, ErrProtocol
		}
		r.Rules[name] = field.Value
	}
	excludes := wire.BackupPaths["userExcludes"]
	if json.Unmarshal(excludes.Value, &r.Set.Excludes) != nil || r.Set.Excludes == nil {
		return r, ErrProtocol
	}
	r.Set.ExcludesLocked = excludes.Locked || wire.Locked
	r.Set.DestinationsLocked = wire.Destinations.Locked || wire.Locked
	for id, item := range wire.Destinations.Map {
		if !validID(id) || item.ID.String() != id {
			return r, ErrProtocol
		}
		if !item.Removed {
			r.Set.Destinations = append(r.Set.Destinations, SetDestination{ID: id, Locked: item.Locked || r.Set.DestinationsLocked})
		}
	}
	sort.Slice(r.Set.Destinations, func(i, j int) bool { return r.Set.Destinations[i].ID < r.Set.Destinations[j].ID })
	if len(wire.Retention) > 0 && string(wire.Retention) != "null" {
		r.Set.Retention, err = projectSettings(wire.Retention, settingsArea{Fields: []SettingSpec{
			{"backupFrequency", "Backup frequency", "long", "ms"},
			{"keepDeleted", "Keep deleted files", "boolean", ""},
			{"keepDeletedMinutes", "Deleted-file retention", "integer", "minutes"},
			{"versionLastWeekInterval", "Version interval: last week", "integer", "minutes"},
			{"versionLastNinetyDaysInterval", "Version interval: last 90 days", "integer", "minutes"},
			{"versionLastYearInterval", "Version interval: last year", "integer", "minutes"},
			{"versionPrevYearsInterval", "Version interval: previous years", "integer", "minutes"},
		}})
		if err != nil {
			return r, err
		}
		for i := range r.Set.Retention {
			r.Set.Retention[i].Locked = true
		}
	}
	return r, nil
}
func (c *Client) setRecord(ctx context.Context, id string) (setRecord, error) {
	if !validID(id) {
		return setRecord{}, ErrInvalidRequest
	}
	raw, err := c.request(ctx, "GET", "/v1/BackupSet/"+id, nil, true, false)
	if err != nil {
		return setRecord{}, err
	}
	r, err := decodeSet(raw)
	if err != nil {
		return r, err
	}
	if r.Set.ID != id {
		return r, ErrProtocol
	}
	return r, nil
}
func (c *Client) BackupSet(ctx context.Context, id string) (BackupSet, error) {
	ctx = c.operationContext(ctx)
	r, err := c.setRecord(ctx, id)
	return r.Set, err
}
func (c *Client) BackupSets(ctx context.Context) ([]BackupSet, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.request(ctx, "GET", "/v1/BackupSet", nil, true, false)
	if err != nil {
		return nil, err
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil || len(entries) > 1000 {
		return nil, ErrProtocol
	}
	sets := make([]BackupSet, 0, len(entries))
	seen := map[string]bool{}
	for _, raw := range entries {
		r, e := decodeSet(raw)
		if e != nil {
			return nil, e
		}
		if seen[r.Set.ID] {
			return nil, ErrProtocol
		}
		seen[r.Set.ID] = true
		sets = append(sets, r.Set)
	}
	return sets, nil
}
func (c *Client) putSet(ctx context.Context, id string, body any) (BackupSet, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return BackupSet{}, ErrInvalidRequest
	}
	response, err := c.request(ctx, "PUT", "/v1/BackupSet/"+id, raw, true, true)
	if err != nil {
		return BackupSet{}, err
	}
	if len(bytes.TrimSpace(response)) > 0 {
		if _, ids, e := responseEnvelope(response, ""); e != nil || len(ids) > 0 {
			return BackupSet{}, ErrIndeterminate
		}
	}
	after, err := c.BackupSet(ctx, id)
	if err != nil {
		return BackupSet{}, errors.Join(ErrIndeterminate, err)
	}
	return after, nil
}
func (c *Client) PatchBackupSet(ctx context.Context, id string, changes map[string]json.RawMessage) (SetResult, error) {
	ctx = c.operationContext(ctx)
	if !validID(id) || len(changes) == 0 || len(changes) > len(backupSetDefinition.Fields) {
		return SetResult{}, ErrInvalidRequest
	}
	values := map[string]json.RawMessage{}
	for name, raw := range changes {
		found := false
		for _, spec := range backupSetDefinition.Fields {
			if name == spec.Name {
				value, err := normalizeSetting(spec, raw)
				if err != nil {
					return SetResult{}, err
				}
				if name == "name" && string(value) == `""` {
					return SetResult{}, ErrInvalidRequest
				}
				values[name] = value
				found = true
				break
			}
		}
		if !found {
			return SetResult{}, ErrInvalidRequest
		}
	}
	before, err := c.BackupSet(ctx, id)
	if err != nil {
		return SetResult{}, err
	}
	for name := range values {
		for _, field := range before.Fields {
			if name == field.Name {
				if !field.Available {
					return SetResult{}, ErrProtocol
				}
				if field.Locked {
					return SetResult{}, ErrSettingLocked
				}
			}
		}
	}
	patch := map[string]any{}
	for name, value := range values {
		patch[name] = map[string]json.RawMessage{"value": value}
	}
	after, err := c.putSet(ctx, id, patch)
	if err != nil {
		return SetResult{}, err
	}
	result := SetResult{Set: after, Complete: true, Applied: map[string]bool{}}
	for name, want := range values {
		for _, field := range after.Fields {
			if name == field.Name {
				result.Applied[name] = bytes.Equal(want, field.Value)
			}
		}
		if !result.Applied[name] {
			result.Complete = false
		}
	}
	return result, nil
}
func (c *Client) ChangeSelection(ctx context.Context, id string, changes []SelectionPath) (SetResult, error) {
	ctx = c.operationContext(ctx)
	if !validID(id) || len(changes) == 0 || len(changes) > 1000 {
		return SetResult{}, ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, path := range changes {
		if !validSelectionPath(path.Path) || seen[path.Path] {
			return SetResult{}, ErrInvalidRequest
		}
		seen[path.Path] = true
	}
	before, err := c.setRecord(ctx, id)
	if err != nil {
		return SetResult{}, err
	}
	if before.Set.PathsLocked {
		return SetResult{}, ErrSettingLocked
	}
	entries := map[string]map[string]json.RawMessage{}
	for i, path := range before.Set.Paths {
		entries[path.Path] = before.PathEntries[i]
	}
	for _, change := range changes {
		if change.Directory {
			for path := range entries {
				if path != change.Path && (change.Path == "/" || strings.HasPrefix(path, change.Path+"/")) {
					delete(entries, path)
				}
			}
		}
		entry, ok := entries[change.Path]
		if !ok {
			entry = map[string]json.RawMessage{}
		}
		entry["path"], _ = json.Marshal(change.Path)
		entry["directory"], _ = json.Marshal(change.Directory)
		entry["selected"], _ = json.Marshal(change.Selected)
		entries[change.Path] = entry
	}
	if len(entries) > 10000 {
		return SetResult{}, ErrInvalidRequest
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	paths := make([]map[string]json.RawMessage, 0, len(keys))
	for _, key := range keys {
		paths = append(paths, entries[key])
	}
	after, err := c.putSet(ctx, id, map[string]any{"backupPaths": map[string]any{"pathSet": map[string]any{"value": map[string]any{"paths": paths}}}})
	if err != nil {
		return SetResult{}, err
	}
	wanted := map[string]SelectionPath{}
	for _, entry := range paths {
		raw, _ := json.Marshal(entry)
		var p SelectionPath
		json.Unmarshal(raw, &p)
		wanted[p.Path] = p
	}
	ok := len(after.Paths) == len(wanted)
	for _, p := range after.Paths {
		if want, found := wanted[p.Path]; !found || want != p {
			ok = false
		}
	}
	return SetResult{Set: after, Complete: ok, Applied: map[string]bool{"selection": ok}}, nil
}
func (c *Client) CreateBackupSet(ctx context.Context, name string) (BackupSet, error) {
	ctx = c.operationContext(ctx)
	if strings.TrimSpace(name) == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") {
		return BackupSet{}, ErrInvalidRequest
	}
	body, _ := json.Marshal(map[string]any{"name": map[string]string{"value": name}, "backupPaths": map[string]any{"pathSet": map[string]any{"value": map[string]any{"paths": []any{}}}}, "destinations": map[string]any{"map": map[string]any{}}})
	raw, err := c.request(ctx, "POST", "/v1/BackupSet", body, true, true)
	if err != nil {
		return BackupSet{}, err
	}
	r, err := decodeSet(raw)
	if err != nil {
		return BackupSet{}, errors.Join(ErrIndeterminate, err)
	}
	return r.Set, nil
}
func (c *Client) DeleteBackupSet(ctx context.Context, id string) error {
	ctx = c.operationContext(ctx)
	if !validID(id) || id == "1" {
		return ErrInvalidRequest
	}
	set, err := c.BackupSet(ctx, id)
	if err != nil {
		return err
	}
	if set.Locked {
		return ErrSettingLocked
	}
	_, err = c.request(ctx, "DELETE", "/v1/BackupSet/"+id, nil, true, true)
	if err != nil {
		return err
	}
	sets, err := c.BackupSets(ctx)
	if err != nil {
		return errors.Join(ErrIndeterminate, err)
	}
	for _, s := range sets {
		if s.ID == id {
			return ErrRejected
		}
	}
	return nil
}
