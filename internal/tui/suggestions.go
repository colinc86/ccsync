package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/humanize"
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
		sb.WriteString(renderError(m.err) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render("✓ "+m.message) + "\n\n")
	}

	items := m.current()
	if len(items) == 0 {
		// Empty-state hero card — green "all clean" card so users
		// land on a satisfying "nothing to do" rather than a single
		// hint line. Tells them this is a good state, not an error.
		body := theme.Good.Bold(true).Render("✓ NO SUGGESTIONS") + "\n" +
			theme.Hint.Render("nothing obvious to clean up right now")
		if n := len(m.ctx.State.DismissedSuggestions); n > 0 {
			body += "\n" + theme.Subtle.Render(fmt.Sprintf(
				"(%s previously dismissed — edit state.json to reset)",
				humanize.Count(n, "suggestion")))
		}
		sb.WriteString(theme.CardClean.Width(56).Render(body))
		return sb.String()
	}

	// Stats chip — "3 suggestions" in warn pill since these are
	// pending actions the user should consider.
	sb.WriteString(theme.ChipWarn.Render(
		fmt.Sprintf("✎ %s", humanize.Count(len(items), "suggestion"))) + "  " +
		theme.Hint.Render("based on your current sync plan") + "\n\n")

	for i, s := range items {
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		// Pattern as a keycap-pill so the suggested rule reads as
		// something the user can picture adopting verbatim.
		fmt.Fprintf(&sb, "%s%s  %s\n", cursor,
			theme.KeycapMuted.Render(s.Pattern),
			theme.Hint.Render("— "+s.Reason))
		for j, p := range s.Paths {
			if j >= 3 {
				fmt.Fprintf(&sb, "     %s\n", theme.Hint.Render(fmt.Sprintf("… %d more", len(s.Paths)-3)))
				break
			}
			fmt.Fprintf(&sb, "     %s\n", theme.Subtle.Render("· "+p))
		}
	}

	sb.WriteString("\n" + renderFooterBar([]footerKey{
		{cap: "enter", label: "accept", primary: true},
		{cap: "x", label: "dismiss forever"},
		{cap: "↑↓", label: "move"},
	}))
	return sb.String()
}

// countSuggestions returns how many non-dismissed suggestions are pending.
// Cheap — analyzes the cached plan only.
func countSuggestions(ctx *AppContext) int {
	return len(suggest.Analyze(ctx.Plan, ctx.State.DismissedSuggestions))
}
