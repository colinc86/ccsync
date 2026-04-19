package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/theme"
)

type homeModel struct {
	ctx     *AppContext
	cursor  int
	choices []homeChoice
}

type homeChoice struct {
	label   string
	enabled bool
	onEnter func() tea.Cmd
}

func newHome(ctx *AppContext) homeModel {
	h := homeModel{ctx: ctx}
	h.rebuildChoices()
	return h
}

func (m *homeModel) rebuildChoices() {
	bootstrapped := m.ctx.State.SyncRepoURL != ""
	m.choices = []homeChoice{
		{
			label:   "Sync now",
			enabled: bootstrapped,
			onEnter: func() tea.Cmd { return switchTo(newSyncPreview(m.ctx)) },
		},
		{
			label:   "History",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newSyncHistory(m.ctx)) },
		},
		{
			label:   "Profiles",
			enabled: bootstrapped,
			onEnter: func() tea.Cmd { return switchTo(newProfiles(m.ctx)) },
		},
		{
			label:   "Doctor",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newDoctorScreen(m.ctx)) },
		},
		{
			label:   "Settings",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newSettings(m.ctx)) },
		},
	}
}

func (m homeModel) Title() string { return "ccsync" }

func (m homeModel) Init() tea.Cmd { return nil }

func (m homeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter":
			c := m.choices[m.cursor]
			if c.enabled {
				return m, c.onEnter()
			}
		}
	}
	return m, nil
}

func (m homeModel) View() string {
	var sb strings.Builder

	bootstrapped := m.ctx.State.SyncRepoURL != ""
	profile := m.ctx.State.ActiveProfile
	if profile == "" {
		profile = "(none)"
	}

	sb.WriteString(fmt.Sprintf("host:    %s\n", theme.Secondary.Render(m.ctx.HostName)))
	sb.WriteString(fmt.Sprintf("profile: %s\n", theme.Secondary.Render(profile)))
	if bootstrapped {
		sb.WriteString(fmt.Sprintf("repo:    %s\n", theme.Hint.Render(m.ctx.State.SyncRepoURL)))
		last := m.ctx.State.LastSyncedSHA[profile]
		if last == "" {
			last = "never"
		} else if len(last) > 7 {
			last = last[:7]
		}
		sb.WriteString(fmt.Sprintf("last:    %s\n", theme.Hint.Render(last)))
	} else {
		sb.WriteString(theme.Warn.Render("no sync repo configured — run: ccsync bootstrap --repo <URL>") + "\n")
	}
	sb.WriteString("\n")

	for i, c := range m.choices {
		cursor := "  "
		line := c.label
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		if !c.enabled {
			line = theme.Hint.Render(line + " (unavailable)")
		}
		sb.WriteString(cursor + line + "\n")
	}
	sb.WriteString("\n" + theme.Hint.Render("↑↓ move • enter select"))

	return sb.String()
}
