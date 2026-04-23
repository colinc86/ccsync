package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveWithBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ccsync.yaml")

	c1, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if err := c1.SaveWithBackup(path); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("expected file to exist")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error("no backup should exist after first write")
	}

	c2, _ := LoadDefault()
	c2.Profiles["extra"] = ProfileSpec{Description: "new"}
	if err := c2.SaveWithBackup(path); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Error("expected .bak after second write")
	}

	if err := RestoreBackup(path); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	restored, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.Profiles["extra"]; ok {
		t.Error("restore didn't roll back the 'extra' profile")
	}
}

func TestRestoreBackupMissing(t *testing.T) {
	dir := t.TempDir()
	if err := RestoreBackup(filepath.Join(dir, "nope")); err == nil {
		t.Error("expected error restoring non-existent backup")
	}
}

// TestRestoreBackupCorrupt pins the last line of defense: if a .bak
// ends up corrupt somehow (pre-v0.6.11 non-atomic write + crash;
// someone's filesystem quirk; manual interference), RestoreBackup
// must refuse rather than clobber the live file with garbage.
// Validates the Parse-before-overwrite guard in RestoreBackup.
func TestRestoreBackupCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ccsync.yaml")

	// Seed a valid live file first so we can detect clobber attempts.
	c, _ := LoadDefault()
	if err := c.SaveWithBackup(path); err != nil {
		t.Fatal(err)
	}
	liveBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Plant a truncated .bak — what a partial non-atomic write would
	// look like mid-crash.
	if err := os.WriteFile(path+".bak", []byte("profiles:\n  default:\n    descr"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RestoreBackup(path); err == nil {
		t.Fatal("expected RestoreBackup to reject a corrupt .bak; got nil — live file would be clobbered")
	}

	// The live file must still be intact.
	liveAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(liveAfter) != string(liveBefore) {
		t.Error("live file was clobbered despite RestoreBackup rejecting the .bak")
	}
}

// TestSaveWithBackupAtomicity pins the iteration-13 fix: the backup
// write must not leave a half-staged ".bak.tmp" behind on a
// successful save. Pre-fix the .bak was written directly with
// WriteFile — non-atomic, so a crash or disk-full mid-write left
// .bak truncated and RestoreBackup (which validates via Parse)
// rejected it. The fix writes to .bak.tmp then renames; this test
// guards that the tmp file is always cleaned up and that .bak always
// parses cleanly as valid YAML after every save.
func TestSaveWithBackupAtomicity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ccsync.yaml")

	// First save — no backup should exist yet.
	c1, _ := LoadDefault()
	if err := c1.SaveWithBackup(path); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Simulate ten edit cycles. Every one must leave .bak parseable
	// and no .bak.tmp sibling lingering.
	for i := 0; i < 10; i++ {
		c, _ := LoadDefault()
		c.Profiles["edit-"+string(rune('a'+i))] = ProfileSpec{Description: "round"}
		if err := c.SaveWithBackup(path); err != nil {
			t.Fatalf("save round %d: %v", i, err)
		}

		// No partial .bak.tmp should linger.
		if _, err := os.Stat(path + ".bak.tmp"); err == nil {
			t.Errorf("round %d: .bak.tmp left behind — atomic write didn't clean up", i)
		}

		// .bak must always parse. If SaveWithBackup writes a
		// truncated backup, RestoreBackup would reject it — which
		// means this file should parse directly as valid YAML.
		bakData, err := os.ReadFile(path + ".bak")
		if err != nil {
			t.Errorf("round %d: .bak missing: %v", i, err)
			continue
		}
		if _, err := Parse(bakData); err != nil {
			t.Errorf("round %d: .bak doesn't parse as valid YAML: %v", i, err)
		}
	}
}
