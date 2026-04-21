package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

// onboardingModel is the first-run flow: Welcome → Bootstrap (delegated to
// bootstrapWizardModel) → Done. Identity is inferred from global git
// config (falling back to hostname in app.go's NewContext), so there's no
// "tell us your name" step. The default .syncignore is good enough out of
// the box — advanced users can edit it from Settings later.
//
// `s` skips to the end at any point before the delegated bootstrap takes
// over. Once the user reaches the end (whether by completion or skip),
// state.OnboardingComplete flips so the wizard doesn't appear again.
type onboardingModel struct {
	ctx  *AppContext
	step onboardingStep
	err  error
}

type onboardingStep int

const (
	onboardWelcome   onboardingStep = iota
	onboardPolicy                   // pick auto-sync vs review-each
	onboardBootstrap                // hand off to bootstrapWizardModel
	onboardDone
)

func newOnboarding(ctx *AppContext) *onboardingModel {
	return &onboardingModel{ctx: ctx, step: onboardWelcome}
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
		// The bootstrap wizard we delegated to finished. On success we
		// advance to done; on error, surface it and let the user bail.
		if msg.err != nil {
			m.err = msg.err
			m.step = onboardDone
			return m, nil
		}
		if msg.st != nil {
			m.ctx.State = msg.st
		}
		m.step = onboardDone
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "s" {
			return m.finish()
		}
		switch m.step {
		case onboardWelcome:
			return m.updateWelcome(msg)
		case onboardPolicy:
			return m.updatePolicy(msg)
		case onboardDone:
			return m.finish()
		}
	}
	return m, nil
}

func (m *onboardingModel) updateWelcome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.step = onboardPolicy
		return m, nil
	}
	return m, nil
}

// updatePolicy handles the three-choice "how much control?" step.
// '1' (auto) is the default / lowest friction. '2' (review pushes)
// sets push=review for the user-content categories and leaves pulls
// on auto — matches the "I want to review what LEAVES this machine"
// mental model. '3' (review everything) sets push AND pull to review
// across every user-content category.
func (m *onboardingModel) updatePolicy(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "1", "enter":
		// Auto-sync everything (default). Nothing to set; empty policy
		// resolves to auto via PolicyFor.
	case "2":
		applyReviewPreset(m.ctx.State, state.DirPush)
	case "3":
		applyReviewPreset(m.ctx.State, state.DirPush)
		applyReviewPreset(m.ctx.State, state.DirPull)
	default:
		return m, nil
	}
	_ = state.Save(m.ctx.StateDir, m.ctx.State)
	m.step = onboardBootstrap
	return m, switchTo(newBootstrapWizard(m.ctx))
}

// applyReviewPreset flips the named categories to policy=review for
// one direction. The list covers the user-content categories where
// "review each" actually benefits the user; cache-ish categories
// (other) stay on auto because the user has no mental model for them.
func applyReviewPreset(st *state.State, dir state.Direction) {
	for _, cat := range []string{
		category.Agents,
		category.Skills,
		category.Commands,
		category.Memory,
		category.MCPServers,
		category.ClaudeMD,
		category.GeneralSettings,
	} {
		st.SetPolicy(cat, dir, state.PolicyReview)
	}
}

// finish persists OnboardingComplete and either hands off to SyncPreview
// (when a repo is bootstrapped) or pops to Home. Used by terminal keys
// and the skip gesture.
func (m *onboardingModel) finish() (tea.Model, tea.Cmd) {
	m.ctx.State.OnboardingComplete = true
	_ = state.Save(m.ctx.StateDir, m.ctx.State)

	if m.ctx.State.SyncRepoURL != "" {
		// Under auto mode, SyncPreview auto-applies when clean — so the
		// user lands on Home showing "✓ in sync" almost immediately.
		// Under manual mode they see the preview and choose.
		return m, tea.Sequence(popToRoot(), switchTo(newSyncPreview(m.ctx)))
	}
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
	case onboardPolicy:
		sb.WriteString(m.renderPolicy())
	case onboardBootstrap:
		// Delegated to the bootstrap wizard; rendered only during the
		// transition window before it pushes onto the stack above us.
		sb.WriteString(theme.Hint.Render("walking you through repo setup…"))
	case onboardDone:
		sb.WriteString(m.renderDone())
	}
	return sb.String()
}

func (m *onboardingModel) renderPolicy() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("how hands-on do you want sync to be?") + "\n\n")
	sb.WriteString(theme.Hint.Render(
		"ccsync can sync silently in the background or pause on every push/pull\n"+
			"to let you review each agent, skill, command, MCP server, etc. You\n"+
			"can tweak per-category policies anytime from Settings → review policies.") + "\n\n")

	fmt.Fprintf(&sb, "  %s  auto-sync everything %s\n",
		theme.Primary.Render("1"),
		theme.Hint.Render("(default — install, sync, forget)"))
	fmt.Fprintf(&sb, "  %s  review each push before it leaves this machine %s\n",
		theme.Primary.Render("2"),
		theme.Hint.Render("(pulls stay silent)"))
	fmt.Fprintf(&sb, "  %s  review every push AND pull %s\n",
		theme.Primary.Render("3"),
		theme.Hint.Render("(fully hands-on)"))
	sb.WriteString("\n" + theme.Hint.Render("1/2/3 choose • enter = 1 (auto) • s skip"))
	return sb.String()
}

func (m *onboardingModel) renderWelcome() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("ccsync — sync your Claude Code config across machines") + "\n\n")
	sb.WriteString(
		"  " + theme.Secondary.Render("what this is:") + "\n" +
			"  keeps " + theme.Primary.Render("~/.claude/") + " and " + theme.Primary.Render("~/.claude.json") +
			" in sync across your machines via a " + theme.Primary.Render("git repo you own") + ".\n\n" +
			"  " + theme.Secondary.Render("what happens next:") + "\n" +
			"  point ccsync at a repo (or create one on the spot) and hit go.\n" +
			"  ccsync figures out the rest — no profile names, no manual merges.\n\n" +
			"  " + theme.Secondary.Render("under a minute.") + "\n\n")
	sb.WriteString(theme.Primary.Render("enter ") + "start • " + theme.Hint.Render("s skip (set up later)"))
	return sb.String()
}

func (m *onboardingModel) renderDone() string {
	var sb strings.Builder
	sb.WriteString(theme.Good.Render("all set ✓") + "\n\n")
	if m.ctx.State.SyncRepoURL != "" {
		sb.WriteString(theme.Hint.Render(
			"press any key to review your first sync.\n" +
				"on a fresh machine, try `p` in the preview for pull-only\n" +
				"so you don't push anything local up until you're ready.") + "\n")
	} else {
		sb.WriteString(theme.Hint.Render(
			"you skipped setup — run `ccsync bootstrap --repo <URL>` when\n" +
				"you're ready, or reopen this from Home.") + "\n")
	}
	return sb.String()
}

