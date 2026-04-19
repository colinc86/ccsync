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
