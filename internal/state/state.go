// Package state owns the per-host state.json at ~/.ccsync/state.json.
// Sync, TUI, bootstrap, and profile all read/write the same record.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AuthKind discriminates how ccsync authenticates with the sync remote.
type AuthKind string

const (
	AuthNone  AuthKind = ""
	AuthSSH   AuthKind = "ssh"
	AuthHTTPS AuthKind = "https"
)

// State is the on-disk shape of ~/.ccsync/state.json.
type State struct {
	SyncRepoURL   string            `json:"syncRepoURL,omitempty"`
	Auth          AuthKind          `json:"auth,omitempty"`
	SSHKeyPath    string            `json:"sshKeyPath,omitempty"`
	HTTPSUser     string            `json:"httpsUser,omitempty"`
	ActiveProfile string            `json:"activeProfile,omitempty"`
	HostUUID      string            `json:"hostUUID,omitempty"`
	HostClass     string            `json:"hostClass,omitempty"` // freeform label (work, personal); informational for now
	LastSyncedSHA map[string]string `json:"lastSyncedSHA,omitempty"`
}

// Path returns the state.json path inside stateDir (~/.ccsync by default).
func Path(stateDir string) string {
	return filepath.Join(stateDir, "state.json")
}

// Load reads state.json. Missing file returns a fresh State — not an error.
func Load(stateDir string) (*State, error) {
	data, err := os.ReadFile(Path(stateDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &State{LastSyncedSHA: map[string]string{}}, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.LastSyncedSHA == nil {
		s.LastSyncedSHA = map[string]string{}
	}
	return &s, nil
}

// Save writes state.json atomically with 0600 permissions.
func Save(stateDir string, s *State) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := Path(stateDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// EnsureHostUUID generates a fresh random host UUID if one isn't set.
// Returns the (possibly newly assigned) UUID.
func (s *State) EnsureHostUUID() string {
	if s.HostUUID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		s.HostUUID = hex.EncodeToString(b)
	}
	return s.HostUUID
}

// DefaultStateDir returns ~/.ccsync.
func DefaultStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home: %w", err)
	}
	return filepath.Join(home, ".ccsync"), nil
}
