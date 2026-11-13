package ui

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuiltAssetsAreEmbeddedCompressedAndCacheSeparated(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	index := httptest.NewRecorder()
	h.ServeHTTP(index, httptest.NewRequest("GET", "http://fixture/", nil))
	if index.Code != 200 || index.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("index cache is unsafe")
	}
	body := index.Body.String()
	start := strings.Index(body, "/assets/")
	if start < 0 {
		t.Fatal("production index lacks built assets")
	}
	end := strings.IndexByte(body[start:], '"')
	if end < 0 {
		t.Fatal("asset link missing")
	}
	assetPath := body[start : start+end]
	request := httptest.NewRequest("GET", "http://fixture"+assetPath, nil)
	request.Header.Set("Accept-Encoding", "gzip")
	asset := httptest.NewRecorder()
	h.ServeHTTP(asset, request)
	if asset.Code != http.StatusOK || asset.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("hashed asset not compressed/cacheable")
	}
	reader, err := gzip.NewReader(asset.Body)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || len(raw) == 0 {
		t.Fatal("compressed asset corrupt")
	}
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest("GET", "http://fixture/assets/missing.js", nil))
	if missing.Code != 404 {
		t.Fatal("missing asset incorrectly returned HTML")
	}
}
