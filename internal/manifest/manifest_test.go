package manifest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	m, err := Load(filepath.Join(dir, "missing.json"), "host-abc")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != FormatVersion {
		t.Errorf("want version %d, got %d", FormatVersion, m.Version)
	}
	if m.UpdatedBy != "host-abc" {
		t.Errorf("UpdatedBy = %q", m.UpdatedBy)
	}
	if len(m.Files) != 0 {
		t.Errorf("expected empty, got %d files", len(m.Files))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	m := New("host-1")
	m.Set("claude/agents/foo.md", Entry{
		SHA256:         "abc",
		Size:           7,
		MTime:          time.Now().UTC().Truncate(time.Second),
		LastModifiedBy: "host-1",
	})
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path, "host-2")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := loaded.Get("claude/agents/foo.md")
	if !ok || got.SHA256 != "abc" || got.Size != 7 {
		t.Errorf("entry didn't round-trip: %+v", got)
	}
	if loaded.UpdatedBy != "host-1" {
		t.Errorf("UpdatedBy survived from save host, got %q", loaded.UpdatedBy)
	}
}

func TestSHA256File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SHA256File(p)
	if err != nil {
		t.Fatal(err)
	}
	// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("SHA256File = %s, want %s", got, want)
	}
}

func TestSHA256Bytes(t *testing.T) {
	got := SHA256Bytes([]byte("hello"))
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("SHA256Bytes = %s, want %s", got, want)
	}
}

func TestSortedPaths(t *testing.T) {
	m := New("h")
	for _, p := range []string{"c", "a", "b"} {
		m.Set(p, Entry{})
	}
	got := m.SortedPaths()
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("SortedPaths = %v", got)
	}
}
