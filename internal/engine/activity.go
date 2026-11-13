package engine

import (
	"context"
	"encoding/json"
	"strings"
)

type BackupControl struct {
	BackupSetID     string `json:"backupSetId,omitempty"`
	PlanID          string `json:"planUid,omitempty"`
	DestinationID   string `json:"destinationGuid,omitempty"`
	DurationMinutes string `json:"durationMinutes,omitempty"`
}

func (c *Client) BackupControl(ctx context.Context, action string, in BackupControl) ([]string, error) {
	ctx = c.operationContext(ctx)
	count := 0
	for _, id := range []string{in.BackupSetID, in.PlanID, in.DestinationID} {
		if id != "" {
			if !validID(id) {
				return nil, ErrInvalidRequest
			}
			count++
		}
	}
	if count > 1 || (in.DurationMinutes != "" && !validID(in.DurationMinutes)) {
		return nil, ErrInvalidRequest
	}
	name := "PauseBackupRequest"
	class := "PauseBackupResponseMessage"
	if action == "resume" {
		if in.DurationMinutes != "" {
			return nil, ErrInvalidRequest
		}
		name = "ResumeBackupRequest"
		class = "ResumeBackupResponseMessage"
	} else if action != "pause" {
		return nil, ErrInvalidRequest
	}
	body, _ := json.Marshal(in)
	raw, err := c.legacyRequest(ctx, class, "POST", "/v0/"+name, body, true, true)
	raw, err = restoreResponse(raw, err, class)
	if err != nil {
		return nil, err
	}
	var result struct {
		Sets  *[]json.Number `json:"backupSetIds"`
		Error *string        `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, ErrIndeterminate
	}
	if result.Error != nil && *result.Error != "" {
		return nil, ErrRejected
	}
	if result.Sets == nil || len(*result.Sets) > 10000 {
		return nil, ErrIndeterminate
	}
	sets := make([]string, 0, len(*result.Sets))
	for _, id := range *result.Sets {
		if !validID(id.String()) {
			return nil, ErrProtocol
		}
		sets = append(sets, id.String())
	}
	return sets, nil
}

type Alert struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Title      string `json:"title,omitempty"`
	Message    string `json:"message,omitempty"`
	Flags      int    `json:"flags"`
	Expiration string `json:"expiration"`
}

func (c *Client) Alerts(ctx context.Context) ([]Alert, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.legacyRequest(ctx, "AlertListMessage", "GET", "/v0/AlertRequest", nil, true, false)
	raw, err = restoreResponse(raw, err, "AlertListMessage")
	if err != nil {
		return nil, err
	}
	var result struct {
		Alerts *[]struct {
			Name       string      `json:"name"`
			GUID       string      `json:"guid"`
			Title      string      `json:"title"`
			Message    string      `json:"message"`
			Flags      int         `json:"flags"`
			Expiration json.Number `json:"expiration"`
		} `json:"alerts"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Alerts == nil || len(*result.Alerts) > 10000 {
		return nil, ErrProtocol
	}
	out := make([]Alert, 0, len(*result.Alerts))
	seen := map[string]bool{}
	for _, item := range *result.Alerts {
		id := item.Name
		if item.Name == "CustomAlert" {
			id = item.GUID
		}
		if id == "" || len(id) > 4096 || seen[id] || strings.ContainsAny(id, "\x00\r\n") || len(item.Title) > 65536 || len(item.Message) > 65536 {
			return nil, ErrProtocol
		}
		if _, err := item.Expiration.Int64(); err != nil {
			return nil, ErrProtocol
		}
		seen[id] = true
		out = append(out, Alert{ID: id, Name: item.Name, Title: item.Title, Message: item.Message, Flags: item.Flags, Expiration: item.Expiration.String()})
	}
	return out, nil
}
func (c *Client) DismissAlert(ctx context.Context, id string) error {
	ctx = c.operationContext(ctx)
	alerts, err := c.Alerts(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, alert := range alerts {
		if alert.ID == id {
			found = true
			break
		}
	}
	if !found {
		return ErrInvalidRequest
	}
	body, _ := json.Marshal(map[string]string{"value": id})
	raw, err := c.request(ctx, "PUT", "/v0/AlertRemove", body, true, true)
	if len(raw) > 0 {
		_, err = restoreResponse(raw, err, "")
	}
	if err != nil {
		return err
	}
	after, err := c.Alerts(ctx)
	if err != nil {
		return ErrIndeterminate
	}
	for _, alert := range after {
		if alert.ID == id {
			return ErrIndeterminate
		}
	}
	return nil
}

type HistoryEntry struct {
	Level     string `json:"level"`
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
}

func (c *Client) History(ctx context.Context) ([]HistoryEntry, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.legacyRequest(ctx, "GetHistoryLogResponseMessage", "PUT", "/v0/GetHistoryLog", []byte(`{}`), true, false)
	raw, err = restoreResponse(raw, err, "GetHistoryLogResponseMessage")
	if err != nil {
		return nil, err
	}
	var result struct {
		Messages *[]struct {
			Level     json.RawMessage `json:"level"`
			Timestamp json.Number     `json:"timestamp"`
			Message   string          `json:"message"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Messages == nil || len(*result.Messages) > 50000 {
		return nil, ErrProtocol
	}
	out := make([]HistoryEntry, 0, len(*result.Messages))
	for _, entry := range *result.Messages {
		var level string
		if json.Unmarshal(entry.Level, &level) != nil || len(level) != 1 || len(entry.Message) > 65536 {
			return nil, ErrProtocol
		}
		if _, err := entry.Timestamp.Int64(); err != nil {
			return nil, ErrProtocol
		}
		out = append(out, HistoryEntry{Level: level, Timestamp: entry.Timestamp.String(), Message: entry.Message})
	}
	return out, nil
}

type BackupSummary struct {
	Activities     int    `json:"activities"`
	SendRate       string `json:"sendRateBytesPerSecond"`
	RemainingBytes string `json:"remainingBytes"`
	RemainingFiles int    `json:"remainingFiles"`
	FilesBackedUp  int    `json:"filesBackedUp"`
	LastBackup     string `json:"lastBackup"`
	Status         string `json:"status"`
	SystemBusy     bool   `json:"systemBusy"`
	BatteryBlocked bool   `json:"batteryBlocked"`
}

func (c *Client) BackupSummary(ctx context.Context) (BackupSummary, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.legacyRequest(ctx, "BackupStatsSummaryMessage", "GET", "/v0/GetBackupStatsSummary", nil, true, false)
	raw, err = restoreResponse(raw, err, "BackupStatsSummaryMessage")
	if err != nil {
		return BackupSummary{}, err
	}
	var result struct {
		Stats *struct {
			Activities int         `json:"activities"`
			Rate       json.Number `json:"sendingSendRateInBytesPerSec"`
			Bytes      json.Number `json:"sendingNumRemainingBytesToSend"`
			Files      int         `json:"sendingNumRemainingFilesToBackup"`
			BackedUp   int         `json:"numFilesBackedUp"`
			Last       json.Number `json:"lastBackupTime"`
			Status     string      `json:"backupStatus"`
			Busy       bool        `json:"systemBusy"`
			Battery    bool        `json:"batteryBlocked"`
		} `json:"stats"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Stats == nil {
		return BackupSummary{}, ErrProtocol
	}
	s := result.Stats
	if !validTimestamp(s.Rate.String()) || !validTimestamp(s.Bytes.String()) || s.Files < 0 || s.BackedUp < 0 || s.Activities < 0 {
		return BackupSummary{}, ErrProtocol
	}
	if _, err := s.Last.Int64(); err != nil {
		return BackupSummary{}, ErrProtocol
	}
	switch s.Status {
	case "SAFE", "WARNING", "SEVERE":
	default:
		return BackupSummary{}, ErrProtocol
	}
	return BackupSummary{Activities: s.Activities, SendRate: s.Rate.String(), RemainingBytes: s.Bytes.String(), RemainingFiles: s.Files, FilesBackedUp: s.BackedUp, LastBackup: s.Last.String(), Status: s.Status, SystemBusy: s.Busy, BatteryBlocked: s.Battery}, nil
}
