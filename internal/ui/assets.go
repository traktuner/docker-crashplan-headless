// Package ui embeds the prebuilt SPA. Build web/ before compiling this package.
package ui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist
var files embed.FS

type asset struct {
	raw, gzip         []byte
	etag, contentType string
}

func Handler() (http.Handler, error) {
	assets := make(map[string]asset)
	err := fs.WalkDir(files, "dist", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(raw); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		assets[strings.TrimPrefix(name, "dist")] = asset{raw, compressed.Bytes(), `"` + hex.EncodeToString(digest[:]) + `"`, mime.TypeByExtension(path.Ext(name))}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path
		if name == "/" {
			name = "/index.html"
		}
		file, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", file.contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Vary", "Accept-Encoding")
		if strings.HasPrefix(name, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("ETag", file.etag)
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		raw := file.raw
		for _, coding := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
			if strings.TrimSpace(coding) == "gzip" {
				w.Header().Set("Content-Encoding", "gzip")
				raw = file.gzip
				break
			}
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(raw))
	}), nil
}
