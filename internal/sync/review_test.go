package sync

import (
	"testing"

	"github.com/colinc86/ccsync/internal/manifest"
	ccstate "github.com/colinc86/ccsync/internal/state"
)

// TestPartitionPlan_ApproveModeGatesNewFiles pins the approve-mode
// overlay: any add-new action (outbound ActionAddRemote or inbound
// ActionAddLocal) routes into the Review bucket regardless of the
// category's per-direction policy, while modifies and deletes keep
// flowing through the usual auto/review/never lanes. This is the
// "auto for everything except new files" UX the approve mode
// feature promises — without it the feature would behave
// indistinguishably from plain auto mode.
func TestPartitionPlan_ApproveModeGatesNewFiles(t *testing.T) {
	st := &ccstate.State{
		SyncMode:      "approve",
		LastSyncedSHA: map[string]string{},
	}
	plan := Plan{Actions: []FileAction{
		{Path: "profiles/x/claude/skills/new-local/SKILL.md", Action: manifest.ActionAddRemote, Category: "skills"},
		{Path: "profiles/x/claude/skills/new-remote/SKILL.md", Action: manifest.ActionAddLocal, Category: "skills"},
		{Path: "profiles/x/claude/skills/edited/SKILL.md", Action: manifest.ActionPush, Category: "skills"},
		{Path: "profiles/x/claude/skills/pulled/SKILL.md", Action: manifest.ActionPull, Category: "skills"},
		{Path: "profiles/x/claude/skills/gone/SKILL.md", Action: manifest.ActionDeleteRemote, Category: "skills"},
	}}
	part := PartitionPlan(plan, st)

	// Both adds land in Review.
	reviewPaths := make(map[string]bool)
	for _, a := range part.Review {
		reviewPaths[a.Path] = true
	}
	for _, want := range []string{
		"profiles/x/claude/skills/new-local/SKILL.md",
		"profiles/x/claude/skills/new-remote/SKILL.md",
	} {
		if !reviewPaths[want] {
			t.Errorf("approve mode: expected %q in Review bucket; got review=%v", want, reviewKeys(part.Review))
		}
	}

	// Modify/delete rows do NOT land in Review in approve mode —
	// that's the whole point, they flow auto like in auto mode.
	autoPaths := make(map[string]bool)
	for _, a := range part.Auto {
		autoPaths[a.Path] = true
	}
	for _, want := range []string{
		"profiles/x/claude/skills/edited/SKILL.md",
		"profiles/x/claude/skills/pulled/SKILL.md",
		"profiles/x/claude/skills/gone/SKILL.md",
	} {
		if !autoPaths[want] {
			t.Errorf("approve mode: expected %q in Auto bucket; got auto=%v review=%v", want, reviewKeys(part.Auto), reviewKeys(part.Review))
		}
	}
}

// TestPartitionPlan_AutoModeIgnoresApproveOverlay pins that the
// approve-mode behaviour is mode-gated, not an always-on overlay:
// plain auto mode must NOT pull adds into Review (that'd reproduce
// approve's prompting flow under the wrong mode and annoy users
// who picked auto precisely to avoid prompts).
func TestPartitionPlan_AutoModeIgnoresApproveOverlay(t *testing.T) {
	st := &ccstate.State{SyncMode: "auto", LastSyncedSHA: map[string]string{}}
	plan := Plan{Actions: []FileAction{
		{Path: "profiles/x/claude/skills/new/SKILL.md", Action: manifest.ActionAddRemote, Category: "skills"},
	}}
	part := PartitionPlan(plan, st)
	if len(part.Review) != 0 {
		t.Errorf("auto mode: expected 0 review items, got %d (%v)", len(part.Review), reviewKeys(part.Review))
	}
	if len(part.Auto) != 1 {
		t.Errorf("auto mode: expected add to land in Auto; got auto=%d", len(part.Auto))
	}
}

func reviewKeys(actions []FileAction) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, a.Path)
	}
	return out
}
