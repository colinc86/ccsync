package sync

import (
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/manifest"
)

// TestPlanSummaryExcludesDeniedPaths pins the iteration-37 fix: per-
// machine denied paths must NOT count toward +added / ~modified /
// -deleted in the plan summary. Pre-fix, only ExcludedByProfile was
// skipped — a file on this machine's denylist would still show up as
// "1 modified" in sync previews and the dashboard "↓ 1 pull" badge,
// even though the sync engine correctly did nothing with it. User sees
// "pending change" but sync appears to do nothing — confusing.
func TestPlanSummaryExcludesDeniedPaths(t *testing.T) {
	p := Plan{
		Actions: []FileAction{
			{Path: "profiles/default/claude/agents/a.md", Action: manifest.ActionPull},
			{Path: "profiles/default/claude/agents/b.md", Action: manifest.ActionPull, ExcludedByProfile: true},
			{Path: "profiles/default/claude/agents/c.md", Action: manifest.ActionPull, ExcludedByDeny: true},
			{Path: "profiles/default/claude/agents/d.md", Action: manifest.ActionAddLocal},
			{Path: "profiles/default/claude/agents/e.md", Action: manifest.ActionAddLocal, ExcludedByDeny: true},
			{Path: "profiles/default/claude/agents/f.md", Action: manifest.ActionDeleteRemote, ExcludedByDeny: true},
		},
	}
	added, modified, deleted := p.Summary()
	if added != 1 {
		t.Errorf("added = %d, want 1 (d.md only — e.md is denied)", added)
	}
	if modified != 1 {
		t.Errorf("modified = %d, want 1 (a.md only — b.md profile-excluded, c.md denied)", modified)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 (f.md is denied)", deleted)
	}
}

// TestCommitMessageOmitsDeniedPaths pins iter-38: the commit message
// builder skips ExcludedByProfile but must ALSO skip ExcludedByDeny.
// sync.Run doesn't write denied paths (run.go:318 `if excluded ||
// denied { continue }`), so listing them in the commit message
// produces a lie: "Changed: - agent foo" when the sync never touched
// profiles/<active>/claude/agents/foo on disk or in the tree.
func TestCommitMessageOmitsDeniedPaths(t *testing.T) {
	plan := Plan{
		Actions: []FileAction{
			{Path: "profiles/default/claude/agents/real.md", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/claude/agents/denied.md", Action: manifest.ActionAddRemote, ExcludedByDeny: true},
			{Path: "profiles/default/claude/agents/prof-excl.md", Action: manifest.ActionAddRemote, ExcludedByProfile: true},
		},
	}
	msg := commitMessage("default", "testhost", plan, nil, map[string][]byte{
		"profiles/default/claude/agents/real.md": []byte("ok"),
	})

	if !strings.Contains(msg, "real") {
		t.Errorf("commit message should list the real change; got:\n%s", msg)
	}
	if strings.Contains(msg, "denied") {
		t.Errorf("commit message listed a denied path (sync didn't touch it); got:\n%s", msg)
	}
	if strings.Contains(msg, "prof-excl") {
		t.Errorf("commit message listed a profile-excluded path; got:\n%s", msg)
	}
	// Summary header should also be +1 (only real.md), not +2 or +3.
	if !strings.Contains(msg, "+1 ~0 -0") {
		t.Errorf("commit message summary should be +1 ~0 -0; got:\n%s", msg)
	}
}
