package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRetentionDefaults(t *testing.T) {
	s := &State{}
	c, d := s.SnapshotRetention()
	if c != 30 || d != 14 {
		t.Fatalf("defaults: got (%d, %d), want (30, 14)", c, d)
	}
}

func TestSnapshotRetentionCustom(t *testing.T) {
	s := &State{SnapshotMaxCount: 5, SnapshotMaxAgeDays: 2}
	c, d := s.SnapshotRetention()
	if c != 5 || d != 2 {
		t.Fatalf("custom: got (%d, %d), want (5, 2)", c, d)
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	s := &State{
		SyncRepoURL:        "git@example.com:me/repo.git",
		Auth:               AuthSSH,
		ActiveProfile:      "work",
		HostClass:          "work",
		AuthorName:         "Colin",
		AuthorEmail:        "me@example.com",
		SecretsBackend:     SecretsBackendFile,
		SnapshotMaxCount:   5,
		SnapshotMaxAgeDays: 2,
		AutoApplyClean:     true,
		LastSyncedSHA:      map[string]string{"work": "abc123"},
	}
	if err := Save(tmp, s); err != nil {
		t.Fatal(err)
	}
	back, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if back.AuthorEmail != "me@example.com" || back.AuthorName != "Colin" {
		t.Errorf("author round-trip failed: %+v", back)
	}
	if back.SecretsBackend != SecretsBackendFile {
		t.Errorf("secrets backend round-trip failed: %q", back.SecretsBackend)
	}
	if !back.AutoApplyClean {
		t.Error("auto-apply-clean round-trip failed")
	}
	info, err := os.Stat(filepath.Join(tmp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v; want 0600", info.Mode().Perm())
	}
}
