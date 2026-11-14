package app

import (
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLargeAnswersAreCompressedSmallOnesAreNot(t *testing.T) {
	for _, size := range []int{10, 5000} {
		rec := httptest.NewRecorder()
		g := &gzipJSON{ResponseWriter: rec}
		g.WriteHeader(201)
		body := strings.Repeat("a", size)
		io.WriteString(g, body)
		g.finish()
		if rec.Code != 201 {
			t.Fatalf("status %d", rec.Code)
		}
		got := rec.Body.String()
		if size >= 1024 {
			if rec.Header().Get("Content-Encoding") != "gzip" {
				t.Fatal("large answer not compressed")
			}
			zr, err := gzip.NewReader(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(zr)
			got = string(raw)
		} else if rec.Header().Get("Content-Encoding") != "" {
			t.Fatal("small answer compressed")
		}
		if got != body {
			t.Fatal("body changed")
		}
	}
}
