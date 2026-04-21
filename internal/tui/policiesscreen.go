package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

// policiesScreenModel is the per-category review-policy matrix editor.
// Rows are categories (agents, skills, …); columns are push / pull.
// Each cell cycles auto → review → never → auto on enter/space. The
// cursor moves with arrows; tab flips between the push and pull columns
// on the current row.
type policiesScreenModel struct {
	ctx    *AppContext
	row    int // row index into category.All()
	col    int // 0 = push, 1 = pull
	saving bool
	err    error
}

func newPoliciesScreen(ctx *AppContext) *policiesScreenModel {
	return &policiesScreenModel{ctx: ctx}
}

func (m *policiesScreenModel) Title() string { return "Sync review policies" }

func (m *policiesScreenModel) Init() tea.Cmd { return nil }

func (m *policiesScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cats := category.All()
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.row > 0 {
				m.row--
			}
		case "down", "j":
			if m.row < len(cats)-1 {
				m.row++
			}
		case "left", "h":
			if m.col > 0 {
				m.col--
			}
		case "right", "l", "tab":
			if m.col < 1 {
				m.col++
			}
		case "enter", " ":
			m.cyclePolicy()
			return m, m.save()
		}
	case policiesSavedMsg:
		m.saving = false
		m.err = msg.err
	}
	return m, nil
}

// cyclePolicy rotates the current cell's policy through
// auto → review → never → auto.
func (m *policiesScreenModel) cyclePolicy() {
	cats := category.All()
	cat := cats[m.row]
	dir := state.DirPush
	if m.col == 1 {
		dir = state.DirPull
	}
	cur := m.ctx.State.PolicyFor(cat, dir)
	next := nextPolicy(cur)
	m.ctx.State.SetPolicy(cat, dir, next)
}

func nextPolicy(cur string) string {
	switch cur {
	case state.PolicyAuto:
		return state.PolicyReview
	case state.PolicyReview:
		return state.PolicyNever
	case state.PolicyNever:
		return state.PolicyAuto
	}
	return state.PolicyAuto
}

type policiesSavedMsg struct{ err error }

func (m *policiesScreenModel) save() tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		return policiesSavedMsg{err: state.Save(ctx.StateDir, ctx.State)}
	}
}

func (m *policiesScreenModel) View() string {
	var sb strings.Builder
	sb.WriteString(theme.Hint.Render(
		"Per-category review policies. 'auto' syncs silently. 'review' pauses\n"+
			"for manual allow/deny before each push or pull. 'never' skips entirely.") + "\n\n")

	// Header
	fmt.Fprintf(&sb, "  %-22s  %-10s  %-10s\n",
		theme.Secondary.Render("category"),
		theme.Secondary.Render("push"),
		theme.Secondary.Render("pull"))

	for i, cat := range category.All() {
		cursor := "  "
		if i == m.row {
			cursor = theme.Primary.Render("▸ ")
		}
		pushCell := cellText(m.ctx.State.PolicyFor(cat, state.DirPush), i == m.row && m.col == 0)
		pullCell := cellText(m.ctx.State.PolicyFor(cat, state.DirPull), i == m.row && m.col == 1)
		fmt.Fprintf(&sb, "%s%-22s  %s  %s\n", cursor, category.Label(cat), pushCell, pullCell)
	}

	if m.err != nil {
		sb.WriteString("\n" + theme.Bad.Render("save failed: "+m.err.Error()))
	}
	sb.WriteString("\n" + theme.Hint.Render("↑↓ row · ←→/tab column · enter/space cycle · esc back"))
	return sb.String()
}

// cellText renders one (cat, dir) cell with appropriate highlight +
// color for the current policy.
func cellText(policy string, focused bool) string {
	label := fmt.Sprintf("%-10s", policy)
	switch policy {
	case state.PolicyAuto:
		label = theme.Good.Render(label)
	case state.PolicyReview:
		label = theme.Warn.Render(label)
	case state.PolicyNever:
		label = theme.Bad.Render(label)
	}
	if focused {
		return theme.Primary.Render("[") + label + theme.Primary.Render("]")
	}
	return " " + label + " "
}
