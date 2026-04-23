package manifest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMtimeNanosecondRoundTrip pins iter-43 audit: Entry.MTime round-
// trips via json.Marshal at full nanosecond precision (RFC3339Nano).
// Iter-42's binary LWW fix depends on this: if nanoseconds were
// silently truncated on save, ties would become much more common and
// LWW would devolve back toward always-remote-wins in practice.
func TestMtimeNanosecondRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	orig := time.Date(2026, 4, 22, 10, 30, 45, 123456789, time.UTC)
	m := New("host-1")
	m.Set("claude/agents/precision.md", Entry{
		SHA256: "deadbeef", Size: 1, MTime: orig, LastModifiedBy: "host-1",
	})
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, "host-2")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := loaded.Get("claude/agents/precision.md")
	if !ok {
		t.Fatal("entry missing after round-trip")
	}
	if got.MTime.Nanosecond() != orig.Nanosecond() {
		t.Errorf("nanoseconds lost: orig=%d, loaded=%d", orig.Nanosecond(), got.MTime.Nanosecond())
	}
	if !got.MTime.Equal(orig) {
		t.Errorf("mtime not equal after round-trip: orig=%v loaded=%v", orig, got.MTime)
	}
}

// TestLoadMalformedFilesNullSelfHeals pins the existing Load behavior:
// a manifest with explicit `{"files": null}` on disk is silently
// repaired to an empty Files map. Documented here so a future refactor
// that might prefer to error out has to make that choice deliberately.
func TestLoadMalformedFilesNullSelfHeals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path,
		[]byte(`{"version":1,"updatedAt":"2026-04-22T00:00:00Z","updatedBy":"h","files":null}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path, "host-1")
	if err != nil {
		t.Fatalf("Load should self-heal files=null; got %v", err)
	}
	if m.Files == nil {
		t.Fatal("Files was left nil after Load — Set/Delete would panic")
	}
	// Verify Set works (i.e. self-heal is real, not a nil assignment that survives).
	m.Set("x", Entry{SHA256: "a", Size: 1})
	if _, ok := m.Get("x"); !ok {
		t.Error("Set on self-healed manifest didn't stick")
	}
}
