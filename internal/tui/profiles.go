package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

type profilesModel struct {
	ctx      *AppContext
	cursor   int
	profiles []string
	err      error
}

func newProfiles(ctx *AppContext) *profilesModel {
	names := make([]string, 0, len(ctx.Config.Profiles))
	for k := range ctx.Config.Profiles {
		names = append(names, k)
	}
	sort.Strings(names)

	active := ctx.State.ActiveProfile
	startIdx := 0
	for i, n := range names {
		if n == active {
			startIdx = i
			break
		}
	}
	return &profilesModel{ctx: ctx, profiles: names, cursor: startIdx}
}

func (m *profilesModel) Title() string { return "Profiles" }
func (m *profilesModel) Init() tea.Cmd { return nil }

func (m *profilesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.profiles)-1 {
				m.cursor++
			}
		case "enter":
			target := m.profiles[m.cursor]
			m.ctx.State.ActiveProfile = target
			if err := state.Save(m.ctx.StateDir, m.ctx.State); err != nil {
				m.err = err
				return m, nil
			}
			return m, popScreen()
		}
	}
	return m, nil
}

func (m *profilesModel) View() string {
	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	}
	if len(m.profiles) == 0 {
		sb.WriteString(theme.Hint.Render("no profiles configured"))
		return sb.String()
	}
	for i, name := range m.profiles {
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		marker := ""
		if name == m.ctx.State.ActiveProfile {
			marker = theme.Good.Render(" (active)")
		}
		desc := m.ctx.Config.Profiles[name].Description
		line := name + marker
		if desc != "" {
			line += "  " + theme.Hint.Render("— "+desc)
		}
		sb.WriteString(cursor + line + "\n")
	}
	sb.WriteString("\n" + theme.Hint.Render("enter switches to selected profile"))
	return sb.String()
}
