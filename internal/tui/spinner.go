package tui

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/theme"
)

// newSpinner returns a bubbles spinner styled with the ccsync palette.
// Usage: keep the spinner in your model, return sp.Tick from Init(), and
// forward spinner.TickMsg to sp.Update in your Update method. Render with
// sp.View() alongside a short status string.
func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(theme.Accent)
	return s
}
