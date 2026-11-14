package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
)

const archiveRoot = "AAAAAAAAAAAAAAAAAAAAAA=="

type ArchiveQuery struct {
	SourceID       string `json:"sourceId"`
	TargetID       string `json:"targetId"`
	BackupSetID    string `json:"backupSetId"`
	FileID         string `json:"fileId"`
	At             string `json:"at"`
	IncludeDeleted bool   `json:"includeDeleted"`
	Cursor         string `json:"cursor"`
	Limit          int    `json:"limit"`
}

func validFileID(value string) bool {
	b, err := base64.StdEncoding.Strict().DecodeString(value)
	return err == nil && len(b) == 16 && base64.StdEncoding.EncodeToString(b) == value
}
func (q ArchiveQuery) normalized() (ArchiveQuery, error) {
	if q.FileID == "" {
		q.FileID = archiveRoot
	}
	if q.At == "" {
		q.At = "0"
	}
	if q.Limit == 0 {
		q.Limit = 200
	}
	if !validID(q.SourceID) || !validID(q.TargetID) || !validID(q.BackupSetID) || !validTimestamp(q.At) || !validFileID(q.FileID) || (q.Cursor != "" && !validFileID(q.Cursor)) || q.Limit < 1 || q.Limit > 1000 {
		return q, ErrInvalidRequest
	}
	return q, nil
}

// Validate rejects malformed browsing requests before retrieving archive keys.
func (q ArchiveQuery) Validate() error { _, err := q.normalized(); return err }

type nativeFileID struct {
	Array string `json:"array"`
}
type archiveQueryWire struct {
	Source    json.Number   `json:"sourceId"`
	Target    json.Number   `json:"targetId"`
	Set       json.Number   `json:"backupSetId"`
	File      nativeFileID  `json:"fileId"`
	Timestamp json.Number   `json:"timestamp"`
	Deleted   bool          `json:"includeDeleted"`
	Last      *nativeFileID `json:"batchLastFileId,omitempty"`
	Limit     int           `json:"batchSize"`
	Error     string        `json:"errorCode,omitempty"`
}

func (q ArchiveQuery) wire() archiveQueryWire {
	w := archiveQueryWire{Source: json.Number(q.SourceID), Target: json.Number(q.TargetID), Set: json.Number(q.BackupSetID), File: nativeFileID{q.FileID}, Timestamp: json.Number(q.At), Deleted: q.IncludeDeleted, Limit: q.Limit}
	if q.Cursor != "" {
		w.Last = &nativeFileID{q.Cursor}
	}
	return w
}

// Native longs must be strings. json.Number is used only for decoding below.
func (q ArchiveQuery) body(key string) []byte {
	query := map[string]any{"sourceId": q.SourceID, "targetId": q.TargetID, "backupSetId": q.BackupSetID, "fileId": nativeFileID{q.FileID}, "timestamp": q.At, "includeDeleted": q.IncludeDeleted, "batchSize": q.Limit}
	if q.Cursor != "" {
		query["batchLastFileId"] = nativeFileID{q.Cursor}
	}
	request := map[string]any{"backupQueryData": query}
	if key != "" {
		request["dataKey"] = key
	}
	body, _ := json.Marshal(request)
	return body
}

type ArchiveVersion struct {
	Timestamp string `json:"timestamp"`
	Modified  string `json:"modified"`
	Size      string `json:"size"`
	IsFolder  bool   `json:"isFolder"`
}
type ArchiveItem struct {
	ArchiveVersion
	FileID string `json:"fileId"`
	Path   string `json:"path"`
}
type ArchivePage struct {
	Incomplete bool          `json:"incomplete"`
	Items      []ArchiveItem `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}
type archiveVersionWire struct {
	Timestamp json.Number `json:"timestamp"`
	Modified  json.Number `json:"sourceLastModified"`
	Size      json.Number `json:"sourceLength"`
	Type      *int        `json:"fileType"`
}

func (w archiveVersionWire) decode() (ArchiveVersion, error) {
	modified, err := w.Modified.Int64()
	if !validTimestamp(w.Timestamp.String()) || !validTimestamp(w.Size.String()) || err != nil || strconv.FormatInt(modified, 10) != w.Modified.String() || w.Type == nil || *w.Type < 0 || *w.Type > 9 {
		return ArchiveVersion{}, ErrProtocol
	}
	return ArchiveVersion{Timestamp: w.Timestamp.String(), Modified: w.Modified.String(), Size: w.Size.String(), IsFolder: *w.Type == 1}, nil
}
func (c *Client) BrowseArchive(ctx context.Context, credentials RestoreCredentials, in ArchiveQuery) (ArchivePage, error) {
	ctx = c.operationContext(ctx)
	q, err := in.normalized()
	if err != nil {
		return ArchivePage{}, err
	}
	if !credentials.verified || credentials.key == "" {
		return ArchivePage{}, ErrArchiveKey
	}
	if credentials.revision != c.SessionRevision() {
		return ArchivePage{}, ErrSessionChanged
	}
	if credentials.generation != nil && ctx.Value(generationKey{}) != credentials.generation {
		return ArchivePage{}, ErrUnavailable
	}
	raw, err := c.legacyRead(ctx, "GetChildrenFileVersionsResultMessage", "POST", "/v0/GetChildrenFileVersions", q.body(credentials.key), func(decoded []byte) probeResult {
		// The answer echoes its query; only this query's answer proves the lane.
		if match, e := matchArchiveQuery(q, decoded); e == nil && match {
			return probeInStep
		}
		return probeMismatch
	})
	raw, err = restoreResponse(raw, err, "GetChildrenFileVersionsResultMessage")
	if err != nil {
		return ArchivePage{}, err
	}
	return decodeArchivePage(q, raw)
}

func decodeArchivePage(q ArchiveQuery, raw []byte) (ArchivePage, error) {
	var wire struct {
		Query *archiveQueryWire `json:"backupQueryData"`
		Set   *struct {
			Items *[]struct {
				File struct {
					ID   nativeFileID `json:"fileId"`
					Path string       `json:"sourcePath"`
				} `json:"backupFile"`
				Version archiveVersionWire `json:"version"`
			} `json:"fileVersions"`
		} `json:"set"`
		Next *nativeFileID `json:"batchNextFileId"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Query == nil {
		return ArchivePage{}, ErrProtocol
	}
	if wire.Query.Error != "" {
		return ArchivePage{}, ErrRejected
	}
	match, err := matchArchiveQuery(q, raw)
	if err != nil {
		return ArchivePage{}, err
	}
	if !match {
		return ArchivePage{}, ErrProtocol
	}
	if wire.Set == nil || wire.Set.Items == nil || len(*wire.Set.Items) > q.Limit {
		return ArchivePage{}, ErrProtocol
	}
	page := ArchivePage{Items: make([]ArchiveItem, 0, len(*wire.Set.Items))}
	seen := map[string]bool{}
	for _, item := range *wire.Set.Items {
		v, e := item.Version.decode()
		if e != nil || !validFileID(item.File.ID.Array) || seen[item.File.ID.Array] || item.File.Path == "" || len(item.File.Path) > 65536 || strings.ContainsAny(item.File.Path, "\x00\r\n") {
			return ArchivePage{}, ErrProtocol
		}
		seen[item.File.ID.Array] = true
		page.Items = append(page.Items, ArchiveItem{ArchiveVersion: v, FileID: item.File.ID.Array, Path: item.File.Path})
	}
	if wire.Next != nil {
		if !validFileID(wire.Next.Array) || wire.Next.Array == q.Cursor {
			return ArchivePage{}, ErrProtocol
		}
		page.NextCursor = wire.Next.Array
	}
	return page, nil
}
func (c *Client) ArchiveVersions(ctx context.Context, in ArchiveQuery) ([]ArchiveVersion, error) {
	ctx = c.operationContext(ctx)
	q, err := in.normalized()
	if err != nil {
		return nil, err
	}
	raw, err := c.legacyRead(ctx, "GetAllVersionsResultMessage", "PUT", "/v0/GetAllVersions", q.body(""), func(decoded []byte) probeResult {
		if match, e := matchArchiveQuery(q, decoded); e == nil && match {
			return probeInStep
		}
		return probeMismatch
	})
	raw, err = restoreResponse(raw, err, "GetAllVersionsResultMessage")
	if err != nil {
		return nil, err
	}
	var wire struct {
		Set *struct {
			Versions *[]archiveVersionWire `json:"versions"`
		} `json:"versionSet"`
		Query *archiveQueryWire `json:"backupQueryData"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return nil, ErrProtocol
	}
	match, err := matchArchiveQuery(q, raw)
	if err != nil {
		return nil, err
	}
	if !match {
		return nil, ErrProtocol
	}
	if wire.Set == nil || wire.Set.Versions == nil || len(*wire.Set.Versions) > 10000 {
		return nil, ErrProtocol
	}
	versions := make([]ArchiveVersion, 0, len(*wire.Set.Versions))
	cutoff, _ := strconv.ParseInt(q.At, 10, 64)
	for _, entry := range *wire.Set.Versions {
		v, err := entry.decode()
		if err != nil {
			return nil, err
		}
		stamp, _ := strconv.ParseInt(v.Timestamp, 10, 64)
		if cutoff == 0 || stamp <= cutoff {
			versions = append(versions, v)
		}
	}
	return versions, nil
}
