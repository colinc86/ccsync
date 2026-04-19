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
