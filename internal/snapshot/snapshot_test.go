package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTakeListRestore(t *testing.T) {
	root := t.TempDir()
	claudeDir := t.TempDir()

	f1 := filepath.Join(claudeDir, "a.md")
	f2 := filepath.Join(claudeDir, "nested", "b.md")
	if err := os.MkdirAll(filepath.Dir(f2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f1, []byte("original-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("original-b"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Take(root, "sync", "default", []string{f1, f2})
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(m.Files) != 2 {
		t.Fatalf("expected 2 captured, got %d", len(m.Files))
	}

	snaps, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}

	if err := os.WriteFile(f1, []byte("corrupted-a"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Restore(root, m.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, _ := os.ReadFile(f1)
	if string(got) != "original-a" {
		t.Errorf("after restore, f1 = %q", got)
	}
}

func TestTakeSkipsMissing(t *testing.T) {
	root := t.TempDir()
	m, err := Take(root, "op", "", []string{"/nonexistent/path"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 0 {
		t.Errorf("expected 0 captured for missing path, got %d", len(m.Files))
	}
}

func TestPrune(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// take 3 snapshots; sleep briefly between to get distinct timestamps
	var ids []string
	for i := 0; i < 3; i++ {
		m, err := Take(root, "t", "", []string{f})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
		time.Sleep(time.Second + 10*time.Millisecond)
	}

	// keep only the newest; others older than 0 second window get pruned
	if err := Prune(root, 1, 0); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	snaps, _ := List(root)
	if len(snaps) != 1 {
		t.Errorf("expected 1 after prune, got %d", len(snaps))
	}
}

// TestTakeRapidSuccessionDistinct pins the iteration-10 fix: two
// Take calls within the same wall-clock second must produce distinct
// snapshot IDs. Pre-fix the ID format was
// "20060102T150405Z-<op>" — one-second granularity. Two calls within
// one second collided, the second MkdirAll silently accepted the
// existing dir, and the second Take's writes overwrote the first's
// meta.json + file contents. That's silent data loss in the exact
// path the user relies on for rollback safety.
func TestTakeRapidSuccessionDistinct(t *testing.T) {
	root := t.TempDir()
	f1 := filepath.Join(t.TempDir(), "first")
	f2 := filepath.Join(t.TempDir(), "second")
	if err := os.WriteFile(f1, []byte("first-body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("second-body"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := Take(root, "sync", "default", []string{f1})
	if err != nil {
		t.Fatalf("Take 1: %v", err)
	}
	b, err := Take(root, "sync", "default", []string{f2})
	if err != nil {
		t.Fatalf("Take 2: %v", err)
	}
	if a.ID == b.ID {
		t.Fatalf("two rapid Takes produced the same ID (%q) — second would overwrite first", a.ID)
	}

	snaps, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Errorf("expected 2 distinct snapshots on disk, got %d", len(snaps))
	}

	// Restoring the first snapshot must still bring back "first-body".
	// If the second Take silently overwrote the first's storage, the
	// content would be gone.
	if err := os.Remove(f1); err != nil {
		t.Fatal(err)
	}
	if err := Restore(root, a.ID); err != nil {
		t.Fatalf("Restore of first snapshot: %v", err)
	}
	got, _ := os.ReadFile(f1)
	if string(got) != "first-body" {
		t.Errorf("first snapshot was clobbered; restore yielded %q", got)
	}
}

// TestRestoreAllOrNothing pins the iteration-10 fix: if the snapshot
// is corrupt (one or more source files missing inside the snapshot
// dir), Restore must fail BEFORE writing anything to the real local
// paths. Pre-fix, Restore read-then-wrote in the same loop; a missing
// snapshot source at file 3-of-5 left files 1-2 restored and 3-5
// untouched — a half-rolled-back state the user has to untangle by
// hand.
func TestRestoreAllOrNothing(t *testing.T) {
	root := t.TempDir()
	homeDir := t.TempDir()
	a := filepath.Join(homeDir, "a.md")
	b := filepath.Join(homeDir, "b.md")
	c := filepath.Join(homeDir, "c.md")
	for _, p := range []string{a, b, c} {
		if err := os.WriteFile(p, []byte("orig-"+filepath.Base(p)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m, err := Take(root, "sync", "default", []string{a, b, c})
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the snapshot by removing b's stored copy inside the
	// snapshot dir. meta.json still references it.
	snapInternalB := filepath.Join(root, m.ID, mirrorPath(b))
	if err := os.Remove(snapInternalB); err != nil {
		t.Fatalf("simulating corruption: %v", err)
	}

	// Modify the live files so we can see whether Restore clobbered
	// them partially.
	for _, p := range []string{a, b, c} {
		if err := os.WriteFile(p, []byte("mutated-"+filepath.Base(p)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Restore(root, m.ID); err == nil {
		t.Fatal("expected Restore to fail on corrupt snapshot; got nil")
	}

	// Critical: NONE of the live files should have been touched.
	// Pre-fix, a and c (whichever came before b in iteration order)
	// would already be overwritten to "orig-*".
	for _, p := range []string{a, b, c} {
		got, _ := os.ReadFile(p)
		want := "mutated-" + filepath.Base(p)
		if string(got) != want {
			t.Errorf("%s was partially restored; got %q, want %q — user's local state is now inconsistent", p, got, want)
		}
	}
}
