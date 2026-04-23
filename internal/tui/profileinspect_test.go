package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/profileinspect"
	"github.com/colinc86/ccsync/internal/state"
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

// TestInspectorWhyOnSkillSetsMessage pins that pressing `w` on a
// cursored file-backed item calls why.Explain and puts a trace into
// m.message so the next View() renders it. The user report from
// v0.8.0 was that `w` appeared to do nothing — either because the
// handler silently returned early or because the trace never made
// it into rendered output. This test holds the contract both ways:
// after the keystroke, the message is populated AND the rendered
// View contains its text.
func TestInspectorWhyOnSkillSetsMessage(t *testing.T) {
	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{ActiveProfile: "default", LastSyncedSHA: map[string]string{}}
	ctx := &AppContext{Config: cfg, State: st, ClaudeDir: "/tmp/ccsync-inspect-why", ClaudeJSON: "/tmp/ccsync-inspect-why.json"}
	m := &profileInspectModel{
		ctx: ctx,
		view: &profileinspect.View{
			Sections: []profileinspect.Section{
				{Kind: profileinspect.KindSkill, Label: "Skills", Items: []profileinspect.Item{
					{Title: "research", Path: "claude/skills/research/SKILL.md", Kind: profileinspect.KindSkill, Status: profileinspect.StatusSynced},
				}},
			},
		},
	}
	m.flat = flatten(m.view)
	m.nudgeCursor(0) // land on the skill row

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	pm := updated.(*profileInspectModel)
	if pm.err != nil {
		t.Fatalf("why handler set err = %v — why.Explain should return a Trace, not an error, for a valid repo-relative path", pm.err)
	}
	if pm.message == "" {
		t.Fatal("after pressing w on a skill, m.message should carry the why-trace; it's empty, which is what the user sees as 'why doesn't work'")
	}
	if !strings.Contains(pm.message, "path:") || !strings.Contains(pm.message, "profile:") {
		t.Errorf("m.message doesn't look like a why-trace: %q", pm.message)
	}

	// And the rendered view must surface that message — setting it
	// without rendering it would produce the same user-visible
	// symptom.
	out := pm.View()
	if !strings.Contains(out, "path:") {
		t.Error("View() output doesn't contain the why-trace lines; message isn't making it to the screen")
	}
}

// TestInspectorWhyOnMCPServer pins that `w` on an MCP server entry
// actually surfaces a trace instead of being silently ignored. Pre-
// v0.8.1 the handler early-returned for every MCP item because
// Item.Path uses a `#mcpServers.<name>` suffix that why.Explain
// doesn't speak; the user pressed `w` on gemini, saw nothing, and
// reasonably concluded the feature was broken.
func TestInspectorWhyOnMCPServer(t *testing.T) {
	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{ActiveProfile: "default", LastSyncedSHA: map[string]string{}}
	ctx := &AppContext{Config: cfg, State: st, ClaudeDir: "/tmp/ccsync-inspect-why-mcp", ClaudeJSON: "/tmp/ccsync-inspect-why-mcp.json"}
	m := &profileInspectModel{
		ctx: ctx,
		view: &profileinspect.View{
			Sections: []profileinspect.Section{
				{Kind: profileinspect.KindMCPServer, Label: "MCP Servers", Items: []profileinspect.Item{
					{Title: "gemini", Path: "claude.json#mcpServers.gemini", Kind: profileinspect.KindMCPServer, Status: profileinspect.StatusSynced},
				}},
			},
		},
	}
	m.flat = flatten(m.view)
	m.nudgeCursor(0)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	pm := updated.(*profileInspectModel)
	if pm.message == "" {
		t.Fatal("pressing w on an MCP server should produce a trace; the handler silently returning is the pre-v0.8.1 bug")
	}
	if !strings.Contains(pm.message, "mcpServers.gemini") && !strings.Contains(pm.message, "gemini") {
		t.Errorf("trace should name the server key, got: %q", pm.message)
	}
}
