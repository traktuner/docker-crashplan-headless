package engine

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
)

type LocalArchiveDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Own       bool   `json:"own"`
	Self      bool   `json:"self"`
	ParentID  string `json:"parentId,omitempty"`
	UserID    string `json:"-"`
	Deleted   bool   `json:"deleted"`
	Connected bool   `json:"connected"`
}
type LocalArchivePolicy struct {
	UserID       string               `json:"-"`
	Allowed      bool                 `json:"allowed"`
	OwnDevices   bool                 `json:"ownDevices"`
	CanAttachOwn bool                 `json:"canAttachOwn"`
	SelfID       string               `json:"selfId"`
	Devices      []LocalArchiveDevice `json:"devices"`
}

func (c *Client) LocalArchivePolicy(ctx context.Context) (LocalArchivePolicy, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.request(ctx, "GET", "/v0/GetComputers", nil, true, false)
	if err != nil {
		return LocalArchivePolicy{}, err
	}
	users, ids, err := responseEnvelope(raw, "UsersMessage")
	if err != nil {
		return LocalArchivePolicy{}, err
	}
	if len(ids) > 0 {
		return LocalArchivePolicy{}, ErrRejected
	}
	var u struct {
		ID    json.Number `json:"myUserId"`
		Users []struct {
			ID      json.Number `json:"userId"`
			Allowed *bool       `json:"allowLocalFolders"`
			Own     *bool       `json:"autoOfferSelf"`
		} `json:"users"`
	}
	if json.Unmarshal(users, &u) != nil || !validID(u.ID.String()) {
		return LocalArchivePolicy{}, ErrProtocol
	}
	out := LocalArchivePolicy{UserID: u.ID.String(), Devices: []LocalArchiveDevice{}}
	found := false
	for _, user := range u.Users {
		if user.ID == u.ID {
			if found {
				return out, ErrProtocol
			}
			// Native renderer tests these optional flags with === true.
			out.Allowed = user.Allowed != nil && *user.Allowed
			out.OwnDevices = user.Own != nil && *user.Own
			found = true
		}
	}
	if !found {
		return out, ErrProtocol
	}
	computers, ids, err := responseEnvelope(raw, "ComputersMessage")
	if err != nil {
		return out, err
	}
	if len(ids) > 0 {
		return out, ErrRejected
	}
	var w struct {
		Devices []struct {
			ID         json.Number       `json:"peerId"`
			Name       string            `json:"name"`
			User       json.Number       `json:"userId"`
			Parent     *json.Number      `json:"parentGuid"`
			Deleted    bool              `json:"deleted"`
			Properties map[string]string `json:"properties"`
		} `json:"computers"`
	}
	if json.Unmarshal(computers, &w) != nil || len(w.Devices) > 10000 {
		return out, ErrProtocol
	}
	seen := map[string]bool{}
	for _, d := range w.Devices {
		id := d.ID.String()
		if !validID(id) || seen[id] || len(d.Name) > 4096 {
			return out, ErrProtocol
		}
		seen[id] = true
		item := LocalArchiveDevice{ID: id, Name: d.Name, Own: d.Properties["OWN"] == "true", Self: d.Properties["ME"] == "true", UserID: d.User.String(), Deleted: d.Deleted}
		// The native JSON omits the transient connection timestamp.
		item.Connected = d.Properties["DCODE"] == "CONNECTED"
		if item.Self || d.Parent != nil {
			item.Connected = true
		}
		if d.Parent != nil {
			if !validID(d.Parent.String()) {
				return out, ErrProtocol
			}
			item.ParentID = d.Parent.String()
		}
		if item.Self {
			if out.SelfID != "" {
				return out, ErrProtocol
			}
			out.SelfID = id
		}
		out.Devices = append(out.Devices, item)
	}
	if out.SelfID == "" {
		return out, ErrProtocol
	}
	out.CanAttachOwn = out.OwnDevices
	if !out.OwnDevices {
		for _, device := range out.Devices {
			if device.Own && !device.Self {
				state, err := c.Replacement(ctx)
				if err != nil {
					return out, err
				}
				out.CanAttachOwn = state.InProgress
				break
			}
		}
	}
	return out, nil
}

// LocalArchivePath follows the renderer's computer -> user -> default fallback.
// It exposes only the selected path, never the surrounding configuration.
func (c *Client) LocalArchivePath(ctx context.Context, device LocalArchiveDevice) (string, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.request(ctx, "GET", "/v1/SocialNetworkConfig", nil, true, false)
	if err != nil {
		return "", err
	}
	return c.localArchivePathFromConfig(ctx, device, raw)
}
func (c *Client) localArchivePathFromConfig(ctx context.Context, device LocalArchiveDevice, raw []byte) (string, error) {
	var err error
	type field struct {
		Value *string `json:"value"`
	}
	type entry struct {
		Path field `json:"manifestPath"`
	}
	var w struct {
		Computers struct {
			Map map[string]entry `json:"map"`
		} `json:"computerConfigs"`
		Users struct {
			Map map[string]entry `json:"map"`
		} `json:"userConfigs"`
	}
	if json.Unmarshal(raw, &w) != nil {
		return "", ErrProtocol
	}
	for _, v := range []*string{w.Computers.Map[device.ID].Path.Value, w.Users.Map[device.UserID].Path.Value} {
		if v != nil && *v != "" {
			return localArchivePath(*v)
		}
	}
	raw, err = c.request(ctx, "GET", "/v1/BackupConfig", nil, true, false)
	if err != nil {
		return "", err
	}
	var fallback entry
	if json.Unmarshal(raw, &fallback) != nil || fallback.Path.Value == nil {
		return "", ErrProtocol
	}
	return localArchivePath(*fallback.Path.Value)
}
func localArchivePath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsAny(path, "\x00\r\n") {
		return "", ErrProtocol
	}
	return path, nil
}

type LocalArchiveResult struct {
	State           string `json:"state"`
	Name            string `json:"name,omitempty"`
	Deleted         bool   `json:"deleted,omitempty"`
	DestinationID   string `json:"destinationId,omitempty"`
	JobID           string `json:"jobId,omitempty"`
	ExistingArchive bool   `json:"existingArchive"`
	SourceMissing   bool   `json:"sourceMissing"`
}

// ExecuteLocalArchive keeps archive properties private. validate runs after all
// native reads and immediately before the one native mutation.
func (c *Client) ExecuteLocalArchive(ctx context.Context, action, path, source, target string, validate func(string) error) (LocalArchiveResult, error) {
	ctx = c.operationContext(ctx)
	var method, endpoint, class string
	var body any
	switch action {
	case "create":
		method = "PUT"
		endpoint = "CreateFolderArchiveRequest"
		class = "CreateFolderArchiveResponseMessage"
		body = map[string]string{"path": path}
	case "attach":
		if !validID(source) {
			return LocalArchiveResult{}, ErrInvalidRequest
		}
		query := url.Values{"path": {path}, "deviceGuid": {source}}
		raw, err := c.request(ctx, "GET", "/v1/LocalArchive?"+query.Encode(), nil, true, false)
		if err != nil {
			return LocalArchiveResult{}, err
		}
		raw, err = restoreResponse(raw, err, "")
		if err != nil {
			return LocalArchiveResult{}, err
		}
		var archive struct {
			Properties map[string]string `json:"archiveProperties"`
		}
		if json.Unmarshal(raw, &archive) != nil || len(archive.Properties) == 0 || len(archive.Properties) > 4096 || archive.Properties["guid"] != source {
			return LocalArchiveResult{}, ErrProtocol
		}
		location := archive.Properties["location"]
		if filepath.Clean(location) != filepath.Join(path, source) || validate(filepath.Clean(location)) != nil {
			return LocalArchiveResult{}, ErrRestorePath
		}
		method = "POST"
		endpoint = "AttachArchiveRequest"
		class = "AttachArchiveResponseMessage"
		body = map[string]any{"archiveProperties": archive.Properties}
		if target != "" {
			body.(map[string]any)["childGuid"] = target
		}
	case "move":
		if !validID(source) || !validID(target) {
			return LocalArchiveResult{}, ErrInvalidRequest
		}
		method = "POST"
		endpoint = "MoveJobRequest"
		class = "CopyJobResponseMessage"
		body = map[string]any{"newArchiveLocation": path, "job": map[string]string{"sourceGuid": source, "targetGuid": target}}
	default:
		return LocalArchiveResult{}, ErrInvalidRequest
	}
	if validate(path) != nil {
		return LocalArchiveResult{}, ErrRestorePath
	}
	payload, _ := json.Marshal(body)
	raw, err := c.legacyRequest(ctx, class, method, "/v0/"+endpoint, payload, true, true)
	raw, err = actionResponse(raw, err, class)
	if err != nil {
		return LocalArchiveResult{}, err
	}
	var wire struct {
		ID         *json.Number               `json:"guid"`
		Child      *json.Number               `json:"childGuid"`
		Properties map[string]json.RawMessage `json:"archiveProperties"`
		Missing    bool                       `json:"sourceMissing"`
		Job        *struct {
			ID       json.Number `json:"id"`
			Error    *string     `json:"error"`
			Done     bool        `json:"done"`
			Canceled bool        `json:"canceled"`
		} `json:"job"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return LocalArchiveResult{}, ErrIndeterminate
	}
	out := LocalArchiveResult{State: "accepted", ExistingArchive: len(wire.Properties) > 0, SourceMissing: wire.Missing}
	if out.ExistingArchive {
		out.State = "needs-attach"
		return out, nil
	}
	id := wire.ID
	if action == "attach" {
		id = wire.Child
	}
	if id != nil {
		if !validID(id.String()) {
			return out, ErrIndeterminate
		}
		out.DestinationID = id.String()
	}
	if action == "create" && out.DestinationID == "" {
		return out, ErrIndeterminate
	}
	if action == "move" {
		if wire.Job == nil {
			if !wire.Missing {
				return out, ErrIndeterminate
			}
			out.State = "source-missing"
			return out, nil
		}
		if wire.Job.Error != nil && *wire.Job.Error != "" {
			return out, ErrRejected
		}
		if !validID(wire.Job.ID.String()) || wire.Job.Canceled {
			return out, ErrIndeterminate
		}
		out.JobID = wire.Job.ID.String()
		if wire.Job.Done {
			out.State = "completed"
		}
	}
	return out, nil
}
