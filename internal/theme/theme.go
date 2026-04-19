// Package theme is the single source of truth for ccsync's visual palette.
// No hex codes should live outside this package.
package theme

import "github.com/charmbracelet/lipgloss"

var (
	Accent   = lipgloss.Color("#D97757")
	Accent2  = lipgloss.Color("#CC785C")
	Cream    = lipgloss.Color("#F5F0E8")
	Ink      = lipgloss.Color("#2C2926")
	Muted    = lipgloss.Color("#8B857A")
	Success  = lipgloss.Color("#6B8E4E")
	Warning  = lipgloss.Color("#D4A24C")
	Conflict = lipgloss.Color("#C84A4A")
)
