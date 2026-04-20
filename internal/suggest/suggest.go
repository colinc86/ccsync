// Package suggest derives rule-change proposals from the cached sync plan.
// Pure function — no I/O. The TUI decides how to present them and whether
// to apply; dismissals are persisted in state.DismissedSuggestions.
package suggest

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/sync"
)

// Kind discriminates what the suggestion is proposing.
type Kind int

const (
	// KindSyncignore proposes appending a pattern to the repo's .syncignore.
	KindSyncignore Kind = iota
)

// Suggestion is one proposal for a rule change.
type Suggestion struct {
	Kind    Kind
	Pattern string   // the rule text
	Reason  string   // short human explanation
	Paths   []string // concrete paths that triggered it (sample, truncated)
}

// NoisyExtensions is the baseline list of file extensions ccsync suggests
// filtering out of sync whenever it sees them in a plan. Deliberately
// narrow — better to miss a suggestion than to nag.
var NoisyExtensions = map[string]bool{
	".tmp": true,
	".log": true,
	".swp": true,
	".pid": true,
	".bak": true,
	".lock": true,
}

// Analyze returns zero or more suggestions derived from the plan. Anything
// listed in dismissed is filtered out so the user doesn't see it again.
func Analyze(plan *sync.Plan, dismissed []string) []Suggestion {
	if plan == nil {
		return nil
	}
	dismissedSet := map[string]bool{}
	for _, d := range dismissed {
		dismissedSet[d] = true
	}

	// Group active (not excluded, not no-op) paths by extension.
	byExt := map[string][]string{}
	for _, a := range plan.Actions {
		if a.ExcludedByProfile {
			continue
		}
		if a.Action == manifest.ActionNoOp {
			continue
		}
		ext := strings.ToLower(filepath.Ext(a.Path))
		if !NoisyExtensions[ext] {
			continue
		}
		byExt[ext] = append(byExt[ext], a.Path)
	}

	// Stable order for deterministic output.
	exts := make([]string, 0, len(byExt))
	for k := range byExt {
		exts = append(exts, k)
	}
	sort.Strings(exts)

	var out []Suggestion
	for _, ext := range exts {
		pat := "*" + ext
		if dismissedSet[pat] {
			continue
		}
		paths := byExt[ext]
		// Cap at 5 sample paths for display.
		if len(paths) > 5 {
			paths = paths[:5]
		}
		out = append(out, Suggestion{
			Kind:    KindSyncignore,
			Pattern: pat,
			Reason:  "noisy " + ext + " file(s) appear in sync — usually machine-local",
			Paths:   paths,
		})
	}
	return out
}
