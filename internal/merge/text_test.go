package merge

import (
	"strings"
	"testing"
)

func TestTextNoChanges(t *testing.T) {
	r := Text("x", "x", "x")
	if !r.Clean() || string(r.Merged) != "x" {
		t.Errorf("Text(same) = %+v", r)
	}
}

func TestTextLocalOnly(t *testing.T) {
	r := Text("base\nline2\n", "base\nline2 edited\n", "base\nline2\n")
	if !r.Clean() || string(r.Merged) != "base\nline2 edited\n" {
		t.Errorf("Text(local only) = %q conflicts=%v", r.Merged, r.Conflicts)
	}
}

func TestTextRemoteOnly(t *testing.T) {
	r := Text("base\nline2\n", "base\nline2\n", "base\nline2 remote\n")
	if !r.Clean() || string(r.Merged) != "base\nline2 remote\n" {
		t.Errorf("Text(remote only) = %q conflicts=%v", r.Merged, r.Conflicts)
	}
}

func TestTextDisjointMerge(t *testing.T) {
	base := "line1\nline2\nline3\n"
	local := "line1 CHANGED\nline2\nline3\n"
	remote := "line1\nline2\nline3 CHANGED\n"
	r := Text(base, local, remote)
	if !r.Clean() {
		t.Fatalf("expected clean merge of disjoint edits, got %+v", r.Conflicts)
	}
	if string(r.Merged) != "line1 CHANGED\nline2\nline3 CHANGED\n" {
		t.Errorf("merged = %q", r.Merged)
	}
}

func TestTextOverlappingConflict(t *testing.T) {
	base := "line1\n"
	local := "line1 LOCAL\n"
	remote := "line1 REMOTE\n"
	r := Text(base, local, remote)
	if r.Clean() {
		t.Fatal("expected conflict for overlapping edits on same line")
	}
	if r.Conflicts[0].Kind != ConflictTextHunk {
		t.Errorf("kind = %s", r.Conflicts[0].Kind)
	}
	if string(r.Merged) != local {
		t.Errorf("on conflict, Merged should default to local; got %q", r.Merged)
	}
}

func TestTextSegmentsAgreedOnly(t *testing.T) {
	segs := TextSegments("same\ncontent\n", "same\ncontent\n")
	if len(segs) != 1 || segs[0].Hunk != nil {
		t.Fatalf("expected single agreed segment; got %+v", segs)
	}
	if !strings.Contains(segs[0].Agreed, "same") {
		t.Errorf("agreed missing expected text: %q", segs[0].Agreed)
	}
}

func TestTextSegmentsHunkBetweenAgreed(t *testing.T) {
	local := "header\nline A\nfooter\n"
	remote := "header\nline B\nfooter\n"
	segs := TextSegments(local, remote)
	if len(segs) < 3 {
		t.Fatalf("expected >=3 segments; got %d: %+v", len(segs), segs)
	}
	foundHunk := false
	for _, s := range segs {
		if s.Hunk == nil {
			continue
		}
		foundHunk = true
		if !strings.Contains(s.Hunk.Local, "line A") {
			t.Errorf("hunk local missing: %q", s.Hunk.Local)
		}
		if !strings.Contains(s.Hunk.Remote, "line B") {
			t.Errorf("hunk remote missing: %q", s.Hunk.Remote)
		}
	}
	if !foundHunk {
		t.Fatal("no conflict hunk in segments")
	}
}

func TestTextSegmentsPureAdd(t *testing.T) {
	segs := TextSegments("header\n", "header\nextra\n")
	var hunk *TextHunk
	for _, s := range segs {
		if s.Hunk != nil {
			hunk = s.Hunk
		}
	}
	if hunk == nil {
		t.Fatal("expected a hunk for remote-only addition")
	}
	if hunk.Local != "" {
		t.Errorf("expected empty local; got %q", hunk.Local)
	}
	if !strings.Contains(hunk.Remote, "extra") {
		t.Errorf("remote missing added line: %q", hunk.Remote)
	}
}

// TestTextEmptyBaseDivergent pins the empty-base divergent-content
// case: when we have no shared ancestor and both sides have different
// content, it MUST surface as a conflict. Without this test, a
// refactor that changed the dmp fuzzy-apply path could silently
// produce a surprising "merged" result (concat or interleave) that
// gets written to the user's disk. For an empty base, the right
// answer is "user decides" — never silent auto-resolution.
func TestTextEmptyBaseDivergent(t *testing.T) {
	r := Text("", "local content\n", "remote content\n")
	if r.Clean() {
		t.Fatalf("empty base + divergent content must NOT auto-resolve; got merged=%q", r.Merged)
	}
	if string(r.Merged) != "local content\n" {
		t.Errorf("on conflict, Merged defaults to local; got %q", r.Merged)
	}
}

// TestTextSameContentBothSidesShortcuts the identical-content
// shortcut at the top of Text — when local == remote, the patch
// dance is skipped entirely and we return local as-is. Keeps the
// common "both sides landed at the same point" case fast and
// regression-proof.
func TestTextSameContentBothSidesShortcuts(t *testing.T) {
	r := Text("old\n", "new\n", "new\n")
	if !r.Clean() {
		t.Fatalf("local==remote must be clean, got %+v", r.Conflicts)
	}
	if string(r.Merged) != "new\n" {
		t.Errorf("Merged = %q, want 'new\\n'", r.Merged)
	}
}
