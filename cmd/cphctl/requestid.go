package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

var errRequestID = errors.New("INVALID_REQUEST_ID")

func validRequestID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16 && strings.ToLower(id) == id
}
func newRequestID(provided string) (string, error) {
	if provided != "" {
		if !validRequestID(provided) {
			return "", errRequestID
		}
		return provided, nil
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}
