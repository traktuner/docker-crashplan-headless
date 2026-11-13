package engine

import (
	"context"
	"encoding/json"
	"net/url"
)

type ArchiveSet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) ArchiveSets(ctx context.Context, source, target string) ([]ArchiveSet, error) {
	ctx = c.operationContext(ctx)
	if !validID(source) || !validID(target) {
		return nil, ErrInvalidRequest
	}
	query := url.Values{"sourceGuid": {source}, "destinationGuid": {target}}
	raw, err := c.request(ctx, "GET", "/v1/BackupSets?"+query.Encode(), nil, true, false)
	if err != nil {
		return nil, err
	}
	var wire []struct {
		ID   json.Number `json:"id"`
		Name string      `json:"name"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire == nil || len(wire) > 10000 {
		return nil, ErrProtocol
	}
	out := make([]ArchiveSet, 0, len(wire))
	seen := map[string]bool{}
	for _, set := range wire {
		id := set.ID.String()
		if !validID(id) || seen[id] || len(set.Name) > 4096 {
			return nil, ErrProtocol
		}
		seen[id] = true
		out = append(out, ArchiveSet{ID: id, Name: set.Name})
	}
	return out, nil
}

type Device struct {
	ArchiveType     string `json:"archiveType,omitempty"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Source          bool   `json:"source"`
	Target          bool   `json:"target"`
	AvailableTarget bool   `json:"availableTarget"`
	Self            bool   `json:"self"`
	Blocked         bool   `json:"blocked"`
	Deleted         bool   `json:"deleted"`
}

func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.legacyRequest(ctx, "ComputersMessage", "GET", "/v0/GetComputers", nil, true, false)
	raw, err = restoreResponse(raw, err, "ComputersMessage")
	if err != nil {
		return nil, err
	}
	var wire struct {
		Computers *[]struct {
			ID          json.Number                `json:"peerId"`
			Name        string                     `json:"name"`
			ArchiveType string                     `json:"defaultArchiveType"`
			Source      bool                       `json:"source"`
			Target      bool                       `json:"target"`
			Available   bool                       `json:"availableTarget"`
			Blocked     bool                       `json:"blocked"`
			Deleted     bool                       `json:"deleted"`
			Properties  map[string]json.RawMessage `json:"properties"`
		} `json:"computers"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Computers == nil || len(*wire.Computers) > 10000 {
		return nil, ErrProtocol
	}
	devices := make([]Device, 0, len(*wire.Computers))
	seen := map[string]bool{}
	for _, item := range *wire.Computers {
		id := item.ID.String()
		if !validID(id) || seen[id] || len(item.Name) > 4096 {
			return nil, ErrProtocol
		}
		seen[id] = true
		self := string(item.Properties["ME"]) == `"true"` || string(item.Properties["ME"]) == `true`
		archiveType := ""
		if item.ArchiveType == "ARCHIVE_V1" || item.ArchiveType == "ARCHIVE_V3" {
			archiveType = item.ArchiveType
		}
		devices = append(devices, Device{ArchiveType: archiveType, ID: id, Name: item.Name, Source: item.Source, Target: item.Target, AvailableTarget: item.Available, Self: self, Blocked: item.Blocked, Deleted: item.Deleted})
	}
	return devices, nil
}
