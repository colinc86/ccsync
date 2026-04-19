package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"

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
