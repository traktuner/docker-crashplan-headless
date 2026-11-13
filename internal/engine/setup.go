package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

func (c *Client) LoginSetup(ctx context.Context) (json.RawMessage, error) {
	ctx = c.operationContext(ctx)
	// appCode=CPD clears native new-alert state. The product does not send it.
	raw, err := c.request(ctx, "POST", "/v1/LoginSetup", []byte(`{}`), false, false)
	if err != nil {
		return nil, err
	}
	decoded, ids, err := responseEnvelope(raw, "")
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		return nil, loginRejection(ids)
	}
	var result struct {
		Authenticated        *bool  `json:"authenticated"`
		AuthorityFullAddress string `json:"authorityFullAddress"`
		Username             string `json:"username,omitempty"`
		FirstName            string `json:"firstName,omitempty"`
		LastName             string `json:"lastName,omitempty"`
		Locale               string `json:"locale,omitempty"`
		Version              string `json:"productVersion,omitempty"`
		Build                string `json:"buildNumber,omitempty"`
		Migration            bool   `json:"authorizedDeviceMigrationInProgress"`
		RemoteUI             bool   `json:"remoteUI"`
		Hostname             string `json:"osHostname,omitempty"`
	}
	if json.Unmarshal(decoded, &result) != nil || result.Authenticated == nil {
		return nil, ErrProtocol
	}
	return json.Marshal(result)
}

type LoginConfiguration struct {
	Type            string `json:"loginType"`
	RegistrationKey string `json:"regKey,omitempty"`
}

func (c LoginConfiguration) String() string { return "engine.LoginConfiguration{private}" }

// Native discovery can change the authority address; serialize it with login.
func (c *Client) LoginConfiguration(ctx context.Context, in LoginInfo) (LoginConfiguration, error) {
	ctx = c.operationContext(ctx)
	if !c.loginMu.TryLock() {
		return LoginConfiguration{}, ErrLoginInProgress
	}
	defer c.loginMu.Unlock()
	return c.loginConfiguration(ctx, in)
}
func (c *Client) loginConfiguration(ctx context.Context, in LoginInfo) (LoginConfiguration, error) {
	ctx = c.operationContext(ctx)
	if !validLogin(in.Username, in.ServerAddress) {
		return LoginConfiguration{}, ErrInvalidRequest
	}
	query := url.Values{"username": []string{in.Username}, "serverAddress": []string{in.ServerAddress}}
	if in.RegistrationKey != "" {
		query.Set("regKey", in.RegistrationKey)
	}
	raw, err := c.request(ctx, "GET", "/v1/LoginConfiguration?"+query.Encode(), nil, false, false)
	if err != nil {
		var status *nativeStatusError
		if errors.As(err, &status) && status.status == 504 {
			return LoginConfiguration{}, &LoginRejection{reason: "SERVER_UNAVAILABLE"}
		}
		return LoginConfiguration{}, err
	}
	var result LoginConfiguration
	if json.Unmarshal(raw, &result) != nil || (result.Type != "local" && result.Type != "cloud_sso") {
		return LoginConfiguration{}, ErrProtocol
	}
	return result, nil
}

// Reacquire registration context inside the actual caller's generation-bound
// operation. No discovered key is cached, shared between callers or serialized.
func (c *Client) registrationContext(ctx context.Context, in LoginInfo) (LoginInfo, error) {
	if !in.Register || in.RegistrationKey != "" || in.OrganizationKey != "" {
		return in, nil
	}
	configuration, err := c.loginConfiguration(ctx, in)
	if err != nil {
		return in, err
	}
	if strings.TrimSpace(configuration.RegistrationKey) == "" {
		return in, &LoginRejection{reason: "REGISTRATION_KEY_REQUIRED"}
	}
	in.RegistrationKey = configuration.RegistrationKey
	return in, nil
}

// SSO discovers the client key separately from the operator's organization key.
// The matching renderer sends both meanings in different native fields.
func (c *Client) ssoContext(ctx context.Context, in LoginInfo) (LoginInfo, error) {
	org := in.OrganizationKey
	if org == "" {
		org = in.RegistrationKey
	}
	lookup := in
	lookup.RegistrationKey = org
	discovered, err := c.loginConfiguration(ctx, lookup)
	if err != nil {
		return in, err
	}
	if in.Register && strings.TrimSpace(discovered.RegistrationKey) == "" {
		return in, &LoginRejection{reason: "REGISTRATION_KEY_REQUIRED"}
	}
	if discovered.Type != "cloud_sso" {
		return in, ErrRejected
	}
	in.RegistrationKey = discovered.RegistrationKey
	in.OrganizationKey = org
	return in, nil
}
