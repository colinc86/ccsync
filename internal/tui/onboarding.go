package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/profile"
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

	// Profile picker state for onboardProfile step.
	profileNames     []string        // sorted list of existing profile names
	profileCursor    int             // which existing profile is highlighted
	profileNameInput textinput.Model // only used while creating a new profile
	creatingProfile  bool            // toggles picker ⇄ name-input sub-view
}

type onboardingStep int

const (
	onboardWelcome   onboardingStep = iota
	onboardPolicy                   // pick auto-sync vs review-each
	onboardBootstrap                // hand off to bootstrapWizardModel
	onboardProfile                  // pick existing profile or create a new one
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
		// The bootstrap wizard we delegated to finished. On error surface
		// it and bail to done. On success, re-load the freshly-cloned
		// ccsync.yaml and show the profile picker so the user can either
		// join as an existing profile (typical for machine #2+) or
		// create a new one (e.g. "work" extending the home machine's
		// "default").
		if msg.err != nil {
			m.err = msg.err
			m.step = onboardDone
			return m, nil
		}
		if msg.st != nil {
			m.ctx.State = msg.st
		}
		if cfg, err := config.Load(m.ctx.ConfigPath()); err == nil {
			m.ctx.Config = cfg
		}
		m.profileNames = sortedProfileNames(m.ctx.Config)
		// Default cursor to the active profile if it's in the list.
		for i, n := range m.profileNames {
			if n == m.ctx.State.ActiveProfile {
				m.profileCursor = i
				break
			}
		}
		m.step = onboardProfile
		return m, nil

	case tea.KeyMsg:
		// 's' skips the whole wizard — but only when NOT typing into a
		// textinput, otherwise the letter 's' gets eaten before it
		// can be entered into a profile name.
		if msg.String() == "s" && !(m.step == onboardProfile && m.creatingProfile) {
			return m.finish()
		}
		switch m.step {
		case onboardWelcome:
			return m.updateWelcome(msg)
		case onboardPolicy:
			return m.updatePolicy(msg)
		case onboardProfile:
			return m.updateProfilePick(msg)
		case onboardDone:
			return m.finish()
		}
	}
	return m, nil
}

// updateProfilePick handles the profile-selection step shown after a
// successful bootstrap. Two sub-states:
//   - picking: arrow keys + numbers choose an existing profile; 'n'
//     switches to creating a new profile (prompts for a name)
//   - creating: textinput for the new profile name; enter commits
func (m *onboardingModel) updateProfilePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.creatingProfile {
		return m.updateProfileCreate(msg)
	}
	key := msg.String()
	switch key {
	case "up", "k":
		if m.profileCursor > 0 {
			m.profileCursor--
		}
	case "down", "j":
		if m.profileCursor < len(m.profileNames)-1 {
			m.profileCursor++
		}
	case "enter":
		if len(m.profileNames) == 0 {
			return m, nil
		}
		target := m.profileNames[m.profileCursor]
		if err := m.activateProfile(target); err != nil {
			m.err = err
			return m, nil
		}
		m.step = onboardDone
		return m, nil
	case "n":
		// Spawn a textinput for the new profile name. Defaults to the
		// machine's hostname so "enter" is the happy path.
		in := textinput.New()
		in.Placeholder = defaultNewProfileName(m.ctx)
		in.CharLimit = 32
		in.Width = 24
		in.Focus()
		m.profileNameInput = in
		m.creatingProfile = true
		return m, textinput.Blink
	}
	// Numeric shortcut: 1..9 selects nth entry in the list.
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		idx := int(key[0]-'1')
		if idx < len(m.profileNames) {
			m.profileCursor = idx
		}
	}
	return m, nil
}

// updateProfileCreate handles the "name this new profile" sub-view.
// Enter commits; esc cancels back to the picker.
func (m *onboardingModel) updateProfileCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.profileNameInput.Value())
		if name == "" {
			name = m.profileNameInput.Placeholder
		}
		if err := m.createAndActivate(name); err != nil {
			m.err = err
			return m, nil
		}
		m.creatingProfile = false
		m.step = onboardDone
		return m, nil
	case "esc":
		m.creatingProfile = false
		return m, nil
	}
	var cmd tea.Cmd
	m.profileNameInput, cmd = m.profileNameInput.Update(msg)
	return m, cmd
}

// activateProfile switches state.ActiveProfile to target and saves.
// Takes no snapshot because no files have been written to ~/.claude
// yet on this machine — nothing to back up.
func (m *onboardingModel) activateProfile(target string) error {
	m.ctx.State.ActiveProfile = target
	return state.Save(m.ctx.StateDir, m.ctx.State)
}

// createAndActivate creates a new profile in ccsync.yaml extending the
// repo's first profile (usually "default"), switches state to it, and
// leaves the ccsync.yaml write to be picked up by the first real sync.
func (m *onboardingModel) createAndActivate(name string) error {
	cfgPath := m.ctx.ConfigPath()
	if err := profile.Create(m.ctx.Config, cfgPath, name, ""); err != nil {
		return err
	}
	// Set Extends to the first existing profile (usually "default") so
	// the new machine inherits the content that's already in the repo.
	if len(m.profileNames) > 0 {
		parent := m.profileNames[0]
		spec := m.ctx.Config.Profiles[name]
		spec.Extends = parent
		m.ctx.Config.Profiles[name] = spec
		if err := m.ctx.Config.SaveWithBackup(cfgPath); err != nil {
			return err
		}
	}
	return m.activateProfile(name)
}

// sortedProfileNames returns config's profile names in stable order so
// the onboarding picker renders deterministically across launches.
func sortedProfileNames(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// defaultNewProfileName returns a reasonable placeholder for the new-
// profile textinput — the machine's hostname, lowercased. Falls back
// to "work" because that's the canonical second-machine name.
func defaultNewProfileName(ctx *AppContext) string {
	if ctx.HostName != "" {
		return strings.ToLower(ctx.HostName)
	}
	return "work"
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
	case onboardProfile:
		sb.WriteString(m.renderProfilePick())
	case onboardDone:
		sb.WriteString(m.renderDone())
	}
	return sb.String()
}

func (m *onboardingModel) renderProfilePick() string {
	var sb strings.Builder
	if m.creatingProfile {
		sb.WriteString(theme.Heading.Render("name this machine's profile") + "\n\n")
		sb.WriteString(theme.Hint.Render(
			"we'll create a new profile that extends the repo's primary one\n"+
				"(so this machine inherits everything already there). you can\n"+
				"tweak it later from Home → more → Profiles.") + "\n\n")
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("name:"), m.profileNameInput.View())
		sb.WriteString("\n" + theme.Primary.Render("enter ") + "create  " +
			theme.Hint.Render("esc back to picker"))
		return sb.String()
	}

	sb.WriteString(theme.Heading.Render("which profile is this machine?") + "\n\n")
	sb.WriteString(theme.Hint.Render(
		"this repo already has the profiles below. pick one if this machine\n"+
			"should match an existing setup, or create a new one (e.g. \"work\"\n"+
			"on a second laptop that inherits from \"default\").") + "\n\n")

	for i, name := range m.profileNames {
		cursor := "  "
		if i == m.profileCursor {
			cursor = theme.Primary.Render("▸ ")
		}
		desc := ""
		if spec, ok := m.ctx.Config.Profiles[name]; ok && spec.Description != "" {
			desc = "  " + theme.Hint.Render("— "+spec.Description)
		}
		num := theme.Primary.Render(fmt.Sprintf("%d", i+1))
		fmt.Fprintf(&sb, "%s%s  %s%s\n", cursor, num, name, desc)
	}
	sb.WriteString("\n")
	sb.WriteString("  " + theme.Primary.Render("n") + "  create a new profile for this machine\n")
	sb.WriteString("\n" + theme.Hint.Render("↑↓ move · 1-9/enter pick · n new · s skip"))
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

