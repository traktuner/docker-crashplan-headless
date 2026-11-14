package engine

import (
	"context"
	"encoding/json"
	"strings"
)

// AccountInfo is the read-only account and license panel of the stock app
// (General preferences: device name and ID; license from StatusQuery).
type AccountInfo struct {
	Username       string `json:"username"`
	Server         string `json:"server"`
	DeviceName     string `json:"deviceName"`
	DeviceGUID     string `json:"deviceGuid"`
	ProductVersion string `json:"productVersion"`
	// LicenseType is the engine license's name; the engine has no "type" field.
	LicenseType string `json:"licenseType,omitempty"`
	// LicenseExpires is the license expireDate in epoch milliseconds.
	LicenseExpires string `json:"licenseExpires,omitempty"`
}

// Account reads LoginSetup (user, authority, version), the computer list
// (this device) and StatusQuery (license). The license part is optional: a
// failed or incomplete StatusQuery omits it instead of failing the panel.
func (c *Client) Account(ctx context.Context) (AccountInfo, error) {
	ctx = c.operationContext(ctx)
	if !c.Authorized() {
		return AccountInfo{}, ErrUnauthorized
	}
	setup, err := c.LoginSetup(ctx)
	if err != nil {
		return AccountInfo{}, err
	}
	var login struct {
		Authenticated *bool  `json:"authenticated"`
		Server        string `json:"authorityFullAddress"`
		Username      string `json:"username"`
		Version       string `json:"productVersion"`
	}
	if json.Unmarshal(setup, &login) != nil || login.Authenticated == nil {
		return AccountInfo{}, ErrProtocol
	}
	if !*login.Authenticated {
		return AccountInfo{}, ErrUnauthorized
	}
	devices, err := c.Devices(ctx)
	if err != nil {
		return AccountInfo{}, err
	}
	info := AccountInfo{Username: printable(login.Username, 512), Server: printable(login.Server, 1024), ProductVersion: printable(login.Version, 64)}
	for _, d := range devices {
		if d.Self {
			info.DeviceName, info.DeviceGUID = d.Name, d.ID
		}
	}
	if info.DeviceGUID == "" {
		return AccountInfo{}, ErrProtocol
	}
	raw, err := c.legacyRead(ctx, "StatusResponseMessage", "POST", "/v0/StatusQuery", statusQuery(c.home()), anyAnswer)
	if raw, err = restoreResponse(raw, err, "StatusResponseMessage"); err == nil {
		info.LicenseType, info.LicenseExpires = parseLicense(raw)
		if info.Username == "" {
			info.Username = statusUsername(raw)
		}
	}
	return info, nil
}

// parseLicense projects only licenseMessage.license.name and expireDate. The
// license key and every other license field stay private.
func parseLicense(raw []byte) (string, string) {
	var wire struct {
		License *struct {
			License *struct {
				Name   string          `json:"name"`
				Expire json.RawMessage `json:"expireDate"`
			} `json:"license"`
		} `json:"licenseMessage"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.License == nil || wire.License.License == nil {
		return "", ""
	}
	l := wire.License.License
	return printable(l.Name, 128), decimal(l.Expire)
}

func statusUsername(raw []byte) string {
	var wire struct {
		User *struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.User == nil {
		return ""
	}
	return printable(wire.User.Username, 512)
}

// printable drops values with control characters or above max bytes.
func printable(s string, max int) string {
	if len(s) > max || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return ""
	}
	return s
}
