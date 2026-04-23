package suggest

import (
	"testing"

	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/sync"
)

func TestAnalyzeFindsNoisyExtensions(t *testing.T) {
	plan := &sync.Plan{
		Actions: []sync.FileAction{
			{Path: "profiles/default/claude/agents/foo.md", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/cache.tmp", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/session.log", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/other.md.swp", Action: manifest.ActionAddRemote},
		},
	}
	got := Analyze(plan, nil)
	if len(got) != 3 {
		t.Fatalf("want 3 suggestions; got %d: %+v", len(got), got)
	}
	// All three proposals should be distinct patterns.
	seen := map[string]bool{}
	for _, s := range got {
		if seen[s.Pattern] {
			t.Errorf("duplicate pattern: %s", s.Pattern)
		}
		seen[s.Pattern] = true
	}
}

func TestAnalyzeRespectsDismissals(t *testing.T) {
	plan := &sync.Plan{
		Actions: []sync.FileAction{
			{Path: "profiles/default/claude/cache.tmp", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/session.log", Action: manifest.ActionAddRemote},
		},
	}
	got := Analyze(plan, []string{"*.tmp"})
	if len(got) != 1 || got[0].Pattern != "*.log" {
		t.Fatalf("expected only *.log suggestion; got %+v", got)
	}
}

func TestAnalyzeIgnoresProfileExcluded(t *testing.T) {
	plan := &sync.Plan{
		Actions: []sync.FileAction{
			{Path: "profiles/default/claude/cache.tmp", Action: manifest.ActionAddRemote, ExcludedByProfile: true},
		},
	}
	if got := Analyze(plan, nil); len(got) != 0 {
		t.Fatalf("expected 0 suggestions for excluded path; got %+v", got)
	}
}

// TestAnalyzeSamplePathsAreSorted pins the iteration-19 fix: the
// sample paths shown per suggestion must be deterministic across
// calls. Pre-fix, plan.Actions' map-range order bled through into
// the suggestion's Paths[], so refresh-heavy users saw the "example
// files" list cycle through different names on every render. Sorting
// before truncating pins the first-5 set to the lexically-earliest
// matches.
func TestAnalyzeSamplePathsAreSorted(t *testing.T) {
	// Deliberately provide .tmp paths in non-sorted order. After
	// Analyze, the Paths slice must be sorted ascending.
	plan := &sync.Plan{
		Actions: []sync.FileAction{
			{Path: "profiles/default/claude/z.tmp", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/a.tmp", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/m.tmp", Action: manifest.ActionAddRemote},
		},
	}
	got := Analyze(plan, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(got))
	}
	paths := got[0].Paths
	if len(paths) != 3 {
		t.Fatalf("expected 3 sample paths, got %d: %v", len(paths), paths)
	}
	want := []string{
		"profiles/default/claude/a.tmp",
		"profiles/default/claude/m.tmp",
		"profiles/default/claude/z.tmp",
	}
	for i, p := range paths {
		if p != want[i] {
			t.Errorf("paths[%d] = %q, want %q — suggestion samples must be deterministic across runs", i, p, want[i])
		}
	}

	// Also verify the cap-at-5 logic still takes the lexically-earliest
	// five. With six inputs, we drop the last two alphabetically.
	plan = &sync.Plan{
		Actions: []sync.FileAction{
			{Path: "profiles/default/claude/f.log", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/a.log", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/e.log", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/b.log", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/d.log", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/c.log", Action: manifest.ActionAddRemote},
		},
	}
	got = Analyze(plan, nil)
	if len(got[0].Paths) != 5 {
		t.Fatalf("expected 5 sample paths (cap), got %d", len(got[0].Paths))
	}
	if got[0].Paths[0] != "profiles/default/claude/a.log" || got[0].Paths[4] != "profiles/default/claude/e.log" {
		t.Errorf("cap should take lex-earliest 5; got %v", got[0].Paths)
	}
}
