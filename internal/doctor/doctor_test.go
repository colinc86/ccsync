package doctor

import (
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"

	cryptopkg "github.com/colinc86/ccsync/internal/crypto"
	"github.com/colinc86/ccsync/internal/secrets"
)

func TestCheckAllOK(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeJSON := filepath.Join(tmp, ".claude.json")
	if err := os.WriteFile(claudeJSON, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	repoPath := filepath.Join(tmp, "repo")
	if _, err := gogit.PlainInit(repoPath, false); err != nil {
		t.Fatal(err)
	}

	r := Check(Inputs{
		ClaudeDir: claudeDir, ClaudeJSON: claudeJSON,
		RepoPath: repoPath, StateDir: filepath.Join(tmp, ".ccsync"),
	})
	if r.Worst() != SeverityOK {
		t.Errorf("expected all OK, got worst=%s findings=%v", r.Worst(), r.Findings)
	}
}

func TestCheckFlagsDanglingPlaceholders(t *testing.T) {
	tmp := t.TempDir()
	claudeJSON := filepath.Join(tmp, ".claude.json")
	if err := os.WriteFile(claudeJSON, []byte(`{"key":"<<REDACTED:ccsync:default:x.y>>"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Check(Inputs{ClaudeJSON: claudeJSON})
	if r.Worst() != SeverityFail {
		t.Errorf("expected FAIL, got %s: %v", r.Worst(), r.Findings)
	}
}

// TestEncryptionHalfMigrationAutoFix pins the iteration-37 invariant:
// when a repo has a ccsync encryption marker but NO stored passphrase
// AND no encrypted files under profiles/, that's a dead marker left
// over from a failed `ccsync encrypt` run. doctor --fix should remove
// the marker to restore the repo to a plaintext-consistent state.
// The safeguard: fix is ONLY wired when firstCiphertext returns nil —
// otherwise there's real ciphertext in play and removing the marker
// would lock the user out. This test pins both halves of that gate.
func TestEncryptionHalfMigrationAutoFix(t *testing.T) {
	secrets.MockInit()
	_ = secrets.Delete("repo-encryption-passphrase")

	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repoPath, "profiles/default/claude/agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gogit.PlainInit(repoPath, false); err != nil {
		t.Fatal(err)
	}

	// Half-migration: marker present, no ciphertext, no passphrase.
	marker, err := cryptopkg.NewMarker()
	if err != nil {
		t.Fatal(err)
	}
	if err := cryptopkg.WriteMarker(repoPath, marker); err != nil {
		t.Fatal(err)
	}

	r := Check(Inputs{RepoPath: repoPath, StateDir: filepath.Join(tmp, ".ccsync")})
	var encF *Finding
	for i := range r.Findings {
		if r.Findings[i].Check == "encryption" {
			encF = &r.Findings[i]
			break
		}
	}
	if encF == nil {
		t.Fatal("no encryption finding")
	}
	if encF.Fix == nil {
		t.Fatalf("expected Fix for half-migration state; finding = %+v", *encF)
	}
	if err := encF.Fix(); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if _, err := cryptopkg.ReadMarker(repoPath); err != nil {
		t.Fatalf("ReadMarker after Fix: %v", err)
	} else if m, _ := cryptopkg.ReadMarker(repoPath); m != nil {
		t.Error("marker should be gone after Fix")
	}

	// Re-run with ciphertext present AND no passphrase — Fix must be
	// nil so doctor --fix can't nuke a marker that's protecting real
	// data.
	if err := cryptopkg.WriteMarker(repoPath, marker); err != nil {
		t.Fatal(err)
	}
	// Derive a key just to produce valid ciphertext; the doctor code
	// only checks for the magic header, not that it decrypts.
	key, err := marker.DeriveKey("throwaway-for-test-ciphertext")
	if err != nil {
		t.Fatal(err)
	}
	ct, err := cryptopkg.Encrypt(key, []byte("realish content"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "profiles/default/claude/agents/foo.md"), ct, 0o644); err != nil {
		t.Fatal(err)
	}

	r2 := Check(Inputs{RepoPath: repoPath, StateDir: filepath.Join(tmp, ".ccsync")})
	var encF2 *Finding
	for i := range r2.Findings {
		if r2.Findings[i].Check == "encryption" {
			encF2 = &r2.Findings[i]
			break
		}
	}
	if encF2 == nil {
		t.Fatal("no encryption finding on second check")
	}
	if encF2.Severity != SeverityFail {
		t.Errorf("missing passphrase with ciphertext must FAIL; got %s", encF2.Severity)
	}
	if encF2.Fix != nil {
		t.Fatal("Fix must NOT be wired when ciphertext is present — would silently destroy the user's encryption, locking them out of real data")
	}
}

// TestSnapshotsFixRemovesMultipleOrphans pins the iteration-19 fix:
// the orphan-cleanup Fix now attempts every orphan even when one
// fails. Pre-fix, the loop returned on the first RemoveAll error,
// leaving the rest untouched — one stuck dir (permission denied,
// filesystem lock) would wedge the whole cleanup.
func TestSnapshotsFixRemovesMultipleOrphans(t *testing.T) {
	tmp := t.TempDir()
	snaps := filepath.Join(tmp, "snapshots")

	// Three orphan dirs (no meta.json).
	for _, name := range []string{"orphan-a", "orphan-b", "orphan-c"} {
		if err := os.MkdirAll(filepath.Join(snaps, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	r := Check(Inputs{StateDir: tmp})
	var snapFinding *Finding
	for i := range r.Findings {
		if r.Findings[i].Check == "snapshots" {
			snapFinding = &r.Findings[i]
			break
		}
	}
	if snapFinding == nil {
		t.Fatal("no snapshots finding")
	}
	if snapFinding.Severity != SeverityWarn {
		t.Errorf("orphans should WARN; got %s", snapFinding.Severity)
	}
	if snapFinding.Fix == nil {
		t.Fatal("expected Fix on orphans finding")
	}
	if err := snapFinding.Fix(); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	// All three orphan dirs must be gone.
	for _, name := range []string{"orphan-a", "orphan-b", "orphan-c"} {
		if _, err := os.Stat(filepath.Join(snaps, name)); !os.IsNotExist(err) {
			t.Errorf("orphan %s still exists after Fix — pre-fix would have stopped on the first error and left the rest", name)
		}
	}
}
