package merge

import "testing"

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
