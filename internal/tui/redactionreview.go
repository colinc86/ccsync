package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/theme"
)

// redactionReviewModel lets the user paste in values for missing secrets
// surfaced by the last sync (keychain had no entry for these placeholders).
// Values go directly into the OS keychain; the next sync restores them.
type redactionReviewModel struct {
	ctx    *AppContext
	paths  []string
	cursor int
	input  textinput.Model
	saved  map[string]bool
	err    error
}

func newRedactionReview(ctx *AppContext, paths []string) *redactionReviewModel {
	ti := textinput.New()
	ti.Placeholder = "paste secret value"
	ti.EchoMode = textinput.EchoPassword
	ti.CharLimit = 4096
	ti.Width = 40
	return &redactionReviewModel{
		ctx:   ctx,
		paths: paths,
		input: ti,
		saved: map[string]bool{},
	}
}

func (m *redactionReviewModel) Title() string {
	return fmt.Sprintf("Redaction review (%d missing)", len(m.paths))
}

func (m *redactionReviewModel) Init() tea.Cmd { return textinput.Blink }

func (m *redactionReviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "shift+tab":
			if !m.input.Focused() && m.cursor > 0 {
				m.cursor--
			}
		case "down", "tab":
			if !m.input.Focused() && m.cursor < len(m.paths)-1 {
				m.cursor++
			}
		case "enter":
			if m.input.Focused() {
				val := m.input.Value()
				path := m.paths[m.cursor]
				if val != "" {
					if err := secrets.Store(secrets.Key(m.ctx.State.ActiveProfile, path), val); err != nil {
						m.err = err
					} else {
						m.saved[path] = true
						m.err = nil
						m.input.Reset()
					}
				}
				m.input.Blur()
				if m.cursor < len(m.paths)-1 {
					m.cursor++
				}
			} else {
				m.input.Reset()
				m.input.Focus()
				return m, textinput.Blink
			}
		case "esc":
			if m.input.Focused() {
				m.input.Blur()
				return m, nil
			}
		}
	}

	if m.input.Focused() {
		m.input, cmd = m.input.Update(msg)
	}
	return m, cmd
}

func (m *redactionReviewModel) View() string {
	if len(m.paths) == 0 {
		return theme.Good.Render("no missing secrets")
	}
	var sb strings.Builder
	sb.WriteString(theme.Hint.Render(
		"paste each secret; values go straight to the OS keychain (they never touch disk or the sync repo)") + "\n\n")

	for i, p := range m.paths {
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		mark := theme.Warn.Render("pending")
		if m.saved[p] {
			mark = theme.Good.Render("saved  ")
		}
		sb.WriteString(fmt.Sprintf("%s%s  %s\n", cursor, mark, p))
	}

	sb.WriteString("\n")
	if m.input.Focused() {
		sb.WriteString(theme.Secondary.Render("value: "))
		sb.WriteString(m.input.View())
		sb.WriteString("\n" + theme.Hint.Render("enter saves • esc cancels"))
	} else {
		sb.WriteString(theme.Hint.Render("enter to type a value • ↑↓ move"))
	}
	if m.err != nil {
		sb.WriteString("\n" + theme.Bad.Render("error: "+m.err.Error()))
	}
	return sb.String()
}
