package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/bootstrap"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

// bootstrapWizardModel walks a fresh user through picking a sync repo and
// writing state.json. v1 only supports SSH auth from the TUI; HTTPS users
// can fall back to `ccsync bootstrap --auth https`. The profile name is
// always "default" here — users who want a second profile create it from
// the Profiles screen after bootstrap.
type bootstrapWizardModel struct {
	ctx  *AppContext
	step wizStep

	sourceCursor int
	sourceKind   bootstrap.Source

	urlInput textinput.Model
	spin     spinner.Model

	running bool
	err     error
	done    *state.State
}

type wizStep int

const (
	stepSource wizStep = iota
	stepURL
	stepConfirm
	stepDone
)

type bootstrapDoneMsg struct {
	st  *state.State
	err error
}

func newBootstrapWizard(ctx *AppContext) *bootstrapWizardModel {
	urlInput := textinput.New()
	urlInput.Placeholder = "git@github.com:user/claude-settings.git"
	urlInput.CharLimit = 256
	urlInput.Width = 48
	urlInput.Focus()

	return &bootstrapWizardModel{
		ctx:      ctx,
		urlInput: urlInput,
		spin:     newSpinner(),
	}
}

func (m *bootstrapWizardModel) Title() string { return "Bootstrap — first-run wizard" }
func (m *bootstrapWizardModel) Init() tea.Cmd { return textinput.Blink }

// IsTerminal marks the final step as terminal — the wizard has either
// finished bootstrapping the repo or reported an error, and there's
// no meaningful "back one step" destination (the earlier steps
// already wrote state and can't be safely re-visited).
func (m *bootstrapWizardModel) IsTerminal() bool { return m.step == stepDone }

func (m *bootstrapWizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case bootstrapDoneMsg:
		m.running = false
		m.err = msg.err
		m.done = msg.st
		m.step = stepDone
		if m.done != nil {
			// refresh context state so Home shows bootstrapped status, and
			// kick off the first plan fetch now that we have a repo.
			m.ctx.State = m.done
			return m, refreshPlanCmd(m.ctx)
		}
		return m, nil

	case tea.KeyMsg:
		if m.running {
			return m, nil
		}
		switch m.step {
		case stepSource:
			return m.updateSource(msg)
		case stepURL:
			return m.updateURL(msg)
		case stepConfirm:
			return m.updateConfirm(msg)
		case stepDone:
			// After a successful bootstrap, hand off to the profile
			// picker — it'll decide whether this machine joins an
			// existing profile or creates a new one. On a fresh
			// single-profile repo the picker auto-advances without a
			// keystroke, so machine #1 setup stays frictionless.
			//
			// On error, just pop back to Home so the user can retry or
			// read the message.
			if m.err != nil || m.done == nil {
				return m, popToRoot()
			}
			return m, tea.Sequence(popToRoot(), switchTo(newProfilePickerScreen(m.ctx)))
		}
	}
	return m, nil
}

func (m *bootstrapWizardModel) sourceChoices() []string {
	return []string{
		"Use existing repo URL",
		"Create a new private repo via gh",
		"Use a local bare repo path",
	}
}

func (m *bootstrapWizardModel) updateSource(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.sourceCursor = wrapCursor(m.sourceCursor, len(m.sourceChoices()), -1)
	case "down", "j":
		m.sourceCursor = wrapCursor(m.sourceCursor, len(m.sourceChoices()), +1)
	case "enter":
		switch m.sourceCursor {
		case 0:
			m.sourceKind = bootstrap.SourceExisting
			m.urlInput.Placeholder = "git@github.com:user/claude-settings.git"
		case 1:
			m.sourceKind = bootstrap.SourceGHCreate
			m.urlInput.Placeholder = "claude-settings"
		case 2:
			m.sourceKind = bootstrap.SourceLocalBare
			m.urlInput.Placeholder = "/path/to/bare.git"
		}
		m.step = stepURL
		m.urlInput.SetValue("")
		m.urlInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m *bootstrapWizardModel) updateURL(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if strings.TrimSpace(m.urlInput.Value()) == "" {
			return m, nil
		}
		m.step = stepConfirm
		m.urlInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.urlInput, cmd = m.urlInput.Update(msg)
	return m, cmd
}

func (m *bootstrapWizardModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.running = true
		return m, tea.Batch(
			runBootstrap(m.ctx, m.sourceKind, m.urlInput.Value(), "default"),
			m.spin.Tick,
		)
	case "b", "esc":
		// esc mirrors the documented "b to go back" so users don't
		// need to learn a screen-specific key to back out. Pre-fix
		// only "b" worked; esc silently no-op'd even though
		// every other TUI screen treats esc as back.
		m.step = stepURL
		m.urlInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func runBootstrap(ctx *AppContext, source bootstrap.Source, urlOrName, profile string) tea.Cmd {
	return func() tea.Msg {
		in := bootstrap.Inputs{
			Source:   source,
			Profile:  profile,
			StateDir: ctx.StateDir,
			Auth:     state.AuthSSH,
		}
		switch source {
		case bootstrap.SourceGHCreate:
			in.RepoName = urlOrName
		default:
			in.RepoURL = urlOrName
		}
		st, err := bootstrap.Run(context.Background(), in)
		return bootstrapDoneMsg{st: st, err: err}
	}
}

func (m *bootstrapWizardModel) View() string {
	if m.running {
		body := theme.Warn.Bold(true).Render("◌ BOOTSTRAPPING") + "\n" +
			theme.Hint.Render("cloning the repo and seeding defaults — one moment…")
		return theme.CardPending.Width(56).Render(body)
	}

	var sb strings.Builder
	switch m.step {
	case stepSource:
		sb.WriteString(theme.Heading.Render("where should your sync repo live?") + "\n\n")
		for i, c := range m.sourceChoices() {
			cursor := "  "
			if m.sourceCursor == i {
				cursor = theme.Primary.Render("▸ ")
			}
			sb.WriteString(cursor + c + "\n")
		}
		sb.WriteString("\n" + renderFooterBar([]footerKey{
			{cap: "enter", label: "select"},
			{cap: "↑↓", label: "move"},
		}))

	case stepURL:
		label := "repo URL"
		switch m.sourceKind {
		case bootstrap.SourceGHCreate:
			label = "new repo name"
		case bootstrap.SourceLocalBare:
			label = "local bare repo path"
		}
		sb.WriteString(theme.Heading.Render(label) + "\n\n")
		sb.WriteString("  " + m.urlInput.View() + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "enter", label: "next"},
			{cap: "esc", label: "back"},
		}))

	case stepConfirm:
		// Confirmation card — neutral-bordered so it reads as "about
		// to commit" without the pending/warm weight (nothing's gone
		// wrong, user just needs to approve).
		var body strings.Builder
		body.WriteString(theme.Primary.Render("ready to bootstrap") + "\n\n")
		fmt.Fprintf(&body, " %s %-8s %s\n",
			theme.Rule.Render("·"), theme.Hint.Render("source"), theme.Secondary.Render(m.sourceSummary()))
		fmt.Fprintf(&body, " %s %-8s %s\n",
			theme.Rule.Render("·"), theme.Hint.Render("target"), theme.Secondary.Render(m.urlInput.Value()))
		fmt.Fprintf(&body, " %s %-8s %s\n",
			theme.Rule.Render("·"), theme.Hint.Render("auth"), theme.Secondary.Render("ssh (auto-detect ~/.ssh/id_*)"))
		sb.WriteString(theme.CardNeutral.Width(60).Render(body.String()) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "enter", label: "apply"},
			{cap: "b", label: "edit URL"},
			{cap: "esc", label: "cancel"},
		}))

	case stepDone:
		if m.err != nil {
			body := theme.Bad.Render("! BOOTSTRAP FAILED") + "\n" +
				theme.Subtle.Render(gitx.Friendly(m.err))
			sb.WriteString(theme.CardConflict.Width(60).Render(body) + "\n\n")
			sb.WriteString(renderFooterBar([]footerKey{
				{cap: "any key", label: "return"},
			}))
			return sb.String()
		}
		var body strings.Builder
		body.WriteString(theme.Good.Bold(true).Render("✓ BOOTSTRAPPED") + "\n\n")
		fmt.Fprintf(&body, " %s %-8s %s\n",
			theme.Rule.Render("·"), theme.Hint.Render("repo"), theme.Secondary.Render(m.done.SyncRepoURL))
		fmt.Fprintf(&body, " %s %-8s %s\n",
			theme.Rule.Render("·"), theme.Hint.Render("profile"), theme.Secondary.Render(m.done.ActiveProfile))
		body.WriteString("\n" + theme.Hint.Render("nothing has synced yet — next step previews what would change"))
		sb.WriteString(theme.CardClean.Width(60).Render(body.String()) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "any key", label: "continue"},
		}))
	}
	return sb.String()
}

func (m *bootstrapWizardModel) sourceSummary() string {
	switch m.sourceKind {
	case bootstrap.SourceExisting:
		return "clone existing"
	case bootstrap.SourceGHCreate:
		return "create via gh CLI"
	case bootstrap.SourceLocalBare:
		return "local bare"
	}
	return "?"
}
