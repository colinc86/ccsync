package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/profileinspect"
	"github.com/colinc86/ccsync/internal/theme"
)

// profileInspectModel is the "what's in this profile" screen. Lists
// the user's synced things — skills, commands, subagents, MCP
// servers, memory, settings — grouped by category with a title and
// description extracted from each file. No paths. No file-sizes.
// The pro-level path view (Browse Tracked) stays available behind
// its own `b` shortcut for power users.
type profileInspectModel struct {
	ctx    *AppContext
	view   *profileinspect.View
	err    error
	cursor int
	// flat mirrors Sections's Items so cursor indexing doesn't have
	// to recompute (section-idx, item-idx) pairs on every keystroke.
	flat []flatRow
}

// flatRow is one renderable row in the flattened Sections tree —
// either a section header (no item) or a data row. Headers are
// non-selectable; cursor skips over them so ↑↓ feels linear.
type flatRow struct {
	Header  bool
	Section profileinspect.Section
	Item    profileinspect.Item
}

func newProfileInspect(ctx *AppContext) *profileInspectModel {
	m := &profileInspectModel{ctx: ctx}
	m.reload()
	return m
}

func (m *profileInspectModel) Title() string { return "Inspect profile" }
func (m *profileInspectModel) Init() tea.Cmd { return nil }

// reload rebuilds the view from the current state. Called on open
// and on `r` refresh. Network-free — inspect is pure local I/O.
func (m *profileInspectModel) reload() {
	v, err := profileinspect.Inspect(profileinspect.Inputs{
		Config:     m.ctx.Config,
		State:      m.ctx.State,
		ClaudeDir:  m.ctx.ClaudeDir,
		ClaudeJSON: m.ctx.ClaudeJSON,
		RepoPath:   m.ctx.RepoPath,
	})
	m.err = err
	m.view = v
	m.flat = flatten(v)
	if m.cursor >= len(m.flat) {
		m.cursor = 0
	}
}

// flatten walks a View and produces the renderable row list. Each
// Section contributes one Header row + one row per Item. The caller
// skips Header rows in cursor navigation, so the layout stays
// section-aware without a second data structure.
func flatten(v *profileinspect.View) []flatRow {
	if v == nil {
		return nil
	}
	var out []flatRow
	for _, s := range v.Sections {
		out = append(out, flatRow{Header: true, Section: s})
		for _, it := range s.Items {
			out = append(out, flatRow{Item: it, Section: s})
		}
	}
	return out
}

// firstSelectableFrom returns the next non-header row index from
// start (inclusive). Returns -1 if none — zero-item view.
func (m *profileInspectModel) firstSelectableFrom(start int) int {
	for i := start; i < len(m.flat); i++ {
		if !m.flat[i].Header {
			return i
		}
	}
	return -1
}

func (m *profileInspectModel) lastSelectableTo(end int) int {
	for i := end; i >= 0; i-- {
		if !m.flat[i].Header {
			return i
		}
	}
	return -1
}

// nudgeCursor moves to the next/prev selectable row, wrapping
// within the list bounds. Header rows are invisible to the cursor.
func (m *profileInspectModel) nudgeCursor(delta int) {
	if len(m.flat) == 0 {
		return
	}
	// Make sure we're on a selectable row to start.
	if m.cursor < 0 || m.cursor >= len(m.flat) || m.flat[m.cursor].Header {
		if n := m.firstSelectableFrom(0); n >= 0 {
			m.cursor = n
		}
		return
	}
	target := m.cursor + delta
	for target >= 0 && target < len(m.flat) && m.flat[target].Header {
		target += delta
	}
	if target < 0 || target >= len(m.flat) {
		return
	}
	m.cursor = target
}

func (m *profileInspectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.nudgeCursor(-1)
		case "down", "j":
			m.nudgeCursor(1)
		case "r":
			m.reload()
		case "b":
			// Power-user escape hatch — jump to Browse Tracked from
			// the inspector for the path-level view.
			return m, switchTo(newBrowseTracked(m.ctx))
		}
	}
	return m, nil
}

func (m *profileInspectModel) View() string {
	var sb strings.Builder

	if m.err != nil {
		sb.WriteString(renderError(m.err) + "\n\n")
	}

	// Empty-state hero card: fresh bootstrap, nothing on either
	// side yet. Lands on something reassuring + actionable.
	if m.view == nil || m.view.Empty() {
		body := theme.Subtle.Bold(true).Render("◦ NOTHING SYNCED YET") + "\n" +
			theme.Hint.Render(
				"add a skill under ~/.claude/skills/, a command under\n"+
					"~/.claude/commands/, or an MCP server in ~/.claude.json\n"+
					"— then run sync. this screen will show what's flowing.")
		sb.WriteString(theme.CardNeutral.Width(60).Render(body) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "r", label: "refresh", primary: true},
			{cap: "esc", label: "back"},
		}))
		return sb.String()
	}

	// Summary chips — total + per-section counts in a dot-separated
	// row at top. Lets the user see the inventory shape before
	// scanning rows.
	sb.WriteString(renderInspectorChipRow(m.view) + "\n\n")

	// Grouped sections. Items selectable; headers aren't.
	for i, row := range m.flat {
		if row.Header {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(theme.Secondary.Bold(true).Render(
				fmt.Sprintf("%s (%d)", row.Section.Label, len(row.Section.Items))))
			sb.WriteString("\n")
			sb.WriteString(theme.Rule.Render(strings.Repeat("─", 28)) + "\n")
			continue
		}
		cursor := "  "
		if i == m.cursor {
			cursor = theme.Primary.Render("▸ ")
		}
		sb.WriteString(cursor + renderInspectorRow(row.Item) + "\n")
	}

	sb.WriteString("\n" + renderFooterBar([]footerKey{
		{cap: "↑↓", label: "move", primary: true},
		{cap: "r", label: "refresh"},
		{cap: "b", label: "browse paths"},
		{cap: "esc", label: "back"},
	}))
	return sb.String()
}

// renderInspectorChipRow builds the "5 skills · 3 commands · 2 agents
// · 1 mcp" summary strip. Ordered by sectionOrder (the same order
// Sections render in) so the pill sequence matches the layout below.
func renderInspectorChipRow(v *profileinspect.View) string {
	if v == nil {
		return ""
	}
	var chips []string
	for _, s := range v.Sections {
		n := len(s.Items)
		if n == 0 {
			continue
		}
		chips = append(chips, theme.ChipNeutral.Render(
			fmt.Sprintf("%d %s", n, strings.ToLower(s.Label))))
	}
	return strings.Join(chips, theme.Rule.Render("  ·  "))
}

// renderInspectorRow returns a single item row: leading glyph, title,
// em-dashed description, status chip at the right. The title is
// bold for visual hierarchy; the description is muted.
func renderInspectorRow(it profileinspect.Item) string {
	glyph := inspectorGlyph(it.Kind)
	title := theme.Primary.Render(it.Title)
	// Commands prefix a `/` in the title to match the "you'd type
	// this at a slash command prompt" convention.
	if it.Kind == profileinspect.KindCommand {
		title = theme.Primary.Render("/" + it.Title)
	}
	desc := ""
	if it.Description != "" {
		desc = "  " + theme.Hint.Render("— "+it.Description)
	}
	chip := inspectorStatusChip(it.Status)
	// Glue: glyph title desc .... chip. No strict column alignment
	// at terminal-width-awareness yet — sections are tight enough
	// that the ragged right edge reads fine. If we add WindowSizeMsg
	// handling later, this is where alignment would land.
	return glyph + " " + title + desc + "  " + chip
}

// inspectorGlyph is the leading character next to each item row,
// hinting at what kind of thing it is. Bolded in a kind-specific
// accent so it reads as "the skills rows, the MCP rows" even when
// the user is scrolling fast.
func inspectorGlyph(k profileinspect.Kind) string {
	switch k {
	case profileinspect.KindSkill:
		return theme.Secondary.Bold(true).Render("✎")
	case profileinspect.KindCommand:
		return theme.Primary.Render("›")
	case profileinspect.KindAgent:
		return theme.Secondary.Bold(true).Render("◎")
	case profileinspect.KindMCPServer:
		return theme.Warn.Bold(true).Render("⚡")
	case profileinspect.KindMemory:
		return theme.Subtle.Bold(true).Render("✧")
	case profileinspect.KindClaudeMD:
		return theme.Subtle.Bold(true).Render("📄")
	case profileinspect.KindSettings:
		return theme.Subtle.Bold(true).Render("⚙")
	}
	return theme.Subtle.Render("·")
}

// inspectorStatusChip colour-codes each item's sync state:
//   - synced: green (✓)
//   - pending push: warn (↑)
//   - pending pull: warn (↓)
//   - excluded: neutral (⊘)
func inspectorStatusChip(s profileinspect.Status) string {
	switch s {
	case profileinspect.StatusSynced:
		return theme.ChipGood.Render("✓ synced")
	case profileinspect.StatusPendingPush:
		return theme.ChipWarn.Render("↑ pending push")
	case profileinspect.StatusPendingPull:
		return theme.ChipWarn.Render("↓ pending pull")
	case profileinspect.StatusExcluded:
		return theme.ChipNeutral.Render("⊘ excluded")
	}
	return ""
}

// Ensure lipgloss is referenced so the import isn't dropped on a
// future refactor. The theme package re-exports styles, but we
// still rely on lipgloss for rendering inside some helpers.
var _ = lipgloss.Width
