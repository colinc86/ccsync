package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	updateStepOffer               // latest > current
	updateStepUpToDate
	updateStepHomebrew // installed via brew — defer to brew upgrade
	updateStepInstalling
	updateStepDone
	updateStepRestarting // post-install, brief hold then tea.Quit + re-exec
)

// restartDelay is the window the user sees the "updated" card before
// the TUI quits and main() re-execs the fresh binary. Short enough
// that auto-restart feels snappy, long enough that the success
// message actually registers visually.
const restartDelay = 900 * time.Millisecond

// restartTickMsg fires after restartDelay elapses on the Done step
// to trigger the tea.Quit that hands control back to main() for
// the syscall.Exec into the new binary.
type restartTickMsg struct{}

type updateScreenCheckMsg struct {
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

// IsTerminal marks the post-install / up-to-date / done states as
// terminal — the "press any key to return" copy already says the
// next hop is Home, so ESC should honour that instead of dropping
// back to Settings one step at a time. Restarting is NOT terminal
// because the tea.Tick in flight will drive the quit itself; if a
// user mashed ESC mid-restart we'd drop them to Home on a binary
// that's about to re-exec, which is momentarily confusing.
func (m *updateScreenModel) IsTerminal() bool {
	return m.step == updateStepUpToDate ||
		m.step == updateStepHomebrew ||
		m.step == updateStepDone
}

func checkLatestCmd() tea.Cmd {
	return func() tea.Msg {
		tag, err := updater.LatestTag()
		return updateScreenCheckMsg{latest: tag, err: err}
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

	case updateScreenCheckMsg:
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
			// Stage the restart: tell main() which binary to exec
			// after the TUI shuts down, then schedule the tick
			// that triggers the shutdown. A brief hold on the
			// success card first so the user sees confirmation
			// before the screen clears.
			m.ctx.RestartBinaryPath = m.exePath
			m.step = updateStepRestarting
			return m, tea.Tick(restartDelay, func(time.Time) tea.Msg { return restartTickMsg{} })
		}
		m.step = updateStepDone
		return m, nil

	case restartTickMsg:
		// tea.Quit returns control to main.main; it checks
		// ctx.RestartBinaryPath and re-execs the new binary there.
		return m, tea.Quit

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
			// Terminal "press any key to return" states — go to Home.
			return m, popToRoot()
		case updateStepRestarting:
			// Ignore input mid-restart. The tea.Tick is imminent
			// and any re-routing would just race it.
			return m, nil
		}
	}
	return m, nil
}

func (m *updateScreenModel) View() string {
	var sb strings.Builder

	if m.err != nil {
		sb.WriteString(renderError(m.err) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render("✓ "+m.message) + "\n\n")
	}

	// Version strip — compact aligned rows showing installed, latest,
	// and the binary we'd overwrite. Muted dots prefix each row so
	// the block reads as a metadata panel, matching Home's detail
	// strip.
	fmt.Fprintf(&sb, " %s %-10s %s\n",
		theme.Rule.Render("·"), theme.Hint.Render("installed"), theme.Secondary.Render(m.current))
	if m.latest != "" {
		fmt.Fprintf(&sb, " %s %-10s %s\n",
			theme.Rule.Render("·"), theme.Hint.Render("latest"), theme.Secondary.Render(m.latest))
	}
	if m.exePath != "" {
		fmt.Fprintf(&sb, " %s %-10s %s\n",
			theme.Rule.Render("·"), theme.Hint.Render("binary"), theme.Hint.Render(m.exePath))
	}
	sb.WriteString("\n")

	switch m.step {
	case updateStepChecking:
		sb.WriteString(m.spin.View() + " " + theme.Hint.Render("checking github for latest release…"))
	case updateStepUpToDate:
		body := theme.Good.Bold(true).Render("✓ UP TO DATE") + "\n" +
			theme.Hint.Render("you're on the newest release")
		sb.WriteString(theme.CardClean.Width(56).Render(body) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "any key", label: "return"},
		}))
	case updateStepHomebrew:
		body := theme.Warn.Bold(true).Render("↗ HOMEBREW INSTALL") + "\n" +
			theme.Hint.Render("this binary was installed via Homebrew.\nrun: brew upgrade ccsync")
		sb.WriteString(theme.CardPending.Width(56).Render(body) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "any key", label: "return"},
		}))
	case updateStepOffer:
		body := theme.Warn.Bold(true).Render(fmt.Sprintf("↗ UPDATE AVAILABLE  %s → %s", m.current, m.latest)) + "\n" +
			theme.Hint.Render("downloads from GitHub, atomic-replaces this binary")
		sb.WriteString(theme.CardPending.Width(60).Render(body) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "y", label: "install now"},
			{cap: "enter", label: "install"},
			{cap: "n", label: "cancel"},
			{cap: "esc", label: "cancel"},
		}))
	case updateStepInstalling:
		sb.WriteString(m.spin.View() + " " + theme.Hint.Render("downloading and replacing binary…"))
	case updateStepRestarting:
		// Explicit "restarting..." card so the user understands
		// why the TUI is about to flash — otherwise an immediate
		// self-exec looks like a crash.
		body := theme.Good.Bold(true).Render("✓ UPDATED") + "\n" +
			theme.Hint.Render("restarting with the new binary…")
		sb.WriteString(theme.CardClean.Width(56).Render(body))
	case updateStepDone:
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "any key", label: "return"},
		}))
	}
	return sb.String()
}
