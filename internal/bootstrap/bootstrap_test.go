package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/state"
)

func TestRunLocalBare(t *testing.T) {
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	if _, err := gogit.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(tmp, ".ccsync")

	st, err := Run(context.Background(), Inputs{
		Source:   SourceLocalBare,
		RepoURL:  bareDir,
		StateDir: stateDir,
		Auth:     state.AuthSSH,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.SyncRepoURL != bareDir {
		t.Errorf("SyncRepoURL = %q", st.SyncRepoURL)
	}
	if st.ActiveProfile != "default" {
		t.Errorf("ActiveProfile = %q", st.ActiveProfile)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "repo", ".syncignore")); err != nil {
		t.Error(".syncignore wasn't seeded:", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "repo", "ccsync.yaml")); err != nil {
		t.Error("ccsync.yaml wasn't seeded:", err)
	}
}

func TestRunExistingDirFails(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, ".ccsync")
	// Pre-create the repo subdir so Run sees it
	if err := os.MkdirAll(filepath.Join(stateDir, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Inputs{
		Source: SourceLocalBare, RepoURL: filepath.Join(tmp, "bare"),
		StateDir: stateDir, Auth: state.AuthSSH,
	})
	if err == nil {
		t.Fatal("expected error for pre-existing repo dir")
	}
}

// TestRunNonExistentRepoSurfacesError pins the iteration-9 fix: when
// Clone fails for any reason OTHER than "remote is empty" (e.g. a
// URL that doesn't resolve, auth failures, network errors), Bootstrap
// must surface the error. Pre-fix the Init fallback ran
// unconditionally, so pointing at a bogus URL silently "bootstrapped"
// a worktree with a broken origin — the user saw success at the TUI
// level, then every future sync failed with the same origin error.
//
// We use a non-existent local path because it reliably fails Clone
// (no remote to read refs from) and goes through exactly the code
// path that had the bug.
func TestRunNonExistentRepoSurfacesError(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, ".ccsync")
	bogusURL := filepath.Join(tmp, "does-not-exist.git")

	_, err := Run(context.Background(), Inputs{
		Source:   SourceLocalBare,
		RepoURL:  bogusURL,
		StateDir: stateDir,
		Auth:     state.AuthSSH,
	})
	if err == nil {
		t.Fatal("expected clone error to surface; got nil — Init fallback silently succeeded, next sync would break")
	}
	// Belt-and-braces: no worktree should linger after a failed
	// bootstrap; the user's retry should get a clean slate.
	if _, statErr := os.Stat(filepath.Join(stateDir, "repo", ".git")); statErr == nil {
		t.Error("worktree left behind after failed bootstrap — a retry would hit 'already exists'")
	}
}

// TestRunCreatesMissingProfileInConfig pins the iteration-39 fix:
// when bootstrap is invoked with --profile X and X isn't in the
// seeded / cloned ccsync.yaml, bootstrap must add X to the config
// (and persist it) so the next sync doesn't immediately die with
// "resolve profile X: unknown profile X". Pre-fix: the user's
// bootstrap command returned "bootstrapped ✓" but every sync
// afterwards errored — the user was stuck in a state that required
// either hand-editing ccsync.yaml or running `ccsync profile create`.
func TestRunCreatesMissingProfileInConfig(t *testing.T) {
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	if _, err := gogit.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(tmp, ".ccsync")

	st, err := Run(context.Background(), Inputs{
		Source:   SourceLocalBare,
		RepoURL:  bareDir,
		Profile:  "work",
		StateDir: stateDir,
		Auth:     state.AuthSSH,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want work", st.ActiveProfile)
	}

	// The seeded ccsync.yaml at the repo root must now contain the
	// "work" profile — otherwise the first sync immediately errors
	// with "unknown profile" and the user is stuck.
	cfg, err := config.Load(filepath.Join(stateDir, "repo", "ccsync.yaml"))
	if err != nil {
		t.Fatalf("load seeded config: %v", err)
	}
	if _, ok := cfg.Profiles["work"]; !ok {
		t.Errorf("bootstrap didn't add requested profile %q to ccsync.yaml; profiles = %+v", "work", keysOf(cfg.Profiles))
	}
	// The default profile should still be present too — adding a
	// missing profile must not clobber the existing set.
	if _, ok := cfg.Profiles["default"]; !ok {
		t.Errorf("bootstrap removed the default profile while adding %q", "work")
	}
}

func keysOf(m map[string]config.ProfileSpec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
