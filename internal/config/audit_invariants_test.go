package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoreBackupAtomic pins iter-43 audit fix: RestoreBackup uses
// the tmp+rename pattern, matching SaveWithBackup. Pre-fix it did
// a direct os.WriteFile, so a crash or disk-full mid-restore
// truncated the live ccsync.yaml — same class of bug as the
// pre-v0.6.11 SaveWithBackup race, just on the restore path. Check:
// after RestoreBackup, no `.tmp` sibling lingers and the live file
// parses cleanly.
func TestRestoreBackupAtomic(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ccsync.yaml")

	c := &Config{Profiles: map[string]ProfileSpec{
		"default": {Description: "Primary"},
	}}
	if err := c.SaveWithBackup(path); err != nil {
		t.Fatal(err)
	}
	c.Profiles["work"] = ProfileSpec{Description: "work"}
	if err := c.SaveWithBackup(path); err != nil {
		t.Fatal(err)
	}
	// The .bak now has the "default-only" shape; restore it.
	if err := RestoreBackup(path); err != nil {
		t.Fatal(err)
	}
	// No stray .tmp.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("stray .tmp after RestoreBackup: %s", e.Name())
		}
	}
	// Live file parses.
	c2, err := Load(path)
	if err != nil {
		t.Fatalf("restored config doesn't parse: %v", err)
	}
	if _, ok := c2.Profiles["work"]; ok {
		t.Error("restored config still has 'work' — restore didn't roll back")
	}
	if _, ok := c2.Profiles["default"]; !ok {
		t.Error("restored config lost 'default' — restore clobbered the live file")
	}
}

// TestEffectiveProfileChainBoundedByProfileCount pins iter-43 audit:
// EffectiveProfile's visited-set cycle detection effectively bounds
// the chain depth by len(cfg.Profiles). A pathological config with
// deep extends chains doesn't recurse unbounded; it terminates at a
// cycle or at an undeclared parent. No explicit depth cap needed.
func TestEffectiveProfileChainBoundedByProfileCount(t *testing.T) {
	cfg := &Config{Profiles: map[string]ProfileSpec{}}
	// Build a 50-deep extends chain: p0 extends p1 extends ... p49.
	for i := 0; i < 50; i++ {
		name := "p" + itoa(i)
		var parent string
		if i < 49 {
			parent = "p" + itoa(i+1)
		}
		cfg.Profiles[name] = ProfileSpec{Description: name, Extends: parent}
	}
	r, err := EffectiveProfile(cfg, "p0")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Chain) != 50 {
		t.Errorf("expected 50-deep chain, got %d", len(r.Chain))
	}
	// A self-loop must still be caught.
	cfg.Profiles["loop"] = ProfileSpec{Extends: "loop"}
	if _, err := EffectiveProfile(cfg, "loop"); err == nil {
		t.Error("self-loop should be rejected")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
