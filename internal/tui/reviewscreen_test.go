package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
)

// TestReviewScreenCursorMatchesVisualOrder pins the iteration-18 fix:
// when actions span multiple categories, the visual render groups
// them by category (agents first, then commands, etc.) but the flat
// m.items slice preserves insertion order. Pre-fix, cursor=1 showed
// the user "▸" next to the 2nd-visible row, but space toggled
// m.items[1] — which corresponded to a DIFFERENT row in the display.
// Users ended up denying the wrong file silently.
//
// Scenario: actions = [agent_A, command_C, agent_B]. Grouped render
// puts agents first, so display order is: agent_A, agent_B,
// command_C. Cursor=1 means "second visible row" → agent_B. Hit
// space → agent_B.Allowed flips, NOT command_C.
func TestReviewScreenCursorMatchesVisualOrder(t *testing.T) {
	ctx := &AppContext{
		State:    &state.State{LastSyncedSHA: map[string]string{}},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		StateDir: t.TempDir(),
		RepoPath: t.TempDir(),
	}

	// Deliberately out-of-canonical-order: command sandwiched between
	// two agents. If the flat/visual order diverges, this catches it.
	actions := []sync.FileAction{
		{
			Path:     "profiles/default/claude/agents/alpha.md",
			Action:   manifest.ActionPush,
			Category: category.Agents,
		},
		{
			Path:     "profiles/default/claude/commands/deploy.md",
			Action:   manifest.ActionPush,
			Category: category.Commands,
		},
		{
			Path:     "profiles/default/claude/agents/beta.md",
			Action:   manifest.ActionPush,
			Category: category.Agents,
		},
	}
	m := newReviewScreen(ctx, actions, "profiles/default/")

	// Sanity: all 3 items allowed by default.
	for i, it := range m.items {
		if !it.Allowed {
			t.Fatalf("item %d default should be allowed", i)
		}
	}

	// Cursor at 0 → visual row 1 → agent alpha. Hit space to toggle.
	// That should flip alpha.
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = newModel.(*reviewScreenModel)
	alpha := findItem(m, "profiles/default/claude/agents/alpha.md")
	if alpha == nil || alpha.Allowed {
		t.Fatalf("cursor=0 → space should have toggled agents/alpha; alpha state: %+v", alpha)
	}

	// Cursor down → position 1 → SECOND VISIBLE row → agents/beta
	// (grouped render puts agents before commands). Hit space.
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = newModel.(*reviewScreenModel)
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = newModel.(*reviewScreenModel)

	beta := findItem(m, "profiles/default/claude/agents/beta.md")
	if beta == nil {
		t.Fatal("beta missing from items")
	}
	if beta.Allowed {
		t.Errorf("cursor at 2nd-visible (agents/beta) → space should toggle beta; got Allowed=true — user is toggling the wrong file (the command row), silent data-handling bug")
	}

	// Third visible row → commands/deploy. Cursor should NOT have
	// been toggled yet (we only hit it at positions 0 and 1). Verify
	// deploy is still allowed.
	deploy := findItem(m, "profiles/default/claude/commands/deploy.md")
	if deploy == nil {
		t.Fatal("deploy missing from items")
	}
	if !deploy.Allowed {
		t.Errorf("deploy should still be allowed — pre-fix, toggling at cursor=1 would have flipped deploy instead of beta")
	}
}

func findItem(m *reviewScreenModel, path string) *reviewItem {
	for i := range m.items {
		if m.items[i].Path == path {
			return &m.items[i]
		}
	}
	return nil
}
