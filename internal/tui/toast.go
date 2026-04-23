package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/theme"
)

// toastKind drives the border color and leading glyph of a toast. Kept
// small and semantic — if a screen wants a neutral-but-info message
// it uses toastInfo; anything stronger maps to success / warn / error.
type toastKind int

const (
	toastInfo toastKind = iota
	toastSuccess
	toastWarn
	toastError
)

// toastPayload is the content + presentation info AppModel stores
// while a toast is on screen. id is the sequence counter used to
// invalidate stale Tick messages — without it, a toast that gets
// replaced by a newer one would still be cleared when the earlier
// Tick eventually fires, cutting the replacement's display short.
type toastPayload struct {
	id   int
	kind toastKind
	text string
}

// showToastMsg is the message any screen returns via showToast(...)
// to ask AppModel to display a transient notice.
type showToastMsg struct {
	kind toastKind
	text string
}

// toastExpireMsg is delivered by the Tick a new toast schedules. If
// the id matches AppModel's current toast, the toast clears; a later
// toast arriving in the meantime will have bumped the id and will
// survive this expire.
type toastExpireMsg struct{ id int }

// toastDuration is how long a toast stays on screen before the
// auto-expire fires. 2.5s is long enough for a quick glance without
// lingering past the user's next input.
const toastDuration = 2500 * time.Millisecond

// showToast is the shell-level API any screen's Update can return to
// surface a transient message. Usage: `return m, showToast("sync
// complete", toastSuccess)`. AppModel batches the Tick internally;
// callers don't have to worry about scheduling or cleanup.
func showToast(text string, kind toastKind) tea.Cmd {
	return func() tea.Msg {
		return showToastMsg{kind: kind, text: text}
	}
}

// scheduleToastExpire returns a Cmd that delivers a toastExpireMsg
// tagged with the given id after toastDuration has elapsed.
func scheduleToastExpire(id int) tea.Cmd {
	return tea.Tick(toastDuration, func(time.Time) tea.Msg {
		return toastExpireMsg{id: id}
	})
}

// renderToast draws the current toast as a bordered rounded-corner
// pill in the appropriate kind color. Returns empty when no toast is
// active. Pinned to a 44-column minimum so short texts still feel
// substantial; longer content wraps naturally inside the card.
func renderToast(t *toastPayload) string {
	if t == nil {
		return ""
	}
	var border lipgloss.Color
	var glyph string
	var textStyle lipgloss.Style
	switch t.kind {
	case toastSuccess:
		border = theme.Success
		glyph = "✓"
		textStyle = theme.Good.Bold(true)
	case toastWarn:
		border = theme.Warning
		glyph = "◦"
		textStyle = theme.Warn.Bold(true)
	case toastError:
		border = theme.Conflict
		glyph = "!"
		textStyle = theme.Bad
	default:
		border = theme.Accent
		glyph = "›"
		textStyle = theme.Primary
	}
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 2)
	return card.Render(textStyle.Render(glyph+" ") + t.text)
}
