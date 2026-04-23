package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/theme"
)

// helpOverlayContent is the static cheat-sheet shown when the user hits
// `?` anywhere in the TUI. Curated rather than introspected — most
// screens' bindings are stable enough that maintaining this once and
// updating when we add a major key is cheaper than per-screen wiring.
var helpOverlayContent = []helpSection{
	{
		Title: "navigation",
		Rows: []helpRow{
			{"ctrl+k", "command palette — quick jump to any action"},
			{"↑↓ / j k", "move cursor"},
			{"enter", "select / confirm"},
			{"esc", "back (or quit on Home)"},
			{"ctrl+c", "quit"},
			{"?", "toggle this help"},
		},
	},
	{
		Title: "home dashboard",
		Rows: []helpRow{
			{"enter", "sync now (or start setup)"},
			{"m", "more — profiles, history, settings, …"},
			{"r", "re-check remote now"},
			{"q", "quit"},
		},
	},
	{
		Title: "sync preview",
		Rows: []helpRow{
			{"enter", "apply all"},
			{"p", "pull only"},
			{"u", "push only"},
			{"s", "selective sync"},
			{"d", "diff cursored file"},
		},
	},
	{
		Title: "conflict resolver",
		Rows: []helpRow{
			{"1 / 2 / 3", "bulk: take remote / take local / manual"},
			{"l / r", "(manual) take local / remote for cursored"},
			{"enter", "(manual) drill in — per-key (JSON) or per-hunk (text)"},
			{"h", "(manual) per-hunk picker (text) — alternate to enter"},
			{"d", "diff local vs remote"},
			{"a", "apply all resolutions (once everything resolved)"},
		},
	},
	{
		Title: "history",
		Rows: []helpRow{
			{"/", "filter"},
			{"c", "clear filter"},
			{"b", "rollback to cursor"},
			{"v", "calendar view"},
		},
	},
	{
		Title: "browse tracked files",
		Rows: []helpRow{
			{"space", "toggle profile exclude"},
			{"p", "promote file to default (shared across profiles)"},
			{"i", "add to .syncignore"},
			{"w", "why — trace rules"},
		},
	},
	{
		Title: "review screen (pre-push)",
		Rows: []helpRow{
			{"space / x", "toggle allow / deny"},
			{"p", "promote to default (push item will share across profiles)"},
			{"a", "allow all"},
			{"d", "deny all"},
			{"enter", "apply"},
		},
	},
	{
		Title: "profiles",
		Rows: []helpRow{
			{"enter", "switch to profile"},
			{"n", "new profile"},
			{"e", "edit name + description"},
			{"d", "delete (with confirm)"},
		},
	},
}

type helpSection struct {
	Title string
	Rows  []helpRow
}

type helpRow struct {
	Key  string
	Desc string
}

// renderHelpOverlay builds the help panel as a bordered lipgloss block.
// The key column renders as inverse-video pills (theme.Keycap) so the
// cheat sheet reads as a keyboard, not a printed manual. Section
// titles get a subtle separator rule on the same line so the eye
// finds "navigation", "home", "sync preview", etc. without
// over-crowding the layout.
func renderHelpOverlay() string {
	var sb strings.Builder
	sb.WriteString(theme.WordmarkStyle.Render("ccsync") + "  " +
		theme.Subtle.Render("keybindings") + "\n")
	sb.WriteString(theme.Rule.Render(strings.Repeat("─", 48)) + "\n\n")

	// Two-column flow by padding each key-pill to a consistent width
	// so descriptions align vertically. Use lipgloss.Width to measure
	// the RENDERED width (pills have ANSI escape codes that plain
	// strings.Repeat can't see around).
	const keyColWidth = 16
	for i, section := range helpOverlayContent {
		sb.WriteString(theme.Secondary.Bold(true).Render(section.Title) + "\n")
		for _, row := range section.Rows {
			pill := theme.KeycapMuted.Render(row.Key)
			pad := keyColWidth - lipgloss.Width(pill)
			if pad < 1 {
				pad = 1
			}
			sb.WriteString("  " + pill + strings.Repeat(" ", pad) + theme.Hint.Render(row.Desc) + "\n")
		}
		if i < len(helpOverlayContent)-1 {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n" + theme.Hint.Render("any key to dismiss"))

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Accent).
		Padding(1, 2).
		Render(sb.String())
	return panel
}
