package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

// onboardingModel is the first-run flow that chains the steps a new user
// has to complete before ccsync is useful: identity → bootstrap →
// syncignore review → first-sync preview. Owns minimal UI itself; delegates
// bootstrap to the existing bootstrapWizardModel via switchTo for that
// middle stretch. The welcome + identity + syncignore steps live here.
//
// The user can `s` (skip) at any step to jump to Home with defaults. Once
// the model reaches the end (by skip or by completing), state.OnboardingComplete
// flips so we don't nag on subsequent launches.
type onboardingModel struct {
	ctx  *AppContext
	step onboardingStep

	nameIn  textinput.Model
	emailIn textinput.Model
	focus   int // 0=name, 1=email

	err error
}

type onboardingStep int

const (
	onboardWelcome  onboardingStep = iota
	onboardIdentity                // author name + email
	onboardBootstrap               // hand off to bootstrapWizardModel
	onboardSyncignore              // show defaults, offer $EDITOR or skip
	onboardDone
)

func newOnboarding(ctx *AppContext) *onboardingModel {
	nameIn := textinput.New()
	nameIn.Placeholder = mustHostname()
	nameIn.CharLimit = 48
	nameIn.Width = 32

	emailIn := textinput.New()
	emailIn.Placeholder = "you@example.com"
	emailIn.CharLimit = 80
	emailIn.Width = 48

	// Pre-fill from existing state if the user has been here before (e.g.
	// came back after a bootstrap reset).
	if ctx.State.AuthorName != "" {
		nameIn.SetValue(ctx.State.AuthorName)
	}
	if ctx.State.AuthorEmail != "" {
		emailIn.SetValue(ctx.State.AuthorEmail)
	}

	return &onboardingModel{ctx: ctx, step: onboardWelcome, nameIn: nameIn, emailIn: emailIn}
}

func (m *onboardingModel) Title() string { return "Welcome to ccsync" }

func (m *onboardingModel) Init() tea.Cmd { return nil }

// CapturesEscape keeps esc from popping us back to Home mid-flow — the
// dedicated `s` skip gesture is how users bail out, so esc is a no-op
// during onboarding.
func (m *onboardingModel) CapturesEscape() bool { return true }

func (m *onboardingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case bootstrapDoneMsg:
		// The bootstrap wizard we delegated to finished. If it succeeded,
		// advance to syncignore review; on error, surface it and let the
		// user bail.
		if msg.err != nil {
			m.err = msg.err
			m.step = onboardDone
			return m, nil
		}
		if msg.st != nil {
			m.ctx.State = msg.st
		}
		m.step = onboardSyncignore
		return m, nil

	case editDoneMsg:
		// editor shelled for syncignore tweaks returned — regardless of
		// success, move on to the final step.
		m.step = onboardDone
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "s" && m.step != onboardIdentity {
			// `s` during a textinput step would just insert the letter —
			// only treat it as skip outside of input-driven steps.
			return m.finish()
		}
		switch m.step {
		case onboardWelcome:
			return m.updateWelcome(msg)
		case onboardIdentity:
			return m.updateIdentity(msg)
		case onboardSyncignore:
			return m.updateSyncignore(msg)
		case onboardDone:
			return m.finish()
		}
	}
	return m, nil
}

func (m *onboardingModel) updateWelcome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.step = onboardIdentity
		m.focus = 0
		m.nameIn.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m *onboardingModel) updateIdentity(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "down":
		if m.focus == 0 {
			m.focus = 1
			m.nameIn.Blur()
			m.emailIn.Focus()
			return m, textinput.Blink
		}
	case "shift+tab", "up":
		if m.focus == 1 {
			m.focus = 0
			m.emailIn.Blur()
			m.nameIn.Focus()
			return m, textinput.Blink
		}
	case "enter":
		// If on name and name is filled, advance to email. If on email,
		// commit + move on. Blank values accepted — we fall back to
		// hostname/hostname@ccsync.local on save.
		if m.focus == 0 {
			if strings.TrimSpace(m.nameIn.Value()) == "" {
				m.focus = 1
				m.nameIn.Blur()
				m.emailIn.Focus()
				return m, textinput.Blink
			}
			m.focus = 1
			m.nameIn.Blur()
			m.emailIn.Focus()
			return m, textinput.Blink
		}
		// focus == 1: commit identity, move to bootstrap.
		return m.commitIdentityAndBootstrap()
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.nameIn, cmd = m.nameIn.Update(msg)
	} else {
		m.emailIn, cmd = m.emailIn.Update(msg)
	}
	return m, cmd
}

func (m *onboardingModel) commitIdentityAndBootstrap() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.nameIn.Value())
	email := strings.TrimSpace(m.emailIn.Value())
	m.ctx.State.AuthorName = name
	m.ctx.State.AuthorEmail = email
	if err := state.Save(m.ctx.StateDir, m.ctx.State); err != nil {
		m.err = err
		return m, nil
	}
	// Sync ctx's effective fields since AppContext.NewContext computed
	// them at startup from state + defaults.
	if name != "" {
		m.ctx.HostName = name
	}
	if email != "" {
		m.ctx.Email = email
	}

	m.step = onboardBootstrap
	// Push the existing bootstrapWizardModel. Its bootstrapDoneMsg will
	// come back to us because we're still on the stack below it.
	return m, switchTo(newBootstrapWizard(m.ctx))
}

func (m *onboardingModel) updateSyncignore(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "e":
		// Open the repo's .syncignore in $EDITOR. Reuses editConfigFileCmd
		// from settings.go — same flow the Settings screen uses.
		path := filepath.Join(m.ctx.RepoPath, ".syncignore")
		return m, editConfigFileCmd(path, false /*no yaml validation*/)
	case "enter", "k":
		// "keep defaults" — the defaults are already in the seeded file
		// after bootstrap, so there's nothing to do here but advance.
		m.step = onboardDone
		return m, nil
	}
	return m, nil
}

// finish persists OnboardingComplete and either hands off to SyncPreview
// (when a repo is bootstrapped) or pops to Home. Used by terminal keys
// and the skip gesture.
func (m *onboardingModel) finish() (tea.Model, tea.Cmd) {
	m.ctx.State.OnboardingComplete = true
	_ = state.Save(m.ctx.StateDir, m.ctx.State)

	if m.ctx.State.SyncRepoURL != "" {
		// We made it through bootstrap — push SyncPreview after popping
		// so the user immediately sees their first-sync preview.
		return m, tea.Sequence(popToRoot(), switchTo(newSyncPreview(m.ctx)))
	}
	// Skipped without bootstrapping — just go Home.
	return m, popToRoot()
}

func (m *onboardingModel) View() string {
	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	}

	switch m.step {
	case onboardWelcome:
		sb.WriteString(m.renderWelcome())
	case onboardIdentity:
		sb.WriteString(m.renderIdentity())
	case onboardBootstrap:
		// Delegated to the bootstrap wizard; it's above us on the stack
		// and AppModel routes Update/View to it. This frame is below —
		// rendered only during the brief transition window.
		sb.WriteString(theme.Hint.Render("walking you through repo bootstrap…"))
	case onboardSyncignore:
		sb.WriteString(m.renderSyncignore())
	case onboardDone:
		sb.WriteString(m.renderDone())
	}
	return sb.String()
}

func (m *onboardingModel) renderWelcome() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("ccsync — sync your Claude Code config across machines") + "\n\n")
	sb.WriteString(
		"  " + theme.Secondary.Render("what this is:") + "\n" +
			"  this tool keeps " + theme.Primary.Render("~/.claude/") + " and " + theme.Primary.Render("~/.claude.json") + " in sync\n" +
			"  across your machines via a " + theme.Primary.Render("git repo you own") + ".\n\n" +
			"  " + theme.Secondary.Render("what happens next:") + "\n" +
			"  you'll tell ccsync who you are (for git commits), point it\n" +
			"  at a repo (or create one), review the default ignore rules,\n" +
			"  and see a preview of the first sync before it touches anything.\n\n" +
			"  " + theme.Secondary.Render("takes about 2 minutes.") + "\n\n")
	sb.WriteString(theme.Primary.Render("enter ") + "start • " + theme.Hint.Render("s skip (use defaults later)"))
	return sb.String()
}

func (m *onboardingModel) renderIdentity() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("1 of 4 — identity") + "\n\n")
	sb.WriteString(theme.Hint.Render(
		"these go on every sync commit so you can tell which machine pushed what.\n" +
			"leave blank to use defaults (hostname / hostname@ccsync.local).") + "\n\n")

	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("author name: "), m.nameIn.View())
	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("author email:"), m.emailIn.View())
	sb.WriteString("\n" +
		theme.Primary.Render("tab/↓↑ ") + "switch field • " +
		theme.Primary.Render("enter ") + "next step")
	return sb.String()
}

func (m *onboardingModel) renderSyncignore() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("3 of 4 — ignore rules") + "\n\n")
	sb.WriteString(theme.Hint.Render(
		"ccsync shipped a sensible default .syncignore with your repo. it excludes\n" +
			"caches, session files, per-project state, plugins, and other machine-local\n" +
			"junk. you can tweak it now or come back to it in Settings later.") + "\n\n")
	sb.WriteString(
		"  " + theme.Primary.Render("e") + "  open .syncignore in $EDITOR now\n" +
			"  " + theme.Primary.Render("enter/k") + "  keep defaults (recommended)\n" +
			"  " + theme.Primary.Render("s") + "  skip and go home\n")
	return sb.String()
}

func (m *onboardingModel) renderDone() string {
	var sb strings.Builder
	sb.WriteString(theme.Good.Render("4 of 4 — all set ✓") + "\n\n")
	if m.ctx.State.SyncRepoURL != "" {
		sb.WriteString(theme.Hint.Render(
			"press any key to review your first-sync preview before it writes.\n" +
				"on a fresh work machine, try `p` for pull-only on that screen\n" +
				"so you don't push anything local up until you're ready.") + "\n")
	} else {
		sb.WriteString(theme.Hint.Render(
			"you skipped bootstrap — run `ccsync bootstrap --repo <URL>` when\n" +
				"you're ready, or open this wizard again from Home.") + "\n")
	}
	return sb.String()
}

// mustHostname returns os.Hostname() as a best-effort placeholder string;
// empty if the syscall failed.
func mustHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}
