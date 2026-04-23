// Package bootstrap owns the first-run flow: clone or create the sync repo,
// seed .syncignore + ccsync.yaml from embedded defaults, persist state.json.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/state"
)

// Source discriminates the repo origin for Bootstrap.
type Source int

const (
	SourceExisting  Source = iota // clone an existing URL
	SourceGHCreate                // create a new private repo via gh CLI
	SourceLocalBare               // point at a local bare repo path
)

// Inputs configure Bootstrap.
type Inputs struct {
	Source    Source
	RepoURL   string // SourceExisting / SourceLocalBare
	RepoName  string // SourceGHCreate (e.g. "claude-settings")
	Profile   string // initial active profile (default "default")
	StateDir  string // ~/.ccsync
	Auth      state.AuthKind
	SSHKey    string
	HTTPSUser string
}

// Run performs bootstrap. Returns the populated State that the caller should
// save (Bootstrap writes it as a convenience).
func Run(ctx context.Context, in Inputs) (*state.State, error) {
	if in.Profile == "" {
		in.Profile = "default"
	}
	if in.StateDir == "" {
		return nil, errors.New("StateDir required")
	}

	repoPath := filepath.Join(in.StateDir, "repo")
	if _, err := os.Stat(repoPath); err == nil {
		return nil, fmt.Errorf("%s already exists; remove it before re-bootstrapping", repoPath)
	}

	targetURL, err := resolveSource(ctx, in)
	if err != nil {
		return nil, err
	}

	auth, authErr := gitx.AuthConfig{
		Kind:       gitxKind(in.Auth),
		SSHKeyPath: in.SSHKey,
		HTTPSUser:  in.HTTPSUser,
	}.Resolve()
	if authErr != nil {
		return nil, fmt.Errorf("auth setup failed: %w%s", authErr, authHint(in.Auth))
	}

	if _, err := gitx.Clone(ctx, targetURL, repoPath, auth); err != nil {
		// Clean up the partial clone directory so the user can retry
		// without hitting "already exists" from the guard above.
		_ = os.RemoveAll(repoPath)

		// Only fall back to Init for "remote is empty" — that's the
		// legitimate "I just created this repo and it has no commits
		// yet" case. For auth failures, network errors, or
		// not-found, we must surface the error instead of silently
		// bootstrapping into a broken state where every future sync
		// fails with the same auth error. Pre-v0.6.7 the init fallback
		// was unconditional: clone-auth-failed → init-succeeded →
		// user sees "bootstrapped ✓" then every sync errors.
		if !errors.Is(err, gitx.ErrEmptyRemote) {
			return nil, err
		}
		if _, initErr := gitx.Init(repoPath, targetURL); initErr != nil {
			return nil, fmt.Errorf("clone failed (%v) and init fallback failed: %w", err, initErr)
		}
	}

	if err := seedRepo(repoPath); err != nil {
		return nil, fmt.Errorf("seed repo: %w", err)
	}

	// Ensure the requested profile exists in ccsync.yaml. If the user
	// bootstrapped with --profile work against an empty (init-fallback)
	// remote, the seeded yaml only has "default"; without this step the
	// next sync would die with "unknown profile" and the user is stuck
	// with no indication of how to recover. If the remote already had a
	// matching entry (normal clone of an existing fleet repo), this is
	// a no-op.
	if err := ensureRequestedProfile(repoPath, in.Profile); err != nil {
		return nil, fmt.Errorf("ensure profile %q: %w", in.Profile, err)
	}

	st, err := state.Load(in.StateDir)
	if err != nil {
		return nil, err
	}
	st.EnsureHostUUID()
	st.SyncRepoURL = targetURL
	st.Auth = in.Auth
	st.SSHKeyPath = in.SSHKey
	st.HTTPSUser = in.HTTPSUser
	st.ActiveProfile = in.Profile
	if err := state.Save(in.StateDir, st); err != nil {
		return nil, err
	}
	return st, nil
}

// authHint returns a parenthesized recovery hint tailored to the auth
// kind being attempted. Pre-v0.6.7 this was a single string that told
// HTTPS users to "unlock your SSH key with `ssh-add`" — confusing for
// the exact users whose path had already picked HTTPS. Separating the
// hints by kind keeps the tail of the error relevant to whatever the
// user was trying to do.
func authHint(k state.AuthKind) string {
	switch k {
	case state.AuthSSH:
		return " (hint: unlock your SSH key with `ssh-add`, or configure a specific key via the Settings screen)"
	case state.AuthHTTPS:
		return " (hint: set GITHUB_TOKEN / GH_TOKEN, or configure a token for this host)"
	}
	return ""
}

func gitxKind(k state.AuthKind) gitx.AuthKind {
	switch k {
	case state.AuthSSH:
		return gitx.AuthSSH
	case state.AuthHTTPS:
		return gitx.AuthHTTPS
	}
	return gitx.AuthNone
}

func resolveSource(ctx context.Context, in Inputs) (string, error) {
	switch in.Source {
	case SourceExisting, SourceLocalBare:
		if in.RepoURL == "" {
			return "", errors.New("RepoURL required")
		}
		return in.RepoURL, nil
	case SourceGHCreate:
		return createViaGH(ctx, in.RepoName)
	}
	return "", fmt.Errorf("unknown source: %d", in.Source)
}

// GHAvailable reports whether the `gh` CLI is present and authenticated.
func GHAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "status").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Logged in to")
}

func createViaGH(ctx context.Context, repoName string) (string, error) {
	if repoName == "" {
		repoName = "claude-settings"
	}
	if !GHAvailable(ctx) {
		return "", errors.New("gh CLI isn't installed or isn't authenticated; run `gh auth login` first")
	}
	out, err := exec.CommandContext(ctx, "gh", "repo", "create",
		"--private", "--clone=false", "--disable-issues", "--disable-wiki",
		repoName).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh repo create failed: %s", string(out))
	}
	// gh prints the repo URL on success; capture the https URL
	// but we prefer ssh since most users are SSH.
	user, err := ghCurrentUser(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("git@github.com:%s/%s.git", user, repoName), nil
}

func ghCurrentUser(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		return "", fmt.Errorf("gh api user: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func seedRepo(repoPath string) error {
	seeds := map[string][]byte{
		".syncignore": []byte(defaultSyncignore()),
		"ccsync.yaml": config.DefaultYAML(),
		".gitignore":  config.DefaultGitignore(),
	}
	for name, data := range seeds {
		path := filepath.Join(repoPath, name)
		if _, err := os.Stat(path); err == nil {
			continue // don't overwrite
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func defaultSyncignore() string {
	c, err := config.LoadDefault()
	if err != nil {
		return ""
	}
	return c.DefaultSyncignore
}

// ensureRequestedProfile loads ccsync.yaml from repoPath and, if the
// requested profile isn't in it, adds an entry with a minimal
// description and re-saves atomically via SaveWithBackup. A no-op when
// the profile is already present or the config can't be loaded (the
// latter case is surfaced as an error because the subsequent sync
// would fail anyway).
func ensureRequestedProfile(repoPath, profile string) error {
	if profile == "" {
		return nil
	}
	cfgPath := filepath.Join(repoPath, "ccsync.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if _, ok := cfg.Profiles[profile]; ok {
		return nil
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]config.ProfileSpec{}
	}
	cfg.Profiles[profile] = config.ProfileSpec{Description: profile + " profile"}
	return cfg.SaveWithBackup(cfgPath)
}
