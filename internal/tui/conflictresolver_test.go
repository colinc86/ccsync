package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
)

// testConflictCtx builds the minimal AppContext the resolver needs.
// Matches the pattern used by profilepickerscreen_test.go.
func testConflictCtx(t *testing.T) *AppContext {
	t.Helper()
	return &AppContext{
		State: &state.State{
			LastSyncedSHA: map[string]string{},
		},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		StateDir: t.TempDir(),
		RepoPath: t.TempDir(),
	}
}

// TestConflictResolverEnterDrillsInByType pins the iteration-16 fix:
// pre-fix, `enter` on a text conflict silently did nothing because
// the key picker was JSON-only. Users then had to discover `h` from
// the footer hint. Now `enter` is the universal "drill in" key —
// JSON → per-key picker, text → per-hunk picker. A refactor that
// reverts this would fail the text case.
func TestConflictResolverEnterDrillsInByType(t *testing.T) {
	ctx := testConflictCtx(t)
	conflicts := []sync.FileConflict{
		{Path: "profiles/default/claude.json", IsJSON: true},
		{Path: "profiles/default/claude/CLAUDE.md", IsJSON: false},
	}
	m := newConflictResolver(ctx, conflicts)
	// Skip the bulk strategy picker — head straight to manual.
	m.strategyPending = false

	// Cursor on JSON → enter → should switch to key resolver.
	m.cursor = 0
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on JSON conflict must produce a switchTo cmd")
	}
	msg := cmd()
	sw, ok := msg.(switchScreenMsg)
	if !ok {
		t.Fatalf("enter on JSON conflict: expected switchScreenMsg, got %T", msg)
	}
	if _, isKeyResolver := sw.s.(*conflictKeyResolverModel); !isKeyResolver {
		t.Errorf("enter on JSON conflict: expected key resolver, got %T", sw.s)
	}

	// Cursor on text → enter → should switch to HUNK resolver, not do nothing.
	m = newConflictResolver(ctx, conflicts)
	m.strategyPending = false
	m.cursor = 1
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on text conflict must drill in, not be silent — pre-fix it did nothing")
	}
	msg = cmd()
	sw, ok = msg.(switchScreenMsg)
	if !ok {
		t.Fatalf("enter on text conflict: expected switchScreenMsg, got %T", msg)
	}
	if _, isHunkResolver := sw.s.(*conflictHunkResolverModel); !isHunkResolver {
		t.Errorf("enter on text conflict: expected hunk resolver, got %T", sw.s)
	}
}

// TestConflictResolverStrategyPickerDisplaysPreview pins that the
// strategy picker shows up by default (strategyPending defaults true
// when conflicts exist) and renders the bulk-choice UI with the
// "how should we resolve?" prompt. A refactor that accidentally set
// strategyPending=false on new resolvers would silently skip the
// bulk shortcut — users would land on the per-file picker with no
// "just take remote" one-shot.
func TestConflictResolverStrategyPickerDisplaysPreview(t *testing.T) {
	ctx := testConflictCtx(t)
	conflicts := []sync.FileConflict{
		{Path: "profiles/default/claude.json"},
	}
	m := newConflictResolver(ctx, conflicts)
	if !m.strategyPending {
		t.Error("newConflictResolver must default strategyPending=true when conflicts exist")
	}
	view := m.View()
	if !contains(view, "how should we resolve") {
		t.Errorf("strategy picker should ask 'how should we resolve?'; got:\n%s", view)
	}
	if !contains(view, "replace local with cloud") || !contains(view, "replace cloud with local") {
		t.Errorf("strategy picker missing the two bulk-choice phrasings; got:\n%s", view)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
