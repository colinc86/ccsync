package snapshot

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestIDContainsPID pins iter-43 audit fix: the snapshot ID now
// embeds os.Getpid() as a second disambiguator after wall-clock
// nanoseconds. Pre-fix, the package comment promised PID but only
// appended the time format — on OSes where time.Now() resolution is
// coarser than one nanosecond or on virtualised clocks, two rapid
// Take calls could produce identical IDs and the second would
// silently clobber the first's snapshot dir. Test: take a snapshot
// and check the ID segment between two dashes is the current PID.
func TestIDContainsPID(t *testing.T) {
	tmp := t.TempDir()
	absPaths := []string{}
	meta, err := Take(filepath.Join(tmp, "snaps"), "test-op", "default", absPaths)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(meta.ID, "-")
	if len(parts) < 3 {
		t.Fatalf("ID must contain at least three dash-separated segments (timestamp-pid-op); got %q", meta.ID)
	}
	pidSeg := parts[1]
	if _, err := strconv.Atoi(pidSeg); err != nil {
		t.Errorf("second ID segment should be numeric PID; got %q in %q", pidSeg, meta.ID)
	}
}

// TestListTiebreakerOnEqualCreatedAt pins iter-43 audit fix: when two
// snapshots share a CreatedAt (possible on coarse clocks or mocked
// time), List's sort is now stable with ID as the tiebreaker. Prune,
// which acts on List's output, needs deterministic ordering to avoid
// evicting the wrong sibling among equal-aged snapshots.
func TestListTiebreakerOnEqualCreatedAt(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "snaps")
	sameTime := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)

	// Hand-write three snapshot dirs with identical CreatedAt.
	for _, id := range []string{"20260101T000000.000000000Z-1-a", "20260101T000000.000000000Z-2-b", "20260101T000000.000000000Z-3-c"} {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeMeta(dir, Meta{ID: id, Op: "test", CreatedAt: sameTime}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(out))
	}
	// With ID-desc tiebreaker, expect the "3-c" id first.
	for i := 0; i < len(out)-1; i++ {
		if out[i].ID < out[i+1].ID {
			t.Errorf("unstable sort order: %q before %q — Prune could evict wrong sibling",
				out[i].ID, out[i+1].ID)
		}
	}
}
