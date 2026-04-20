package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/theme"
	"github.com/colinc86/ccsync/internal/updater"
)

// updateScreenModel walks the user through checking for and (optionally)
// installing a new ccsync release. State flow: checking → offer (current +
// latest, ask to install) or up-to-date → installing → done. Keeps the
// long-ish network operations visible via a spinner so nothing looks hung.
type updateScreenModel struct {
	ctx     *AppContext
	step    updateStep
	spin    spinner.Model
	latest  string
	current string
	exePath string
	brew    bool
	err     error
	message string
}

type updateStep int

const (
	updateStepChecking updateStep = iota
	updateStepOffer              // latest > current
	updateStepUpToDate
	updateStepHomebrew           // installed via brew — defer to brew upgrade
	updateStepInstalling
	updateStepDone
)

type updateCheckDoneMsg struct {
	latest string
	err    error
}

type updateInstallDoneMsg struct {
	err error
}

func newUpdateScreen(ctx *AppContext) *updateScreenModel {
	m := &updateScreenModel{
		ctx:     ctx,
		step:    updateStepChecking,
		spin:    newSpinner(),
		current: "v" + updater.CurrentVersion(),
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		m.exePath = exe
		m.brew = updater.IsHomebrew(exe)
	}
	return m
}

func (m *updateScreenModel) Title() string { return "Update" }
func (m *updateScreenModel) Init() tea.Cmd {
	return tea.Batch(checkLatestCmd(), m.spin.Tick)
}
func (m *updateScreenModel) CapturesEscape() bool {
	// Block esc-pop while a network op is running.
	return m.step == updateStepChecking || m.step == updateStepInstalling
}

func checkLatestCmd() tea.Cmd {
	return func() tea.Msg {
		tag, err := updater.LatestTag()
		return updateCheckDoneMsg{latest: tag, err: err}
	}
}

func (m *updateScreenModel) installCmd() tea.Cmd {
	target := m.exePath
	tag := m.latest
	return func() tea.Msg {
		if err := updater.InstallRelease(tag, target); err != nil {
			return updateInstallDoneMsg{err: err}
		}
		return updateInstallDoneMsg{}
	}
}

func (m *updateScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.step != updateStepChecking && m.step != updateStepInstalling {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case updateCheckDoneMsg:
		m.err = msg.err
		m.latest = msg.latest
		switch {
		case msg.err != nil:
			m.step = updateStepDone
		case m.latest == m.current:
			m.step = updateStepUpToDate
		case m.brew:
			m.step = updateStepHomebrew
		default:
			m.step = updateStepOffer
		}
		return m, nil

	case updateInstallDoneMsg:
		m.err = msg.err
		if msg.err == nil {
			m.message = fmt.Sprintf("updated: %s → %s", m.current, m.latest)
		}
		m.step = updateStepDone
		return m, nil

	case tea.KeyMsg:
		switch m.step {
		case updateStepOffer:
			switch msg.String() {
			case "y", "enter":
				m.step = updateStepInstalling
				return m, tea.Batch(m.installCmd(), m.spin.Tick)
			case "n", "esc":
				return m, popScreen()
			}
		case updateStepUpToDate, updateStepHomebrew, updateStepDone:
			return m, popScreen()
		}
	}
	return m, nil
}

func (m *updateScreenModel) View() string {
	var sb strings.Builder

	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render(m.message) + "\n\n")
	}

	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("installed:"), m.current)
	if m.latest != "" {
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("latest:   "), m.latest)
	}
	if m.exePath != "" {
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("binary:   "), theme.Hint.Render(m.exePath))
	}
	sb.WriteString("\n")

	switch m.step {
	case updateStepChecking:
		sb.WriteString(m.spin.View() + " " + theme.Hint.Render("checking github for latest release…"))
	case updateStepUpToDate:
		sb.WriteString(theme.Good.Render("✓ up to date"))
		sb.WriteString("\n\n" + theme.Hint.Render("press any key to return"))
	case updateStepHomebrew:
		sb.WriteString(theme.Warn.Render("installed via Homebrew — run: brew upgrade ccsync"))
		sb.WriteString("\n\n" + theme.Hint.Render("press any key to return"))
	case updateStepOffer:
		fmt.Fprintf(&sb, theme.Warn.Render("update available: %s → %s")+"\n\n", m.current, m.latest)
		sb.WriteString(
			theme.Primary.Render("y/enter ") + "install now • " +
				theme.Hint.Render("n / esc cancel"))
	case updateStepInstalling:
		sb.WriteString(m.spin.View() + " " + theme.Hint.Render("downloading and replacing binary…"))
	case updateStepDone:
		sb.WriteString(theme.Hint.Render("press any key to return"))
	}
	return sb.String()
}
