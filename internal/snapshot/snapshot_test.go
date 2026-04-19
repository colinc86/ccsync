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
