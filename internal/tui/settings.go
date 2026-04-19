package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/theme"
)

type settingsModel struct {
	ctx *AppContext
}

func newSettings(ctx *AppContext) *settingsModel { return &settingsModel{ctx: ctx} }

func (m *settingsModel) Title() string          { return "Settings" }
func (m *settingsModel) Init() tea.Cmd          { return nil }
func (m *settingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (m *settingsModel) View() string {
	var sb strings.Builder
	write := func(k, v string) {
		if v == "" {
			v = theme.Hint.Render("(unset)")
		}
		sb.WriteString(fmt.Sprintf("  %s  %s\n", theme.Secondary.Render(k+":"), v))
	}

	sb.WriteString(theme.Heading.Render("identity") + "\n")
	write("host-uuid", m.ctx.State.HostUUID)
	write("hostname", m.ctx.HostName)
	write("email", m.ctx.Email)

	sb.WriteString("\n" + theme.Heading.Render("sync repo") + "\n")
	write("url", m.ctx.State.SyncRepoURL)
	write("auth", string(m.ctx.State.Auth))
	write("active profile", m.ctx.State.ActiveProfile)

	sb.WriteString("\n" + theme.Heading.Render("paths") + "\n")
	write("~/.claude", m.ctx.ClaudeDir)
	write("~/.claude.json", m.ctx.ClaudeJSON)
	write("state dir", m.ctx.StateDir)

	sb.WriteString("\n" + theme.Heading.Render("profiles (from ccsync.yaml)") + "\n")
	for name, p := range m.ctx.Config.Profiles {
		desc := p.Description
		if desc == "" {
			desc = "(no description)"
		}
		sb.WriteString(fmt.Sprintf("  %s  %s\n", theme.Secondary.Render(name), theme.Hint.Render(desc)))
	}

	sb.WriteString("\n" + theme.Hint.Render("v1: settings are read-only in the TUI"))
	return sb.String()
}
