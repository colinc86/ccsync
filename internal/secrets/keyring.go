// Package secrets stores and retrieves redacted JSON values via the OS keychain.
// Keys use the form "<profile>:<json-path>" under the ccsync service.
package secrets

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const ServiceName = "ccsync"

// ErrNotFound is returned when a key is absent from the keychain.
var ErrNotFound = errors.New("secret not found in keychain")

// Key forms the keychain key for a profile + JSON path.
func Key(profile, jsonPath string) string {
	return profile + ":" + jsonPath
}

// Store writes value under key.
func Store(key, value string) error {
	return keyring.Set(ServiceName, key, value)
}

// Fetch reads the value, returning ErrNotFound if the key is absent.
func Fetch(key string) (string, error) {
	v, err := keyring.Get(ServiceName, key)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	return v, nil
}

// Delete removes the key. Missing keys are not an error.
func Delete(key string) error {
	err := keyring.Delete(ServiceName, key)
	if err != nil && errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// MockInit switches to an in-memory provider; use in tests only.
func MockInit() {
	keyring.MockInit()
}
