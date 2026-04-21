package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/bootstrap"
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
			// After a successful bootstrap, the most likely next thing the
			// user wants is to preview the first sync. Use tea.Sequence so
			// the pop lands BEFORE the push — tea.Batch is order-
			// independent and would sometimes show a ghost-wizard frame
			// under the new SyncPreview during the transition.
			if m.err != nil || m.done == nil {
				return m, popToRoot()
			}
			return m, tea.Sequence(popToRoot(), switchTo(newSyncPreview(m.ctx)))
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
		if m.sourceCursor > 0 {
			m.sourceCursor--
		}
	case "down", "j":
		if m.sourceCursor < 2 {
			m.sourceCursor++
		}
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
	case "b":
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
		return m.spin.View() + " " + theme.Hint.Render("bootstrapping — cloning and seeding repo…")
	}

	var sb strings.Builder
	switch m.step {
	case stepSource:
		sb.WriteString(theme.Hint.Render("where should your sync repo live?") + "\n\n")
		for i, c := range m.sourceChoices() {
			cursor := "  "
			if m.sourceCursor == i {
				cursor = theme.Primary.Render("▸ ")
			}
			sb.WriteString(cursor + c + "\n")
		}
		sb.WriteString("\n" + theme.Hint.Render("↑↓ move • enter select"))

	case stepURL:
		label := "repo URL"
		switch m.sourceKind {
		case bootstrap.SourceGHCreate:
			label = "new repo name"
		case bootstrap.SourceLocalBare:
			label = "local bare repo path"
		}
		sb.WriteString(theme.Secondary.Render(label+":") + " " + m.urlInput.View())
		sb.WriteString("\n\n" + theme.Hint.Render("enter next • esc back"))

	case stepConfirm:
		sb.WriteString(theme.Heading.Render("confirm") + "\n\n")
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("source:"), m.sourceSummary())
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("target:"), m.urlInput.Value())
		fmt.Fprintf(&sb, "  %s  ssh (auto-detect ~/.ssh/id_*)\n", theme.Secondary.Render("auth:"))
		sb.WriteString("\n" + theme.Primary.Render("enter ") + "apply • " +
			theme.Hint.Render("b edit URL • esc cancel"))

	case stepDone:
		if m.err != nil {
			sb.WriteString(theme.Bad.Render("bootstrap failed") + "\n\n")
			sb.WriteString(m.err.Error() + "\n")
			sb.WriteString("\n" + theme.Hint.Render("press any key to return"))
			return sb.String()
		}
		sb.WriteString(theme.Good.Render("bootstrapped ✓") + "\n\n")
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("repo:"), m.done.SyncRepoURL)
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("profile:"), m.done.ActiveProfile)
		sb.WriteString("\n" +
			theme.Hint.Render("nothing has synced yet — press any key to preview what would change"))
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
