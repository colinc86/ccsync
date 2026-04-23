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

	// Keycap renders a keyboard shortcut inline as a bold accent-color
	// glyph — `enter` or `q` — with no background pill. Pairs with a
	// muted-text label so the eye reads "bold letter = key, normal
	// text = what it does" without the visual weight of an inverse-
	// video block. Apply uniformly: every pressable key on a screen
	// gets the same treatment so no one key looks more "themed" than
	// the others.
	Keycap = lipgloss.NewStyle().
		Foreground(Accent).
		Bold(true)

	// Wordmark is the top-of-screen "ccsync" identity block. Bolded
	// accent on its own line with a subtle underline rule — reads as
	// a product logo rather than a CLI help header. Intended to be
	// rendered via the Wordmark() helper which pairs it with an
	// em-dash tagline.
	WordmarkStyle = lipgloss.NewStyle().
			Foreground(Accent).
			Bold(true)

	// Rule is a horizontal divider used to frame sections inside a
	// card or between stanzas on the Home dashboard. Three glyphs so
	// it hugs dense content without overpowering.
	Rule = lipgloss.NewStyle().Foreground(Accent2)

	// ChipGood/Warn/Bad/Neutral are small inline badges — think a
	// "● in sync" pill — for status chips in dense layouts like
	// sync history rows and the Home header.
	ChipGood    = lipgloss.NewStyle().Foreground(Success).Bold(true)
	ChipWarn    = lipgloss.NewStyle().Foreground(Warning).Bold(true)
	ChipBad     = lipgloss.NewStyle().Foreground(Conflict).Bold(true)
	ChipNeutral = lipgloss.NewStyle().Foreground(Muted).Bold(true)

	// CardClean / CardPending / CardConflict are state-reactive
	// containers for the Home hero status block. Same rounded-border
	// shape, different accent so the user's first glance at the
	// dashboard reads the sync health without having to parse text.
	CardClean = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Success).
			Padding(1, 2)

	CardPending = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Warning).
			Padding(1, 2)

	CardConflict = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Conflict).
			Padding(1, 2)

	CardNeutral = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Muted).
			Padding(1, 2)
)

// Wordmark renders the ccsync identity block — the app name in
// bolded accent, an em-dash, and a one-line tagline in muted. Use
// at the top of any "home-ish" screen (dashboard, onboarding) so
// the TUI always identifies itself. Returns a single pre-rendered
// string ready for Builder.WriteString.
func Wordmark(tagline string) string {
	name := WordmarkStyle.Render("ccsync")
	if tagline == "" {
		return name
	}
	return name + " " + Hint.Render("— "+tagline)
}
