package engine

import (
	"context"
	"encoding/json"
)

type ArchiveContents struct {
	Files       string `json:"files"`
	Directories string `json:"directories"`
	Bytes       string `json:"bytes"`
	Done        bool   `json:"done"`
	Verified    bool   `json:"verified"`
}

func decodeArchiveContents(q ArchiveQuery, raw []byte) (ArchiveContents, error) {
	var w struct {
		Contents *struct {
			File        nativeFileID `json:"fileId"`
			Files       json.Number  `json:"numFiles"`
			Directories json.Number  `json:"numDirectories"`
			Bytes       json.Number  `json:"size"`
			Done        *bool        `json:"done"`
		} `json:"contents"`
	}
	if json.Unmarshal(raw, &w) != nil || w.Contents == nil {
		return ArchiveContents{}, ErrProtocol
	}
	v := w.Contents
	if v.File.Array != q.FileID || v.Done == nil || !validTimestamp(v.Files.String()) || !validTimestamp(v.Directories.String()) || !validTimestamp(v.Bytes.String()) {
		return ArchiveContents{}, ErrProtocol
	}
	return ArchiveContents{Files: v.Files.String(), Directories: v.Directories.String(), Bytes: v.Bytes.String(), Done: *v.Done}, nil
}
func (c *Client) ArchiveContents(ctx context.Context, in ArchiveQuery) (ArchiveContents, error) {
	ctx = c.operationContext(ctx)
	q, err := in.normalized()
	if err != nil || q.Cursor != "" {
		return ArchiveContents{}, ErrInvalidRequest
	}
	return c.countArchive(ctx, q)
}
