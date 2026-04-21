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

func TestPolicyForDefaultsAuto(t *testing.T) {
	var s State
	if got := s.PolicyFor("agents", DirPush); got != PolicyAuto {
		t.Errorf("unset policy should be auto; got %q", got)
	}
	if got := s.PolicyFor("unknown_category", DirPull); got != PolicyAuto {
		t.Errorf("unknown category should fall through to auto; got %q", got)
	}
}

func TestPolicyRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	s := &State{}
	s.SetPolicy("commands", DirPush, PolicyReview)
	s.SetPolicy("skills", DirPull, PolicyNever)
	s.DenyPath("claude/commands/work-only.md")
	s.DenyMCPServer("internal-tool")

	if err := Save(tmp, s); err != nil {
		t.Fatal(err)
	}
	back, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if back.PolicyFor("commands", DirPush) != PolicyReview {
		t.Errorf("commands push policy lost: %q", back.PolicyFor("commands", DirPush))
	}
	if back.PolicyFor("skills", DirPull) != PolicyNever {
		t.Errorf("skills pull policy lost: %q", back.PolicyFor("skills", DirPull))
	}
	if back.PolicyFor("agents", DirPush) != PolicyAuto {
		t.Errorf("untouched category should still be auto; got %q", back.PolicyFor("agents", DirPush))
	}
	if !back.IsPathDenied("claude/commands/work-only.md") {
		t.Error("denied path lost in round-trip")
	}
	if !back.IsMCPServerDenied("internal-tool") {
		t.Error("denied mcp server lost in round-trip")
	}
}

func TestDenyAllowPathIsIdempotent(t *testing.T) {
	s := &State{}
	s.DenyPath("x")
	s.DenyPath("x")
	if len(s.DeniedPaths) != 1 {
		t.Errorf("duplicate deny should be a no-op; got %d entries", len(s.DeniedPaths))
	}
	s.AllowPath("x")
	if len(s.DeniedPaths) != 0 {
		t.Errorf("allow after deny should remove; got %d entries", len(s.DeniedPaths))
	}
	s.AllowPath("x") // allow on absent is fine
}
