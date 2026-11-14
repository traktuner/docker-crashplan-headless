package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// authorityClient reaches the vendor authority selector on the internet. It
// uses the system trust store and proxy settings, never the engine's local TLS.
var authorityClient = &http.Client{Timeout: 15 * time.Second}

// DiscoverAuthority finds the server that holds an account, as the desktop
// app does: the engine names the selector URL in LoginSetup, and the selector
// answers with the account's authority for an email address or username.
func (c *Client) DiscoverAuthority(ctx context.Context, username string) (string, error) {
	ctx = c.operationContext(ctx)
	if !validLogin(username, "-") {
		return "", ErrInvalidRequest
	}
	raw, err := c.request(ctx, "POST", "/v1/LoginSetup", []byte(`{}`), false, false)
	if err != nil {
		return "", err
	}
	decoded, ids, err := responseEnvelope(raw, "")
	if err != nil {
		return "", err
	}
	if len(ids) > 0 {
		return "", loginRejection(ids)
	}
	var setup struct {
		Selector string `json:"authoritySelectorApiUrl"`
		Version  string `json:"productVersion"`
	}
	if json.Unmarshal(decoded, &setup) != nil {
		return "", ErrProtocol
	}
	selector, err := url.Parse(setup.Selector)
	if setup.Selector == "" || err != nil || selector.Scheme != "https" || selector.Host == "" {
		// No selector (for example a private server): the address must be entered.
		return "", &LoginRejection{reason: "AUTHORITY_REQUIRED"}
	}
	return lookupAuthority(ctx, selector, username, setup.Version)
}

func lookupAuthority(ctx context.Context, selector *url.URL, username, version string) (string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", ErrUnavailable
	}
	id[6] = id[6]&0x0f | 0x40 // version 4, as crypto.randomUUID in the desktop app
	id[8] = id[8]&0x3f | 0x80
	h := hex.EncodeToString(id)
	query := selector.Query()
	query.Set("uuid", h[:8]+"-"+h[8:12]+"-"+h[12:16]+"-"+h[16:20]+"-"+h[20:])
	query.Set("username", username)
	target := *selector
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		return "", ErrInvalidRequest
	}
	req.Header.Set("Accept", "application/json")
	// The selector answers only CrashPlan desktop clients; this control acts as
	// the desktop UI of the same engine and says so.
	if version == "" || strings.ContainsAny(version, " \r\n") {
		version = "0"
	}
	req.Header.Set("User-Agent", "CrashPlan/"+version+" Electron CrashPlan-Headless")
	response, err := authorityClient.Do(req)
	if err != nil {
		return "", &LoginRejection{reason: "SERVER_UNAVAILABLE"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", &LoginRejection{reason: "SERVER_UNAVAILABLE"}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return "", &LoginRejection{reason: "SERVER_UNAVAILABLE"}
	}
	var found []struct {
		Address string `json:"primaryAddress"`
	}
	if json.Unmarshal(body, &found) != nil {
		return "", ErrProtocol
	}
	switch len(found) {
	case 0:
		return "", &LoginRejection{reason: "ACCOUNT_NOT_FOUND"}
	case 1:
		address := strings.TrimSpace(found[0].Address)
		if !validLogin(username, address) {
			return "", ErrProtocol
		}
		return address, nil
	default:
		// The account exists on several servers; the operator chooses one.
		return "", &LoginRejection{reason: "AUTHORITY_REQUIRED"}
	}
}
