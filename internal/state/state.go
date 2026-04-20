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
	"time"
)

// AuthKind discriminates how ccsync authenticates with the sync remote.
type AuthKind string

const (
	AuthNone  AuthKind = ""
	AuthSSH   AuthKind = "ssh"
	AuthHTTPS AuthKind = "https"
)

// SecretsBackend names a persistence backend for JSON redaction values.
// Empty string means "fall back to env CCSYNC_SECRETS_BACKEND, or keychain".
type SecretsBackend string

const (
	SecretsBackendDefault  SecretsBackend = ""
	SecretsBackendKeychain SecretsBackend = "keychain"
	SecretsBackendFile     SecretsBackend = "file"
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

	// Commit identity — used as git author on every sync commit. Unset means
	// fallback to hostname / hostname@ccsync.local.
	AuthorName  string `json:"authorName,omitempty"`
	AuthorEmail string `json:"authorEmail,omitempty"`

	// SecretsBackend overrides the default (keychain). Empty string means use
	// the env var or platform default.
	SecretsBackend SecretsBackend `json:"secretsBackend,omitempty"`

	// Snapshot retention. Zero means "use defaults" (30 snapshots, 14 days).
	SnapshotMaxCount   int `json:"snapshotMaxCount,omitempty"`
	SnapshotMaxAgeDays int `json:"snapshotMaxAgeDays,omitempty"`

	// AutoApplyClean, when true, skips the "press enter to apply" step on
	// syncs that have no conflicts and no redaction gaps. Default false.
	AutoApplyClean bool `json:"autoApplyClean,omitempty"`

	// FetchInterval controls how often the TUI re-runs a background dry-run
	// to refresh the push/pull status badge. Parsed via ParseFetchInterval;
	// stored as a short string ("", "1h", "24h") so the state file stays
	// human-readable. Empty == no periodic refresh; startup and on-demand
	// refreshes still happen.
	FetchInterval string `json:"fetchInterval,omitempty"`

	// DismissedSuggestions records rule patterns the user has rejected from
	// the Suggestions screen. The suggester filters these out so nothing
	// gets re-proposed after dismissal.
	DismissedSuggestions []string `json:"dismissedSuggestions,omitempty"`

	// UpdateMode controls self-update behaviour. Empty or "manual" means
	// the app only checks/installs when the user explicitly asks; "auto"
	// silently installs a new version in the background when one is
	// available. Homebrew-installed binaries are never auto-replaced.
	UpdateMode string `json:"updateMode,omitempty"`
}

// FetchIntervalDuration returns the parsed fetch interval, or zero when the
// user has opted out of periodic fetches.
func (s *State) FetchIntervalDuration() time.Duration {
	if s == nil {
		return 0
	}
	return ParseFetchInterval(s.FetchInterval)
}

// ParseFetchInterval accepts "", "1h", "24h" — or any Go duration string the
// user has put in state.json by hand. Unparseable strings yield 0 ("off").
func ParseFetchInterval(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

// SnapshotRetention returns (maxCount, maxAge) with defaults applied.
func (s *State) SnapshotRetention() (int, int) {
	count := s.SnapshotMaxCount
	if count <= 0 {
		count = 30
	}
	days := s.SnapshotMaxAgeDays
	if days <= 0 {
		days = 14
	}
	return count, days
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
