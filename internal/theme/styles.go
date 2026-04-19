package theme

import "github.com/charmbracelet/lipgloss"

var (
	Primary   = lipgloss.NewStyle().Foreground(Accent).Bold(true)
	Secondary = lipgloss.NewStyle().Foreground(Accent2)
	Subtle    = lipgloss.NewStyle().Foreground(Muted)
	Heading   = lipgloss.NewStyle().Foreground(Accent).Bold(true).Underline(true)
	Good      = lipgloss.NewStyle().Foreground(Success)
	Warn      = lipgloss.NewStyle().Foreground(Warning)
	Bad       = lipgloss.NewStyle().Foreground(Conflict).Bold(true)
	Hint      = lipgloss.NewStyle().Foreground(Muted).Italic(true)
	Card      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Accent).Padding(0, 1)
)
