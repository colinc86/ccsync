package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/state"
)

func TestCreateDelete(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ccsync.yaml")
	cfg, _ := config.LoadDefault()
	if err := cfg.SaveWithBackup(cfgPath); err != nil {
		t.Fatal(err)
	}

	if err := Create(cfg, cfgPath, "work", "Work profile"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Profiles["work"]; !ok {
		t.Fatal("profile not persisted")
	}

	if err := Create(cfg, cfgPath, "work", "dup"); err == nil {
		t.Error("duplicate Create should fail")
	}
	if err := Delete(cfg, cfgPath, "work", "default"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := Delete(cfg, cfgPath, "default", "default"); err == nil {
		t.Error("deleting active profile should fail")
	}
	if err := Delete(cfg, cfgPath, "nosuch", "default"); err == nil {
		t.Error("deleting missing profile should fail")
	}
	if err := Delete(cfg, cfgPath, "default", "other"); err == nil {
		t.Error("deleting last profile should fail")
	}
}

func TestSwitch(t *testing.T) {
	dir := t.TempDir()

	// Create a file to capture in the backup
	fakeFile := filepath.Join(dir, "claude-file.md")
	if err := os.WriteFile(fakeFile, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &state.State{
		ActiveProfile: "default",
		HostUUID:      "h",
		LastSyncedSHA: map[string]string{},
	}
	if err := state.Save(dir, st); err != nil {
		t.Fatal(err)
	}

	meta, err := Switch(st, dir, "work", []string{fakeFile})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if st.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q", st.ActiveProfile)
	}
	if meta.ID == "" {
		t.Error("expected snapshot meta")
	}

	// Reload and confirm
	reloaded, err := state.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ActiveProfile != "work" {
		t.Errorf("reloaded ActiveProfile = %q", reloaded.ActiveProfile)
	}

	// Switching to same profile is a no-op
	meta2, err := Switch(st, dir, "work", []string{fakeFile})
	if err != nil {
		t.Fatal(err)
	}
	if meta2.ID != "" {
		t.Error("expected no snapshot when switching to same profile")
	}
}

// TestDeleteRejectsParentWithDescendants pins the iteration-11 fix:
// if profile "work" extends "default", deleting "default" must be
// rejected — otherwise "work" becomes an orphan whose extends chain
// references a profile that no longer exists. Pre-fix Delete only
// checked (a) it's not the active profile, (b) it exists, (c) it's
// not the last one; it did NOT check whether anyone else depended on
// it. Symptom in the wild: user deletes "default", next sync on any
// descendant profile fails with "extends unknown profile 'default'"
// — confusing because the user just sanitized their config.
func TestDeleteRejectsParentWithDescendants(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ccsync.yaml")
	cfg, _ := config.LoadDefault()
	if err := cfg.SaveWithBackup(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Create "work" extending "default".
	if err := Create(cfg, cfgPath, "work", "work laptop"); err != nil {
		t.Fatal(err)
	}
	spec := cfg.Profiles["work"]
	spec.Extends = "default"
	cfg.Profiles["work"] = spec
	if err := cfg.SaveWithBackup(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Try to delete "default" — should be rejected because "work"
	// depends on it. activeProfile="work" so the first guard
	// (can't-delete-active) doesn't accidentally catch this.
	err := Delete(cfg, cfgPath, "default", "work")
	if err == nil {
		t.Fatal("expected Delete to reject a parent with descendants; got nil — work would be orphaned")
	}

	// "default" must still be in the config — Delete shouldn't have
	// half-applied.
	if _, ok := cfg.Profiles["default"]; !ok {
		t.Error("default was removed despite the error being returned — half-applied delete")
	}

	// "work" without its parent: also confirm the error message
	// actually names the dependent so the user knows what to fix.
	if err != nil && !strings.Contains(err.Error(), "work") {
		t.Errorf("error message should name the dependent profile; got: %v", err)
	}
}

// TestEffectiveProfileThreeDeep pins correct ordering and
// accumulation for a multi-level extends chain — nothing tested it
// directly before, so a refactor that broke the walk order (e.g.,
// accidentally appending parent-first instead of leaf-first) would
// slip through.
func TestEffectiveProfileThreeDeep(t *testing.T) {
	cfg, err := config.Parse([]byte(`
profiles:
  grandparent:
    description: gp
    exclude:
      paths: ["claude/gp-only"]
  parent:
    extends: grandparent
    exclude:
      paths: ["claude/parent-only"]
  leaf:
    description: leaf desc
    extends: parent
    exclude:
      paths: ["claude/leaf-only"]
`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := config.EffectiveProfile(cfg, "leaf")
	if err != nil {
		t.Fatalf("EffectiveProfile: %v", err)
	}
	wantChain := []string{"leaf", "parent", "grandparent"}
	if len(r.Chain) != 3 {
		t.Fatalf("chain length = %d, want 3: %v", len(r.Chain), r.Chain)
	}
	for i, want := range wantChain {
		if r.Chain[i] != want {
			t.Errorf("chain[%d] = %q, want %q (order must be leaf→root)", i, r.Chain[i], want)
		}
	}
	if len(r.PathExcludes) != 3 {
		t.Errorf("excludes should accumulate across all 3 levels; got %d: %v", len(r.PathExcludes), r.PathExcludes)
	}
	if r.Description != "leaf desc" {
		t.Errorf("leaf description must win over ancestor descriptions; got %q", r.Description)
	}
}
