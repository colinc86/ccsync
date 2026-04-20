// Package sync orchestrates the end-to-end ccsync flow: discover → compare →
// merge-safe → snapshot → write → commit → push. Conflicts are detected but
// never auto-resolved here; the TUI consumes Plan.Conflicts to drive per-file
// resolution.
package sync

import (
	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/merge"
)

// FileAction is the per-file outcome decided by the sync engine.
type FileAction struct {
	Path              string          // repo-relative (e.g. profiles/default/claude/foo)
	LocalAbs          string          // absolute path in user's ~/.claude or "" for .claude.json
	Action            manifest.Action // Decide() output (before profile filtering)
	ExcludedByProfile bool            // this path is filtered out by the active profile; treat as NoOp
}

// FileConflict bundles the per-file merge conflicts plus the raw bytes on
// either side — the TUI needs both to render pickers and to write resolved
// content back via ApplyResolutions.
type FileConflict struct {
	Path       string
	Conflicts  []merge.Conflict
	LocalData  []byte // repo-side filtered content as computed from local ~/.claude
	RemoteData []byte // bytes at the remote's HEAD for this path
	MergedData []byte // best-effort merge from the engine; defaults to local where it couldn't decide
	IsJSON     bool   // true = per-key resolution is supported
}

// Plan is the computed change set.
type Plan struct {
	Actions   []FileAction
	Conflicts []FileConflict
}

// Summary returns +<added> ~<modified> -<deleted> counts. Profile-excluded
// actions don't count toward any bucket.
func (p Plan) Summary() (added, modified, deleted int) {
	for _, a := range p.Actions {
		if a.ExcludedByProfile {
			continue
		}
		switch a.Action {
		case manifest.ActionAddLocal, manifest.ActionAddRemote:
			added++
		case manifest.ActionPull, manifest.ActionPush:
			modified++
		case manifest.ActionDeleteLocal, manifest.ActionDeleteRemote:
			deleted++
		}
	}
	return
}

// ExcludedPaths returns the repo paths that the active profile filtered out
// for this run. Useful for "why isn't this syncing?" diagnostics.
func (p Plan) ExcludedPaths() []string {
	var out []string
	for _, a := range p.Actions {
		if a.ExcludedByProfile {
			out = append(out, a.Path)
		}
	}
	return out
}

// Result is the output of Run.
type Result struct {
	Plan           Plan
	CommitSHA      string
	SnapshotID     string
	MissingSecrets []string // JSON paths whose keychain value was missing on pull
}

// Event is a progress update emitted during Run.
type Event struct {
	Stage   string
	Message string
	Path    string
}
