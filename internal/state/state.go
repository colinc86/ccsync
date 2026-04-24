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

	// LastSyncedAt records the UTC timestamp at which LastSyncedSHA was
	// last advanced for each profile. Used by the Home dashboard to show
	// "last synced Nh ago" without relying on the snapshot directory as
	// a proxy (which undercounted push-only syncs since snapshots are
	// only taken pre-local-write). Zero value on load for existing
	// users; backfilled on first sync under the new code.
	LastSyncedAt map[string]time.Time `json:"lastSyncedAt,omitempty"`

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

	// OnboardingComplete flips true once the first-run wizard has been
	// dismissed (whether the user finished it or skipped through). The
	// Home router uses this to decide whether to push the wizard on
	// launch. Existing users who had SyncRepoURL set before this field
	// existed effectively skip onboarding because the Home router checks
	// SyncRepoURL first — so the backfill is automatic.
	OnboardingComplete bool `json:"onboardingComplete,omitempty"`

	// SyncMode picks the default sync UX. Three valid values:
	//   - "auto": file watcher + auto-apply clean syncs (the "install,
	//     sync, forget" default)
	//   - "approve": auto-apply modifications and deletes both
	//     directions, but pause on any new file (push or pull) for the
	//     user to allow or deny. Lets users run mostly-auto while
	//     keeping a gate on NEW content flowing in or out — so a
	//     stray skill on one machine doesn't silently propagate to
	//     the fleet, and a teammate's new MCP server doesn't auto-
	//     land in their profile.
	//   - "manual": preview-every-sync cadence v0.2.x shipped; nothing
	//     auto-applies.
	// Empty value is treated as "auto" so new installs get the
	// simplest experience. Existing users are *not* migrated —
	// their explicit AutoApplyClean preference still wins.
	SyncMode string `json:"syncMode,omitempty"`

	// Policies per-category × per-direction. Empty value in any slot is
	// treated as "auto" so existing state.json files from v0.3/v0.4 keep
	// their silent-sync behavior — only users who opt in (or go through
	// the fresh-install wizard) see review prompts. See
	// internal/category for the canonical list of category names.
	Policies CategoryPolicies `json:"policies,omitempty"`

	// DeniedPaths is the per-machine denylist added to by the review
	// screen whenever the user says "don't push this / don't pull this"
	// on a categorized item. Layered on top of profile excludes at sync
	// time; applies equally to push and pull. Repo-relative paths under
	// the active profile prefix (e.g. "claude/commands/work-only.md").
	DeniedPaths []string `json:"deniedPaths,omitempty"`

	// DeniedMCPServers is the per-machine denylist for specific MCP
	// server keys inside ~/.claude.json. Entries are the map keys under
	// $.mcpServers (e.g. "gemini", "notion") — not JSON paths. When a
	// server name appears here, its value under local's ~/.claude.json
	// is preserved through any sync touching mcpServers (same splice
	// trick as jsonfilter.PreserveLocalExcludes).
	DeniedMCPServers []string `json:"deniedMcpServers,omitempty"`

	// TipsSeen tracks one-time educational nudges the user has already
	// been shown, so we don't pester them on every launch. Canonical
	// tip IDs are defined alongside where they're surfaced (e.g.
	// "palette" for the ctrl+k teaching toast). Adding a tip ID to
	// the list is the "mark as read" action.
	TipsSeen []string `json:"tipsSeen,omitempty"`
}

// CategoryPolicies is the (category, direction) → policy matrix stored
// per machine. All fields optional — empty values resolve to "auto" via
// State.PolicyFor so upgrading users keep v0.4.x behavior until they
// opt in from Settings.
type CategoryPolicies struct {
	Agents          DirectionPolicy `json:"agents,omitempty"`
	Skills          DirectionPolicy `json:"skills,omitempty"`
	Commands        DirectionPolicy `json:"commands,omitempty"`
	Memory          DirectionPolicy `json:"memory,omitempty"`
	MCPServers      DirectionPolicy `json:"mcpServers,omitempty"`
	ClaudeMD        DirectionPolicy `json:"claudeMD,omitempty"`
	GeneralSettings DirectionPolicy `json:"generalSettings,omitempty"`
	Other           DirectionPolicy `json:"other,omitempty"`
}

// DirectionPolicy pairs the push and pull policies for one category.
// Either may be "", "auto", "review", or "never"; "" resolves to "auto"
// via State.PolicyFor.
type DirectionPolicy struct {
	Push string `json:"push,omitempty"`
	Pull string `json:"pull,omitempty"`
}

// Direction names a sync direction for PolicyFor lookups.
type Direction string

const (
	DirPush Direction = "push"
	DirPull Direction = "pull"
)

// Policy canonical values. Empty (zero value) is treated as PolicyAuto
// so v0.4 state rolls forward cleanly.
const (
	PolicyAuto   = "auto"
	PolicyReview = "review"
	PolicyNever  = "never"
)

// PolicyFor returns the effective policy for a (category, direction)
// pair. Unknown category names fall back to Other. Empty slots resolve
// to auto.
func (s *State) PolicyFor(category string, dir Direction) string {
	if s == nil {
		return PolicyAuto
	}
	dp := s.Policies.directionPolicy(category)
	var v string
	switch dir {
	case DirPush:
		v = dp.Push
	case DirPull:
		v = dp.Pull
	}
	if v == "" {
		return PolicyAuto
	}
	return v
}

// SetPolicy updates the policy for one (category, direction) pair.
// Returns the previous value so callers can undo or log transitions.
func (s *State) SetPolicy(category string, dir Direction, policy string) string {
	if s == nil {
		return ""
	}
	prev := s.PolicyFor(category, dir)
	switch category {
	case "agents":
		setDir(&s.Policies.Agents, dir, policy)
	case "skills":
		setDir(&s.Policies.Skills, dir, policy)
	case "commands":
		setDir(&s.Policies.Commands, dir, policy)
	case "memory":
		setDir(&s.Policies.Memory, dir, policy)
	case "mcp_servers":
		setDir(&s.Policies.MCPServers, dir, policy)
	case "claude_md":
		setDir(&s.Policies.ClaudeMD, dir, policy)
	case "general_settings":
		setDir(&s.Policies.GeneralSettings, dir, policy)
	default:
		setDir(&s.Policies.Other, dir, policy)
	}
	return prev
}

func (p CategoryPolicies) directionPolicy(category string) DirectionPolicy {
	switch category {
	case "agents":
		return p.Agents
	case "skills":
		return p.Skills
	case "commands":
		return p.Commands
	case "memory":
		return p.Memory
	case "mcp_servers":
		return p.MCPServers
	case "claude_md":
		return p.ClaudeMD
	case "general_settings":
		return p.GeneralSettings
	}
	return p.Other
}

func setDir(dp *DirectionPolicy, dir Direction, policy string) {
	switch dir {
	case DirPush:
		dp.Push = policy
	case DirPull:
		dp.Pull = policy
	}
}

// TipSeen reports whether the named one-time tip (e.g. "palette") has
// already been surfaced to the user. Callers that want to show an
// educational nudge exactly once check this flag, render the tip
// when false, and call MarkTipSeen to flip it.
func (s *State) TipSeen(id string) bool {
	if s == nil {
		return false
	}
	for _, t := range s.TipsSeen {
		if t == id {
			return true
		}
	}
	return false
}

// MarkTipSeen adds id to the TipsSeen list if it isn't already there.
// Idempotent — calling twice is a no-op. Caller is responsible for
// persisting via state.Save.
func (s *State) MarkTipSeen(id string) {
	if s == nil || s.TipSeen(id) {
		return
	}
	s.TipsSeen = append(s.TipsSeen, id)
}

// IsPathDenied reports whether a repo-relative path (already stripped
// of the profile prefix — e.g. "claude/commands/foo.md") is on this
// machine's denylist. Comparison is literal; glob patterns would need
// a matcher which we don't have on this hot path yet.
func (s *State) IsPathDenied(repoRelPath string) bool {
	if s == nil {
		return false
	}
	for _, p := range s.DeniedPaths {
		if p == repoRelPath {
			return true
		}
	}
	return false
}

// IsMCPServerDenied reports whether a specific mcpServers key is on
// this machine's denylist.
func (s *State) IsMCPServerDenied(name string) bool {
	if s == nil {
		return false
	}
	for _, n := range s.DeniedMCPServers {
		if n == name {
			return true
		}
	}
	return false
}

// DenyPath adds a path to the denylist, idempotent. Caller is
// responsible for Save'ing state afterwards.
func (s *State) DenyPath(repoRelPath string) {
	if s == nil || s.IsPathDenied(repoRelPath) {
		return
	}
	s.DeniedPaths = append(s.DeniedPaths, repoRelPath)
}

// AllowPath removes a path from the denylist if present.
func (s *State) AllowPath(repoRelPath string) {
	if s == nil {
		return
	}
	for i, p := range s.DeniedPaths {
		if p == repoRelPath {
			s.DeniedPaths = append(s.DeniedPaths[:i], s.DeniedPaths[i+1:]...)
			return
		}
	}
}

// DenyMCPServer adds a server name to the denylist, idempotent.
func (s *State) DenyMCPServer(name string) {
	if s == nil || s.IsMCPServerDenied(name) {
		return
	}
	s.DeniedMCPServers = append(s.DeniedMCPServers, name)
}

// AllowMCPServer removes a server name from the denylist if present.
func (s *State) AllowMCPServer(name string) {
	if s == nil {
		return
	}
	for i, n := range s.DeniedMCPServers {
		if n == name {
			s.DeniedMCPServers = append(s.DeniedMCPServers[:i], s.DeniedMCPServers[i+1:]...)
			return
		}
	}
}

// IsAutoMode reports whether this machine is running in the default
// "install, sync, forget" mode. Empty string is treated as auto so fresh
// installs get the zero-config experience without a state migration.
// Does NOT return true for "approve" — approve mode wants the review
// screen for new files, so background auto-sync stays off.
func (s *State) IsAutoMode() bool {
	if s == nil {
		return true
	}
	return s.SyncMode == "" || s.SyncMode == "auto"
}

// IsApproveMode reports whether new-file actions (both push-new and
// pull-new) should be routed through the review screen for per-item
// allow/deny, while modifications and deletes flow through
// auto-style. Independent of IsAutoMode so callers can branch on
// background-sync eligibility (auto only) vs. auto-apply-after-
// review eligibility (auto or approve).
func (s *State) IsApproveMode() bool {
	return s != nil && s.SyncMode == "approve"
}

// SyncModeLabel returns a short human label for the current sync
// mode, suitable for the settings row and Home dashboard badge.
func (s *State) SyncModeLabel() string {
	switch {
	case s.IsApproveMode():
		return "approve new files"
	case s.IsAutoMode():
		return "auto"
	default:
		return "manual"
	}
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
			return &State{
				LastSyncedSHA: map[string]string{},
				LastSyncedAt:  map[string]time.Time{},
			}, nil
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
	if s.LastSyncedAt == nil {
		s.LastSyncedAt = map[string]time.Time{}
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
