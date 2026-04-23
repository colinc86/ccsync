package tui

import (
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/sync"
)

// TestNaturalLanguageSummaryShapes pins the exact strings the user
// sees in the sync-preview header so copy drift is caught by CI.
// If a refactor accidentally reverts to stacked-sentence or
// zero-noise phrasings, these tests break with a clear diff.
func TestNaturalLanguageSummaryShapes(t *testing.T) {
	pushN := func(n int) []sync.FileAction { return make([]sync.FileAction, n) }
	pullN := func(n int) []sync.FileAction { return make([]sync.FileAction, n) }
	confN := func(n int) []sync.FileConflict { return make([]sync.FileConflict, n) }

	cases := []struct {
		name           string
		push, pull     []sync.FileAction
		conflicts      []sync.FileConflict
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:           "empty plan returns empty string",
			wantNotContain: []string{"sync"},
		},
		{
			name:         "push only, plural",
			push:         pushN(3),
			wantContains: []string{"push 3 local files up to the repo"},
		},
		{
			name:         "push only, singular",
			push:         pushN(1),
			wantContains: []string{"push 1 local file up to the repo"},
		},
		{
			name:         "pull only",
			pull:         pullN(5),
			wantContains: []string{"pull 5 files down into ~/.claude"},
		},
		{
			name:         "push + pull",
			push:         pushN(2),
			pull:         pullN(4),
			wantContains: []string{"push 2 local files", "and pull 4 files"},
		},
		{
			name:           "single conflict only — no 'this sync will' preamble",
			conflicts:      confN(1),
			wantContains:   []string{"1 conflict needs manual resolution."},
			wantNotContain: []string{"This sync will"},
		},
		{
			name:         "multiple conflicts only, singular/plural agreement",
			conflicts:    confN(3),
			wantContains: []string{"3 conflicts need manual resolution."},
		},
		{
			name:         "push + conflicts — conflict sentence follows the main one",
			push:         pushN(2),
			conflicts:    confN(1),
			wantContains: []string{"push 2 local files", "1 conflict needs manual resolution."},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := naturalLanguageSummary(c.push, c.pull, c.conflicts)
			if c.name == "empty plan returns empty string" && got != "" {
				t.Fatalf("want empty, got %q", got)
			}
			for _, s := range c.wantContains {
				if !strings.Contains(got, s) {
					t.Errorf("want contains %q, got: %q", s, got)
				}
			}
			for _, s := range c.wantNotContain {
				if strings.Contains(got, s) {
					t.Errorf("should NOT contain %q, got: %q", s, got)
				}
			}
		})
	}
}
