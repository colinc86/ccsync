package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/gitx"
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

	// Legend chips — give each policy its color identity upfront so
	// the cell colors in the grid carry meaning.
	sb.WriteString(theme.Hint.Render("per-category review policies") + "\n")
	legend := []string{
		theme.ChipGood.Render("auto") + " " + theme.Hint.Render("sync silently"),
		theme.ChipWarn.Render("review") + " " + theme.Hint.Render("pause for allow/deny"),
		theme.ChipBad.Render("never") + " " + theme.Hint.Render("skip entirely"),
	}
	sb.WriteString(strings.Join(legend, "  ") + "\n\n")

	// Header row — secondary accents, right-padded to align with cells.
	fmt.Fprintf(&sb, "  %-22s  %-10s  %-10s\n",
		theme.Secondary.Bold(true).Render("category"),
		theme.Secondary.Bold(true).Render("push"),
		theme.Secondary.Bold(true).Render("pull"))

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
		sb.WriteString("\n" + theme.Bad.Render("save failed: "+gitx.Friendly(m.err)))
	}
	sb.WriteString("\n" + renderFooterBar([]footerKey{
		{cap: "enter", label: "cycle policy", primary: true},
		{cap: "space", label: "cycle"},
		{cap: "↑↓", label: "row"},
		{cap: "←→", label: "column"},
		{cap: "tab", label: "column"},
	}))
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
