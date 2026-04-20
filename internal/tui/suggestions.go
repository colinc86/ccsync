package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/suggest"
	"github.com/colinc86/ccsync/internal/theme"
)

// suggestionsModel lists auto-generated rule proposals. Enter accepts
// (appends to .syncignore); `x` dismisses permanently (stored in
// state.DismissedSuggestions). The list re-derives from the cached plan
// every render so accepting one causes the rest to re-evaluate.
type suggestionsModel struct {
	ctx     *AppContext
	cursor  int
	err     error
	message string
}

func newSuggestions(ctx *AppContext) *suggestionsModel {
	return &suggestionsModel{ctx: ctx}
}

func (m *suggestionsModel) Title() string { return "Suggestions" }
func (m *suggestionsModel) Init() tea.Cmd { return nil }

func (m *suggestionsModel) current() []suggest.Suggestion {
	return suggest.Analyze(m.ctx.Plan, m.ctx.State.DismissedSuggestions)
}

func (m *suggestionsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		items := m.current()
		if m.cursor >= len(items) {
			m.cursor = 0
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(items)-1 {
				m.cursor++
			}
		case "enter":
			if len(items) == 0 {
				return m, nil
			}
			s := items[m.cursor]
			if err := m.applySuggestion(s); err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			m.message = fmt.Sprintf("added to .syncignore: %s", s.Pattern)
			return m, refreshPlanCmd(m.ctx)
		case "x":
			if len(items) == 0 {
				return m, nil
			}
			s := items[m.cursor]
			m.ctx.State.DismissedSuggestions = append(m.ctx.State.DismissedSuggestions, s.Pattern)
			if err := state.Save(m.ctx.StateDir, m.ctx.State); err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			m.message = fmt.Sprintf("dismissed: %s", s.Pattern)
		}
	}
	return m, nil
}

func (m *suggestionsModel) applySuggestion(s suggest.Suggestion) error {
	switch s.Kind {
	case suggest.KindSyncignore:
		path := filepath.Join(m.ctx.RepoPath, ".syncignore")
		return appendSyncignore(path, s.Pattern)
	}
	return fmt.Errorf("unknown suggestion kind: %d", s.Kind)
}

func (m *suggestionsModel) View() string {
	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render(m.message) + "\n\n")
	}

	items := m.current()
	if len(items) == 0 {
		sb.WriteString(theme.Good.Render("no suggestions — nothing obvious to clean up"))
		if n := len(m.ctx.State.DismissedSuggestions); n > 0 {
			sb.WriteString("\n\n" + theme.Hint.Render(
				fmt.Sprintf("(%d suggestion(s) previously dismissed — edit state.json to reset)", n)))
		}
		return sb.String()
	}

	sb.WriteString(theme.Hint.Render(fmt.Sprintf(
		"%d suggestion(s) — based on your current sync plan.\n\n", len(items))))

	for i, s := range items {
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		fmt.Fprintf(&sb, "%s%s  %s\n", cursor, theme.Primary.Render(s.Pattern), theme.Hint.Render("— "+s.Reason))
		// Preview up to 3 sample paths
		for j, p := range s.Paths {
			if j >= 3 {
				fmt.Fprintf(&sb, "     %s\n", theme.Hint.Render(fmt.Sprintf("… %d more", len(s.Paths)-3)))
				break
			}
			fmt.Fprintf(&sb, "     %s\n", theme.Hint.Render(p))
		}
	}

	sb.WriteString("\n" +
		theme.Primary.Render("enter ") + "accept • " +
		theme.Primary.Render("x ") + "dismiss forever • " +
		theme.Hint.Render("↑↓ move • esc back"))
	return sb.String()
}

// countSuggestions returns how many non-dismissed suggestions are pending.
// Cheap — analyzes the cached plan only.
func countSuggestions(ctx *AppContext) int {
	return len(suggest.Analyze(ctx.Plan, ctx.State.DismissedSuggestions))
}

