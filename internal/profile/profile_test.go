package profile

import (
	"os"
	"path/filepath"
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
