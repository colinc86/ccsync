// Package secrets stores and retrieves redacted JSON values.
//
// Backend selection:
//   - default: OS keychain (via zalando/go-keyring)
//   - env CCSYNC_SECRETS_BACKEND=file: a mode-0600 file under
//     ~/.ccsync/secrets/ (unblocks headless / CI use without keychain prompts)
//
// Keys use the form "<profile>:<json-path>" under the ccsync service.
package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const ServiceName = "ccsync"

// ErrNotFound is returned when a key is absent.
var ErrNotFound = errors.New("secret not found")

// Key forms the service-qualified key for a profile + JSON path.
func Key(profile, jsonPath string) string {
	return profile + ":" + jsonPath
}

func useFileBackend() bool {
	return os.Getenv("CCSYNC_SECRETS_BACKEND") == "file"
}

// Store writes value under key.
func Store(key, value string) error {
	if useFileBackend() {
		return fileStore(key, value)
	}
	return keyring.Set(ServiceName, key, value)
}

// Fetch reads the value, returning ErrNotFound if the key is absent.
func Fetch(key string) (string, error) {
	if useFileBackend() {
		return fileFetch(key)
	}
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
	if useFileBackend() {
		return fileDelete(key)
	}
	err := keyring.Delete(ServiceName, key)
	if err != nil && errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// MockInit switches the keychain backend to an in-memory provider.
// Tests that need cross-invocation persistence should set the
// CCSYNC_SECRETS_BACKEND=file env var instead.
func MockInit() {
	keyring.MockInit()
}

// --- file backend ---

func fileBackendDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ccsync", "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func fileBackendPath(key string) (string, error) {
	dir, err := fileBackendDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeKey(key)), nil
}

// sanitizeKey maps "/" and ":" into safe chars so keys become flat filenames.
func sanitizeKey(key string) string {
	return strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(key)
}

func fileStore(key, value string) error {
	path, err := fileBackendPath(key)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fileFetch(key string) (string, error) {
	path, err := fileBackendPath(key)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	return string(data), nil
}

func fileDelete(key string) error {
	path, err := fileBackendPath(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
