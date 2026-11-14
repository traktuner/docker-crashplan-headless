package engine

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"strings"
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
	// Owned is another computer of the signed-in account (property OWN).
	Owned bool `json:"owned"`
	// Cloud is a CrashPlan cloud server (property CPC), never a restore source.
	Cloud bool `json:"cloud"`
	// Connected is computer_model.isConnected: self, a child (local folder
	// destination, parentGuid set) or properties.DCODE == CONNECTED.
	Connected bool `json:"connected"`
	// DisconnectCode is properties.DCODE while not connected, omitted when unknown.
	DisconnectCode string `json:"disconnectCode,omitempty"`
	// UsedBytes is the destination's backupStats.remoteManifestSize (DeviceDetails only).
	UsedBytes string `json:"usedBytes,omitempty"`
	// QuotaBytes is offeredBytesOnTarget; "-1" means automatic.
	QuotaBytes string `json:"quotaBytes,omitempty"`
	// Location is the folder of a local destination's archive (DeviceDetails only).
	Location string `json:"location,omitempty"`
	parentID string
	userID   string
}

func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	ctx = c.operationContext(ctx)
	revision, epoch := c.SessionRevision(), c.targetsEpochNow()
	raw, err := c.legacyRead(ctx, "ComputersMessage", "GET", "/v0/GetComputers", nil, anyAnswer)
	raw, err = restoreResponse(raw, err, "ComputersMessage")
	if err != nil {
		return nil, err
	}
	devices, err := parseDevices(raw)
	if err != nil {
		return nil, err
	}
	// A fresh list also refreshes the targets progress polling reuses.
	c.storeTargets(revision, epoch, devices)
	return devices, nil
}

// DeviceDetails is the device list with what the stock Destinations
// preferences show per destination: space used (remoteManifestSize from the
// shared progress reading) and the archive folder of a local destination.
// Both are best effort and omitted when unknown; the list itself is exact.
func (c *Client) DeviceDetails(ctx context.Context) ([]Device, error) {
	ctx = c.operationContext(ctx)
	devices, err := c.Devices(ctx)
	if err != nil {
		return nil, err
	}
	used := map[string]string{}
	if progress, err := c.BackupProgress(ctx); err == nil {
		for _, p := range progress {
			if p.Error == "" && p.usedBytes != "" {
				used[p.TargetID] = p.usedBytes
			}
		}
	}
	self := ""
	for _, d := range devices {
		if d.Self {
			self = d.ID
		}
	}
	var social []byte
	for i := range devices {
		d := &devices[i]
		if !d.Target || d.Self || d.Deleted {
			continue
		}
		d.UsedBytes = used[d.ID]
		if d.parentID == "" || self == "" {
			continue
		}
		if social == nil {
			if social, err = c.request(ctx, "GET", "/v1/SocialNetworkConfig", nil, true, false); err != nil {
				social = []byte{}
				continue
			}
		}
		if len(social) == 0 {
			continue
		}
		if base, err := c.localArchivePathFromConfig(ctx, LocalArchiveDevice{ID: d.ID, UserID: d.userID}, social); err == nil {
			d.Location = path.Join(base, self)
		}
	}
	return devices, nil
}

func parseDevices(raw []byte) ([]Device, error) {
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
			Parent      json.RawMessage            `json:"parentGuid"`
			User        json.RawMessage            `json:"userId"`
			Offered     json.RawMessage            `json:"offeredBytesOnTarget"`
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
		flag := func(name string) bool {
			return string(item.Properties[name]) == `"true"` || string(item.Properties[name]) == `true`
		}
		self := flag("ME")
		archiveType := ""
		if item.ArchiveType == "ARCHIVE_V1" || item.ArchiveType == "ARCHIVE_V3" {
			archiveType = item.ArchiveType
		}
		device := Device{ArchiveType: archiveType, ID: id, Name: item.Name, Source: item.Source, Target: item.Target, AvailableTarget: item.Available, Self: self, Blocked: item.Blocked, Deleted: item.Deleted, Owned: flag("OWN"), Cloud: flag("CPC")}
		if len(item.Parent) > 0 && string(item.Parent) != "null" {
			parent := decimal(item.Parent)
			if !validID(parent) {
				return nil, ErrProtocol
			}
			device.parentID = parent
		}
		device.userID = decimal(item.User)
		code := disconnectCode(item.Properties["DCODE"])
		device.Connected = self || device.parentID != "" || code == "CONNECTED"
		if !device.Connected {
			device.DisconnectCode = code
		}
		if quota := signedBytes(item.Offered); quota != "" && device.Target && !self {
			device.QuotaBytes = quota
		}
		devices = append(devices, device)
	}
	return devices, nil
}

// disconnectCode accepts only enum-like DCODE values (CONNECTED, NEW_VERSION, ...).
func disconnectCode(raw json.RawMessage) string {
	code := text(raw)
	if code == "" || len(code) > 64 {
		return ""
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && r != '_' && (r < '0' || r > '9') {
			return ""
		}
	}
	return code
}

// signedBytes accepts a non-negative byte count or the stock "-1" (automatic).
func signedBytes(raw json.RawMessage) string {
	if s := strings.Trim(strings.TrimSpace(string(raw)), `"`); s == "-1" {
		return "-1"
	}
	return decimal(raw)
}
