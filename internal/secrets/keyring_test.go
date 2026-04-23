package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKey(t *testing.T) {
	if got := Key("default", "foo.bar"); got != "default:foo.bar" {
		t.Errorf("Key = %q", got)
	}
}

func TestStoreFetchDelete(t *testing.T) {
	MockInit()

	k := Key("test-profile", "x.y")
	if err := Store(k, "value"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := Fetch(k)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "value" {
		t.Errorf("Fetch = %q", got)
	}

	if err := Delete(k); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = Fetch(k)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, Fetch err = %v, want ErrNotFound", err)
	}

	// Deleting again is not an error.
	if err := Delete(k); err != nil {
		t.Errorf("second Delete should be no-op, got %v", err)
	}
}

func TestFileBackend(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CCSYNC_SECRETS_BACKEND", "file")

	k := Key("file-profile", "some.path")

	if err := Store(k, "value-A"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	// File should actually be on disk, mode 0600
	path := filepath.Join(tmp, ".ccsync", "secrets", sanitizeKey(k))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file perms = %v, want 0600", info.Mode().Perm())
	}

	got, err := Fetch(k)
	if err != nil || got != "value-A" {
		t.Errorf("Fetch = %q err=%v", got, err)
	}

	if err := Delete(k); err != nil {
		t.Fatal(err)
	}
	if _, err := Fetch(k); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, Fetch err = %v", err)
	}
}

// TestSanitizeKeyInjective pins the property that really matters:
// distinct input keys produce distinct filenames. Rather than assert
// specific encodings (which could change if we switch escape schemes),
// we assert injectivity across a small but deliberately-adversarial
// pool of keys that have historically collided under naive
// replacement.
func TestSanitizeKeyInjective(t *testing.T) {
	keys := []string{
		"a_b:x", // collided with "a:b_x" under old scheme
		"a:b_x",
		"profile:nested.path",
		"profile_nested.path", // no colon
		"profile:nested_path",
		"with/slash:yes",
		"with%25already-escaped",
	}
	seen := map[string]string{}
	for _, k := range keys {
		s := sanitizeKey(k)
		if orig, dup := seen[s]; dup {
			t.Errorf("collision: sanitizeKey(%q) == sanitizeKey(%q) == %q", orig, k, s)
		}
		seen[s] = k
	}
}

// TestFileBackendDistinctKeysNoCollision pins the iteration-12 fix for
// sanitizeKey: distinct logical keys must NOT collapse to the same
// on-disk filename. Pre-fix the sanitizer replaced "/", ":", "\" all
// with "_", so any user-chosen characters happening to be "_" in the
// profile name or path created false collisions. Worked example that
// failed: profile "a_b" + path "x" sanitized to "a_b_x"; profile "a"
// + path "b_x" ALSO sanitized to "a_b_x" — one file backed two
// logical secrets, so the second Store overwrote the first and any
// later Fetch returned the wrong profile's token.
//
// With secrets being OAuth tokens and API keys, cross-profile mixing
// is a real user-impact bug.
func TestFileBackendDistinctKeysNoCollision(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CCSYNC_SECRETS_BACKEND", "file")
	// Reset any state from earlier tests.
	SetBackend("")

	// Two logically-different keys that sanitize to the same name
	// under the old `_`-only scheme.
	keyA := Key("a_b", "x") // "a_b:x"
	keyB := Key("a", "b_x") // "a:b_x"

	if err := Store(keyA, "value-A"); err != nil {
		t.Fatalf("Store A: %v", err)
	}
	if err := Store(keyB, "value-B"); err != nil {
		t.Fatalf("Store B: %v", err)
	}

	gotA, err := Fetch(keyA)
	if err != nil {
		t.Fatalf("Fetch A: %v", err)
	}
	gotB, err := Fetch(keyB)
	if err != nil {
		t.Fatalf("Fetch B: %v", err)
	}
	if gotA != "value-A" {
		t.Errorf("Fetch(%q) = %q, want value-A — second Store silently clobbered the first", keyA, gotA)
	}
	if gotB != "value-B" {
		t.Errorf("Fetch(%q) = %q, want value-B", keyB, gotB)
	}
}
