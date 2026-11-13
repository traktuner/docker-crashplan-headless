package engine

import (
	"context"
	"encoding/json"
)

type ApplicationStatus struct {
	GUID           string `json:"guid,omitempty"`
	ProductVersion string `json:"productVersion"`
	BuildNumber    string `json:"buildNumber"`
	Authorized     bool   `json:"authorized"`
	Blocked        bool   `json:"blocked"`
	DataKeyExists  bool   `json:"dataKeyExists"`
}

func (c *Client) ApplicationStatus(ctx context.Context) (ApplicationStatus, error) {
	ctx = c.operationContext(ctx)
	raw, err := c.Call(ctx, "status", OperationRequest{})
	if err != nil {
		return ApplicationStatus{}, err
	}
	decoded, _, err := responseEnvelope(raw, "ApplicationStatusMessage")
	if err != nil {
		return ApplicationStatus{}, err
	}
	var wire struct {
		GUID           json.Number `json:"guid"`
		ProductVersion string      `json:"productVersion"`
		BuildNumber    string      `json:"buildNumber"`
		Authorized     bool        `json:"authorized"`
		Blocked        bool        `json:"blocked"`
		DataKeyExists  bool        `json:"dataKeyExists"`
	}
	if json.Unmarshal(decoded, &wire) != nil || wire.ProductVersion == "" || wire.BuildNumber == "" {
		return ApplicationStatus{}, ErrProtocol
	}
	return ApplicationStatus{wire.GUID.String(), wire.ProductVersion, wire.BuildNumber, wire.Authorized, wire.Blocked, wire.DataKeyExists}, nil
}
