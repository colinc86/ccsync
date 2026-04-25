package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/colinc86/ccsync/internal/state"
)

// writeRepoREADME regenerates README.md at the repo root. It's a human-facing
// snapshot of what the repo contains; never read by ccsync itself.
//
// Guarded by looksLikeSyncRepo: a directory that doesn't carry the
// signature of a ccsync sync repo (a profiles/ directory, or being
// completely empty / brand-new) is left alone. This is the fix for
// the v0.8.x bug where dogfooding ccsync against the project source
// tree wrote a sync-repo boilerplate over the project README.
func writeRepoREADME(repoPath string, profiles []string, st *state.State, hostName string) error {
	if !looksLikeSyncRepo(repoPath) {
		return nil
	}
	sort.Strings(profiles)

	var sb strings.Builder
	sb.WriteString("# ccsync sync repo\n\n")
	sb.WriteString("This repo is managed by [ccsync](https://github.com/colinc86/ccsync). It stores\n")
	sb.WriteString("the *content* portion of one or more Claude Code installs — agents, skills,\n")
	sb.WriteString("commands, hooks, output styles, memory, CLAUDE.md, and the MCP server slices\n")
	sb.WriteString("from `~/.claude.json` and `~/.claude/settings.json`. Settings stay machine-\n")
	sb.WriteString("local; only content rides the repo.\n\n")

	sb.WriteString("## Profiles\n\n")
	if len(profiles) == 0 {
		sb.WriteString("_(no profiles yet — run `ccsync sync` to populate)_\n\n")
	}
	for _, p := range profiles {
		sb.WriteString("- `" + p + "`\n")
	}

	sb.WriteString("\n## Per-profile layout\n\n")
	sb.WriteString("```\n")
	sb.WriteString("profiles/<name>/\n")
	sb.WriteString("  .ccsync.mcp.json     # ~/.claude.json:$.mcpServers\n")
	sb.WriteString("  ccsync.mcp.json      # ~/.claude/settings.json:$.mcpServers\n")
	sb.WriteString("  ccsync.hooks.json    # ~/.claude/settings.json:$.hooks\n")
	sb.WriteString("  claude/\n")
	sb.WriteString("    CLAUDE.md\n")
	sb.WriteString("    agents/  skills/  commands/  hooks/  output-styles/  memory/\n")
	sb.WriteString("```\n\n")

	sb.WriteString("## Last sync\n\n")
	fmt.Fprintf(&sb, "- **host:** %s\n", hostName)
	activeProfile := "(unknown)"
	if st != nil {
		activeProfile = st.ActiveProfile
	}
	fmt.Fprintf(&sb, "- **active profile:** %s\n", activeProfile)
	fmt.Fprintf(&sb, "- **time:** %s UTC\n", time.Now().UTC().Format(time.RFC3339))

	sb.WriteString("\n## What's safe to edit by hand\n\n")
	sb.WriteString("- `.syncignore` — gitignore-syntax rules for what ccsync sends up\n")
	sb.WriteString("- `ccsync.yaml` — per-profile excludes\n\n")
	sb.WriteString("Everything under `profiles/<name>/` is auto-generated. If you hand-edit a\n")
	sb.WriteString("profile file, the next ccsync run will surface that as a three-way conflict\n")
	sb.WriteString("— no silent clobber.\n")

	return writeFileAtomic(filepath.Join(repoPath, "README.md"), []byte(sb.String()))
}

// looksLikeSyncRepo reports whether repoPath has the signature of a
// ccsync sync repo. Two signals count as "yes":
//
//   - A `profiles/` directory exists. Any past ccsync run created it,
//     and no other Go project would put one at the repo root.
//   - The repo is brand-new — nothing on disk yet, or only ccsync's
//     own seed files (.syncignore, manifest.json, ccsync.yaml). This
//     covers the very-first-sync case where profiles/ doesn't exist
//     yet but we still need to write the README.
//
// Repos that contain unrelated source trees (cmd/, internal/, src/,
// vendor/, package.json, Cargo.toml, …) get neither signal and are
// left alone. This is the guard that stops `ccsync bootstrap --repo .`
// against the ccsync project root from stomping the project README.
func looksLikeSyncRepo(repoPath string) bool {
	if repoPath == "" {
		return false
	}
	if isDir(filepath.Join(repoPath, "profiles")) {
		return true
	}
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if name == ".git" {
			continue
		}
		if isCcsyncSeed(name) {
			continue
		}
		return false
	}
	return true
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isCcsyncSeed(name string) bool {
	switch name {
	case ".syncignore", ".gitignore", "manifest.json", "ccsync.yaml", "README.md":
		return true
	}
	return false
}

// errOldFormatRepo signals that the repo on disk was written by a
// pre-v0.9.0 ccsync. Surfaced verbatim by the orchestrator so the
// CLI can format it for the user; no programmatic recovery path —
// the user clean-installs as covered in the upgrade docs.
var errOldFormatRepo = errors.New("ccsync repo was written by an older version (< v0.9.0); please uninstall ccsync, delete the repo, and re-bootstrap a fresh one")

// detectOldFormat looks for repo paths only an older ccsync would
// have written. Hits on any of these means the active codebase can't
// safely sync into the repo — the schema and write paths diverged
// far enough in v0.9.0 that mixing the two would corrupt the repo.
func detectOldFormat(repoPath string) error {
	if repoPath == "" {
		return nil
	}
	profilesDir := filepath.Join(repoPath, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		profilePath := filepath.Join(profilesDir, e.Name())
		// settings.json or claude.json living *inside* a profile is
		// the v0.8.x signature. v0.9.0 never writes either.
		if _, err := os.Stat(filepath.Join(profilePath, "claude", "settings.json")); err == nil {
			return errOldFormatRepo
		}
		if _, err := os.Stat(filepath.Join(profilePath, "claude.json")); err == nil {
			return errOldFormatRepo
		}
	}
	return nil
}
