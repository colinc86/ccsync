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
			{"↑↓ / j k", "move cursor"},
			{"enter", "select / confirm"},
			{"esc", "back (or quit on Home)"},
			{"ctrl+c", "quit"},
			{"?", "toggle this help"},
		},
	},
	{
		Title: "home",
		Rows: []helpRow{
			{"1-9", "jump to menu item"},
			{"r", "refresh sync status now"},
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
			{"enter", "(manual) per-key picker (JSON)"},
			{"h", "(manual) per-hunk picker (text)"},
			{"d", "diff local vs remote"},
			{"a", "apply all resolutions"},
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
			{"i", "add to .syncignore"},
			{"w", "why — trace rules"},
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
func renderHelpOverlay() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("ccsync keybindings") + "\n\n")

	for i, section := range helpOverlayContent {
		sb.WriteString(theme.Secondary.Render(section.Title) + "\n")
		for _, row := range section.Rows {
			// Pad the key column to 14 characters for alignment.
			keyStr := theme.Primary.Render(padRight(row.Key, 14))
			sb.WriteString("  " + keyStr + "  " + theme.Hint.Render(row.Desc) + "\n")
		}
		if i < len(helpOverlayContent)-1 {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n" + theme.Hint.Render("? again — or any key — to dismiss"))

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Accent).
		Padding(1, 2).
		Render(sb.String())
	return panel
}

// padRight pads s with spaces on the right to reach width n. Used for
// key-column alignment in the overlay.
func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
