package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/theme"
)

// renderActivityCalendar turns a list of commit log entries into a
// GitHub-contributions-style grid: 7 rows (weekdays) × 26 columns (weeks).
// Cells are colored by the number of commits landing on that day. Commits
// older than ~26 weeks are bucketed off the left edge and ignored.
func renderActivityCalendar(entries []gitx.LogEntry) string {
	const weeks = 26
	const days = 7

	// Today at 00:00 local. Right-align the grid so the final column is the
	// current week — matches GitHub's layout.
	today := time.Now().Local()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())

	// Find the Sunday that starts the left-most column.
	offsetFromSunday := int(todayStart.Weekday()) // 0 = Sunday
	lastColStart := todayStart.AddDate(0, 0, -offsetFromSunday)
	firstColStart := lastColStart.AddDate(0, 0, -7*(weeks-1))

	counts := map[string]int{}
	for _, e := range entries {
		d := e.When.Local()
		ds := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
		if ds.Before(firstColStart) {
			continue
		}
		counts[ds.Format("2006-01-02")]++
	}

	// Compute max for color ramp scaling.
	maxCount := 0
	for _, n := range counts {
		if n > maxCount {
			maxCount = n
		}
	}

	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("Activity") + "\n\n")
	sb.WriteString(renderMonthLabels(firstColStart, weeks) + "\n")

	dayLabels := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	for row := 0; row < days; row++ {
		sb.WriteString(theme.Hint.Render(fmt.Sprintf("%3s ", dayLabels[row])))
		for col := 0; col < weeks; col++ {
			cellDate := firstColStart.AddDate(0, 0, col*7+row)
			// Don't render future cells.
			if cellDate.After(todayStart) {
				sb.WriteString("  ")
				continue
			}
			n := counts[cellDate.Format("2006-01-02")]
			sb.WriteString(cellGlyph(n, maxCount))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(theme.Hint.Render("less ") + legend() + theme.Hint.Render(" more"))
	if maxCount == 0 {
		sb.WriteString("\n\n" + theme.Hint.Render("(no sync activity in the last 6 months)"))
	}
	return sb.String()
}

// renderMonthLabels produces the top label row with month names aligned
// above the first column of each new month.
func renderMonthLabels(start time.Time, weeks int) string {
	var sb strings.Builder
	sb.WriteString("    ") // leave space for day labels column
	prev := ""
	for col := 0; col < weeks; col++ {
		colStart := start.AddDate(0, 0, col*7)
		m := colStart.Format("Jan")
		if m != prev {
			// Each cell is 2 chars wide. Pad month name to 6 = 3 cells; we
			// skip printing later cells within the same month.
			sb.WriteString(theme.Hint.Render(fmt.Sprintf("%-6s", m)))
			prev = m
			col += 2 // skip the next 2 columns (the month label used 3 cells)
		} else {
			// already handled by skip above
		}
	}
	return sb.String()
}

// cellGlyph picks a block character + color for one cell based on commit
// count, ramp-scaled against the busiest day in the window.
func cellGlyph(n, max int) string {
	if n == 0 {
		return theme.Hint.Render("· ")
	}
	// 4-level ramp based on relative intensity.
	var level int
	switch {
	case max <= 1:
		level = 3
	default:
		bucket := float64(n) / float64(max)
		switch {
		case bucket >= 0.75:
			level = 3
		case bucket >= 0.5:
			level = 2
		case bucket >= 0.25:
			level = 1
		default:
			level = 0
		}
	}
	glyphs := []string{"░ ", "▒ ", "▓ ", "█ "}
	colors := []lipgloss.TerminalColor{theme.Warning, theme.Warning, theme.Success, theme.Success}
	st := lipgloss.NewStyle().Foreground(colors[level])
	return st.Render(glyphs[level])
}

// legend is a horizontal "less ░▒▓█ more" scale shown beneath the grid.
func legend() string {
	var sb strings.Builder
	for i := 0; i < 4; i++ {
		sb.WriteString(cellGlyph(i+1, 4))
	}
	return sb.String()
}
