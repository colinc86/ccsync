package tui

import (
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/profileinspect"
)

// TestInspectorRendersSectionsAndChips pins the inspector layout:
// when the view has items across multiple categories, the rendered
// output carries per-section headings, the summary chip row, and
// status chips inline. A regression in any of those three elements
// would quietly strip signal from the "what's syncing" view.
func TestInspectorRendersSectionsAndChips(t *testing.T) {
	m := &profileInspectModel{
		view: &profileinspect.View{
			Profile: "default",
			Sections: []profileinspect.Section{
				{
					Kind:  profileinspect.KindSkill,
					Label: "Skills",
					Items: []profileinspect.Item{
						{Title: "research", Description: "Full research pipeline", Kind: profileinspect.KindSkill, Status: profileinspect.StatusSynced},
						{Title: "claude-api", Description: "Build Claude API apps", Kind: profileinspect.KindSkill, Status: profileinspect.StatusPendingPush},
					},
				},
				{
					Kind:  profileinspect.KindMCPServer,
					Label: "MCP Servers",
					Items: []profileinspect.Item{
						{Title: "gemini", Description: "launches `gemini-mcp`", Kind: profileinspect.KindMCPServer, Status: profileinspect.StatusSynced},
					},
				},
			},
		},
	}
	m.flat = flatten(m.view)
	out := m.View()

	// Headings are present and include the count suffix.
	for _, want := range []string{"Skills (2)", "MCP Servers (1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("section heading missing: %q\n---\n%s", want, out)
		}
	}
	// Each item's title made it into the render.
	for _, want := range []string{"research", "claude-api", "gemini"} {
		if !strings.Contains(out, want) {
			t.Errorf("item missing: %q", want)
		}
	}
	// The status chips render with their label text.
	for _, want := range []string{"synced", "pending push"} {
		if !strings.Contains(out, want) {
			t.Errorf("status chip missing: %q", want)
		}
	}
	// Commands get a "/" prefix — exercised here by commands-free
	// fixture above, so this guard is just a noop. The real prefix
	// logic is covered by the inspect_test.go unit tests on
	// extractors; keep the render-side pin minimal.
}

// TestInspectorEmptyState pins the fresh-bootstrap view: no items
// yields the neutral-bordered hero card with the "add a skill /
// command / MCP" hint, not a blank screen.
func TestInspectorEmptyState(t *testing.T) {
	m := &profileInspectModel{
		view: &profileinspect.View{Profile: "default"},
	}
	out := m.View()
	if !strings.Contains(out, "NOTHING SYNCED YET") {
		t.Error("empty view should render the hero card")
	}
	if !strings.Contains(out, "add a skill") {
		t.Error("empty state should hint at how to start")
	}
}

// TestInspectorCursorSkipsHeaders pins the cursor-navigation
// invariant: ↑↓ land on Item rows, never on section Header rows.
// Users pressing down on the last item in a section must land on
// the first item of the next section, not its heading.
func TestInspectorCursorSkipsHeaders(t *testing.T) {
	m := &profileInspectModel{
		view: &profileinspect.View{
			Sections: []profileinspect.Section{
				{Kind: profileinspect.KindSkill, Label: "Skills", Items: []profileinspect.Item{
					{Title: "a", Kind: profileinspect.KindSkill},
				}},
				{Kind: profileinspect.KindCommand, Label: "Commands", Items: []profileinspect.Item{
					{Title: "b", Kind: profileinspect.KindCommand},
				}},
			},
		},
	}
	m.flat = flatten(m.view)
	// Flat layout: [header-skills, item-a, header-commands, item-b]
	// cursor starts at 0 (a header); nudge(0) must move to first
	// item.
	m.nudgeCursor(0)
	if m.cursor != 1 || m.flat[m.cursor].Header {
		t.Fatalf("cursor should land on first item (idx 1), got %d", m.cursor)
	}
	// Pressing down once must skip the Commands header and land on
	// item "b" (idx 3).
	m.nudgeCursor(1)
	if m.cursor != 3 || m.flat[m.cursor].Header {
		t.Errorf("down from idx 1 should skip header and land on idx 3; got %d", m.cursor)
	}
}
