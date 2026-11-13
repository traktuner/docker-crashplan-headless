package engine

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrLoginRejected = errors.New("NATIVE_LOGIN_REJECTED")

// LoginRejection contains a fixed public category, never the vendor's text.
type LoginRejection struct{ reason string }

func (e *LoginRejection) Error() string  { return ErrLoginRejected.Error() }
func (e *LoginRejection) Unwrap() error  { return ErrLoginRejected }
func (e *LoginRejection) Reason() string { return e.reason }

// responseEnvelope understands the legacy handler's single response and its
// named multi-message form. Inspect only response envelopes, not arbitrary
// nested application data that could have an unrelated field named errors.
func responseEnvelope(raw []byte, expected string) (json.RawMessage, []string, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, nil, ErrProtocol
	}
	var ids []string
	check := func(value json.RawMessage) error {
		var fields map[string]json.RawMessage
		if json.Unmarshal(value, &fields) != nil || fields == nil {
			return ErrProtocol
		}
		if entries, ok := fields["errors"]; ok {
			var items []struct {
				ID string `json:"id"`
			}
			if string(entries) == "null" || json.Unmarshal(entries, &items) != nil {
				return ErrProtocol
			}
			for _, item := range items {
				ids = append(ids, item.ID)
			}
		}
		var explicitError bool
		if raw, ok := fields["error"]; ok && string(raw) != "null" {
			var id string
			if json.Unmarshal(raw, &id) != nil {
				return ErrProtocol
			}
			if id != "" {
				ids = append(ids, id)
				explicitError = true
			}
		}
		if raw, ok := fields["errorMessage"]; ok && string(raw) != "null" {
			var message string
			if json.Unmarshal(raw, &message) != nil {
				return ErrProtocol
			}
			if message != "" && !explicitError {
				ids = append(ids, "NATIVE_ERROR_MESSAGE")
			}
		}
		return nil
	}
	if err := check(raw); err != nil {
		return nil, nil, err
	}
	for key, value := range object {
		if key != "errorMessage" && strings.HasSuffix(key, "Message") {
			if err := check(value); err != nil {
				return nil, nil, err
			}
		}
	}
	if value, ok := object[expected]; expected != "" && ok {
		return value, ids, nil
	}
	return raw, ids, nil
}

func loginRejection(ids []string) error {
	reason := "REJECTED"
	// These IDs come from the native client's login_constants.js. Unknown
	// strings and vendor messages cannot enter logs or the browser response.
	for _, id := range ids {
		switch id {
		case "SYSTEM", "TIMED_OUT":
			return errors.Join(ErrIndeterminate, ErrLoginRejected)
		case "INCORRECT_LOGIN_PASSWORD", "INCORRECT_LOGIN_USERNAME":
			reason = "INVALID_CREDENTIALS"
		case "CPC_UNAVAILABLE", "TOO_BUSY", "UPGRADING":
			reason = "SERVER_UNAVAILABLE"
		case "TOO_MANY_ATTEMPTS":
			reason = "RATE_LIMITED"
		case "INVALID_REGISTRATION_KEY", "BLOCKED_REGISTRATION":
			reason = "REGISTRATION_REJECTED"
		case "INVALID_EMAIL", "INVALID_NAME", "INVALID_PASSWORD":
			reason = "INVALID_ACCOUNT_DETAILS"
		case "TIME_BASED_ONE_TIME_PASSWORD_REQUIRED", "MFA_CONFIGURATION_REQUIRED_FOR_USER", "EMAIL_VERIFICATION_CODE_REQUIRED", "ADDITIONAL_AUTH_FACTOR_REQUIRED":
			reason = "USE_BROWSER_SSO"
		}
	}
	return &LoginRejection{reason: reason}
}
