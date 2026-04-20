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
