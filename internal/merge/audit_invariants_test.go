package merge

import (
	"strings"
	"testing"
	"time"
)

// TestCRLFDisjointCleanMerge pins iter-43 audit finding: a text file
// with consistent CRLF line endings merges cleanly when local and
// remote edit different lines. Concern was that CRLF might trip
// line-level diff into treating every line as changed; in practice
// go-diff-match-patch handles CRLF as part of the line content and
// disjoint edits round-trip through the three-way merge without
// spurious conflicts. Keeping this as a pinned invariant means a
// future refactor that adds pre-processing (e.g. line-normalization)
// has to preserve the behavior.
func TestCRLFDisjointCleanMerge(t *testing.T) {
	base := "one\r\ntwo\r\nthree\r\n"
	local := "one CHANGED\r\ntwo\r\nthree\r\n"
	remote := "one\r\ntwo\r\nthree CHANGED\r\n"
	r := Text(base, local, remote)
	if !r.Clean() {
		t.Fatalf("expected clean merge of disjoint CRLF edits, got %d conflicts", len(r.Conflicts))
	}
	if !strings.Contains(string(r.Merged), "one CHANGED") || !strings.Contains(string(r.Merged), "three CHANGED") {
		t.Errorf("merged lost one of the edits: %q", r.Merged)
	}
}

// TestJSONDotInKeyNotCollidingWithNested pins iter-43 audit finding: a
// flat key containing a dot (`{"a.b": 1}`) does NOT collide with a
// nested path (`{"a":{"b":1}}`) in the three-way merge. Concern was
// that joinPath's dot-separator might conflate these; in practice the
// merge iterates top-level keys distinctly so `"a.b"` and `"a"` are
// treated as separate keys. Invariant: a user whose settings.json has
// keys with literal dots won't see spurious conflicts merge them with
// nested siblings.
func TestJSONDotInKeyNotCollidingWithNested(t *testing.T) {
	base := []byte(`{"other":0}`)
	local := []byte(`{"other":0,"a.b":"flat"}`)
	remote := []byte(`{"other":0,"a":{"b":"nested"}}`)
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Fatalf("dot-key + nested-path should merge cleanly: %d conflicts", len(r.Conflicts))
	}
	merged := string(r.Merged)
	if !strings.Contains(merged, `"a.b"`) {
		t.Errorf("flat dot-key lost in merge: %s", merged)
	}
	if !strings.Contains(merged, `"b": "nested"`) {
		t.Errorf("nested path lost in merge: %s", merged)
	}
}

// TestBinaryEqualMtimeRemoteWins pins iter-43 audit finding: when
// local and remote mtimes are exactly equal, merge.Binary returns
// remote. This is a consequence of using `localMTime.After(remoteMTime)`
// — strict inequality — for the tie-break. The iter-42 test coverage
// only exercised distinct times; this pin documents the equal-time
// choice so a refactor can't silently flip it to local-wins.
func TestBinaryEqualMtimeRemoteWins(t *testing.T) {
	now := time.Now()
	r := Binary([]byte("L"), now, []byte("R"), now)
	if string(r.Merged) != "R" {
		t.Errorf("equal mtime should pick remote; got %q", r.Merged)
	}
}
