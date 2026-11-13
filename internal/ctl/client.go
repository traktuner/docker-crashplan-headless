// Package ctl implements the headless CLI over the private product socket.
package ctl

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/traktuner/crashplan-headless/internal/engine"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("CONTROL_UNAVAILABLE")
var ErrResponse = errors.New("CONTROL_INVALID_RESPONSE")
var ErrKeyExportRefused = errors.New("KEY_EXPORT_REFUSED")

// ExportArchiveKey is an explicit secret download, separate from JSON controls.
func (c *Client) ExportArchiveKey(ctx context.Context) ([]byte, error) {
	return c.exportKey(ctx, "security/export-key", json.RawMessage(`{}`))
}
func (c *Client) ExportDerivedKey(ctx context.Context, input json.RawMessage) ([]byte, error) {
	return c.exportKey(ctx, "security/derived-key/export", input)
}
func (c *Client) exportKey(ctx context.Context, path string, input json.RawMessage) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "http://unix/api/v1/"+path, bytes.NewReader(input))
	if err != nil {
		return nil, ErrResponse
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, ErrKeyExportRefused
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(raw) > 4096 || response.Header.Get("Content-Type") != "application/octet-stream" {
		return nil, ErrResponse
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSuffix(string(raw), "\n"))
	if err != nil || len(decoded) != 56 {
		return nil, ErrResponse
	}
	return raw, nil
}

type Client struct{ http *http.Client }

func New(socket string) *Client {
	return &Client{http: &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Request(ctx context.Context, method, path string, body any) (json.RawMessage, int, error) {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, 0, ErrResponse
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, "http://unix/api/v1/"+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, 0, ErrResponse
	}
	r.Header.Set("Content-Type", "application/json")
	client := c.http
	if strings.HasPrefix(path, "account/") {
		copy := *c.http
		copy.Timeout = engine.AccountOperationTimeout + 5*time.Second
		client = &copy
	}
	response, err := client.Do(r)
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 || !json.Valid(raw) {
		return nil, response.StatusCode, ErrResponse
	}
	return raw, response.StatusCode, nil
}
