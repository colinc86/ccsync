package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/profile"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

// profilePickerModel is the standalone screen shown after a successful
// bootstrap. It lets the user either join an existing profile (the
// common case for machine #2+ joining a repo with a "default" already
// set up) or create a new profile extending the first one.
//
// Stands on its own (not inside the onboardingModel's step machine)
// because the bootstrap wizard's own stepDone handler calls
// popToRoot — which flattens any parent screen. Putting the picker
// INSIDE onboarding meant it was never reachable; running it as a
// standalone screen that the wizard pushes directly works from any
// entry point.
type profilePickerModel struct {
	ctx *AppContext

	names []string // sorted list of existing profile names
	cur   int      // cursor into names

	creating  bool            // flipping to "name this new profile" sub-view
	nameInput textinput.Model // used while creating

	err  error
	done bool

	// autoJoin is true for a freshly-bootstrapped repo that has
	// exactly one profile and no content under it yet — i.e. the
	// user just created this repo themselves, so there's no real
	// choice to make and the picker's "this repo already has…"
	// framing would be confusing. Init() fires an auto-finish Cmd
	// in that case. Different from the original v0.6.0/v0.6.1 bug
	// because that one auto-advanced whenever a single profile
	// existed, regardless of content — silently stranding machine
	// #2 users on default.
	autoJoin bool
}

func newProfilePickerScreen(ctx *AppContext) *profilePickerModel {
	names := sortedProfileNamesForPicker(ctx.Config)
	m := &profilePickerModel{ctx: ctx, names: names}

	// Default cursor to the active profile if it's already set and in
	// the list (typical for re-bootstraps / repeat runs).
	for i, n := range names {
		if n == ctx.State.ActiveProfile {
			m.cur = i
			break
		}
	}

	// Auto-advance narrowly: only when the repo has exactly one
	// profile AND its content subtree is empty. That's the "I just
	// created this repo from scratch" shape — showing "this repo
	// already has profiles…" in that case would be wrong. Machine #2
	// joining an existing repo fails this check because the other
	// machine's prior syncs populated the subtree; the picker shows
	// normally there.
	m.autoJoin = isFreshlyBootstrappedRepo(ctx, names)
	return m
}

// isFreshlyBootstrappedRepo reports whether the repo was just created
// by this machine's own bootstrap and has no cross-machine content
// yet. The signals:
//
//  1. Exactly one profile in ccsync.yaml.
//  2. The active profile's claude/ subtree in the repo either doesn't
//     exist yet or contains no files.
//
// The second signal is what distinguishes machine #1 fresh-repo
// (empty subtree) from machine #2 joining (subtree has files from the
// other machine's syncs).
func isFreshlyBootstrappedRepo(ctx *AppContext, names []string) bool {
	if len(names) != 1 {
		return false
	}
	active := ctx.State.ActiveProfile
	if active == "" {
		active = names[0]
	}
	subtree := filepath.Join(ctx.RepoPath, "profiles", active, "claude")
	entries, err := os.ReadDir(subtree)
	if err != nil {
		// Dir doesn't exist → fresh bootstrap. (Any other error is
		// rare enough we treat as "fresh"; worst case is showing the
		// picker when we could've skipped it.)
		return true
	}
	return len(entries) == 0
}

func (m *profilePickerModel) Title() string { return "Which profile is this machine?" }

// autoJoinMsg is fired by Init() when autoJoin is set, and handled by
// Update() to finalize straight through without showing the picker UI.
// A distinct type from profilePickerDoneMsg so tests can tell them apart.
type autoJoinMsg struct{}

func (m *profilePickerModel) Init() tea.Cmd {
	if m.autoJoin {
		return func() tea.Msg { return autoJoinMsg{} }
	}
	return nil
}

// CapturesEscape keeps esc from popping the screen while the user is
// typing into the name field — it should cancel back to the picker
// rather than dropping the whole flow.
func (m *profilePickerModel) CapturesEscape() bool { return m.creating }

// profilePickerDoneMsg reports completion of the persistence step so
// the final transition runs as a sequence of commands rather than
// all-at-once (so popToRoot lands before switchTo(syncPreview)).
type profilePickerDoneMsg struct{ err error }

func (m *profilePickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case autoJoinMsg:
		// Freshly-bootstrapped repo: quietly finalize onto the single
		// existing profile, no keystroke required.
		if len(m.names) == 0 {
			return m, nil
		}
		return m, m.finalizeAs(m.names[m.cur])
	case profilePickerDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.done = true
		return m, tea.Sequence(popToRoot(), switchTo(newSyncPreview(m.ctx)))
	case tea.KeyMsg:
		if m.done {
			return m, nil
		}
		if m.creating {
			return m.updateCreating(msg)
		}
		return m.updatePicking(msg)
	}
	return m, nil
}

func (m *profilePickerModel) updatePicking(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "up", "k":
		if m.cur > 0 {
			m.cur--
		}
	case "down", "j":
		if m.cur < len(m.names)-1 {
			m.cur++
		}
	case "enter":
		if len(m.names) == 0 {
			return m, nil
		}
		return m, m.finalizeAs(m.names[m.cur])
	case "n":
		in := textinput.New()
		in.Placeholder = defaultProfileNameForMachine(m.ctx)
		in.CharLimit = 32
		in.Width = 24
		in.Focus()
		m.nameInput = in
		m.creating = true
		return m, textinput.Blink
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		idx := int(key[0] - '1')
		if idx < len(m.names) {
			m.cur = idx
			return m, m.finalizeAs(m.names[idx])
		}
	}
	return m, nil
}

func (m *profilePickerModel) updateCreating(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			name = m.nameInput.Placeholder
		}
		return m, m.finalizeCreating(name)
	case "esc":
		m.creating = false
		return m, nil
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

// finalizeAs switches state to an existing profile name, marks
// onboarding complete, and returns a Cmd reporting the result.
func (m *profilePickerModel) finalizeAs(name string) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		ctx.State.ActiveProfile = name
		ctx.State.OnboardingComplete = true
		if err := state.Save(ctx.StateDir, ctx.State); err != nil {
			return profilePickerDoneMsg{err: err}
		}
		return profilePickerDoneMsg{}
	}
}

// finalizeCreating runs profile.Create, wires extends to the first
// existing profile so inheritance kicks in, and activates the new
// profile. OnboardingComplete flips true on the way through.
func (m *profilePickerModel) finalizeCreating(name string) tea.Cmd {
	ctx := m.ctx
	existing := append([]string(nil), m.names...)
	return func() tea.Msg {
		cfgPath := ctx.ConfigPath()
		if err := profile.Create(ctx.Config, cfgPath, name, ""); err != nil {
			return profilePickerDoneMsg{err: err}
		}
		if len(existing) > 0 {
			parent := existing[0]
			spec := ctx.Config.Profiles[name]
			spec.Extends = parent
			ctx.Config.Profiles[name] = spec
			if err := ctx.Config.SaveWithBackup(cfgPath); err != nil {
				return profilePickerDoneMsg{err: err}
			}
		}
		ctx.State.ActiveProfile = name
		ctx.State.OnboardingComplete = true
		if err := state.Save(ctx.StateDir, ctx.State); err != nil {
			return profilePickerDoneMsg{err: err}
		}
		return profilePickerDoneMsg{}
	}
}

func (m *profilePickerModel) View() string {
	if m.autoJoin && !m.done {
		return theme.Hint.Render("setting up…")
	}
	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(renderError(m.err) + "\n\n")
	}

	if m.creating {
		sb.WriteString(theme.Heading.Render("name this machine's profile") + "\n\n")
		sb.WriteString(theme.Hint.Render(
			"we'll create a new profile that extends the first existing\n"+
				"one (usually \"default\") so this machine inherits everything\n"+
				"already in the repo. Tweak later from Home → more → Profiles.") + "\n\n")
		fmt.Fprintf(&sb, "  %s  %s\n\n", theme.Secondary.Render("name:"), m.nameInput.View())
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "enter", label: "create"},
			{cap: "esc", label: "back to picker"},
		}))
		return sb.String()
	}

	sb.WriteString(theme.Hint.Render(
		"this repo already has the profiles below. pick one if this machine\n"+
			"should match an existing setup, or create a new one (e.g. \"work\"\n"+
			"on a second laptop that inherits from \"default\").") + "\n\n")

	for i, name := range m.names {
		cursor := "  "
		if i == m.cur {
			cursor = theme.Primary.Render("▸ ")
		}
		desc := ""
		if spec, ok := m.ctx.Config.Profiles[name]; ok && spec.Description != "" {
			desc = "  " + theme.Hint.Render("— "+spec.Description)
		}
		num := theme.Keycap.Render(fmt.Sprintf("%d", i+1))
		fmt.Fprintf(&sb, "%s%s  %s%s\n", cursor, num, name, desc)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "  %s  %s\n\n",
		theme.Keycap.Render("n"),
		theme.Primary.Render("create a new profile for this machine"))
	sb.WriteString(renderFooterBar([]footerKey{
		{cap: "enter", label: "pick cursored"},
		{cap: "1-9", label: "pick by number"},
		{cap: "n", label: "new"},
		{cap: "↑↓", label: "move"},
	}))
	return sb.String()
}

// sortedProfileNamesForPicker is a local helper (the identically-named
// sortedProfileNames in onboarding.go stays for backwards-compat and
// will be removed when the dead onboardProfile step goes).
func sortedProfileNamesForPicker(cfg *config.Config) []string {
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

// defaultProfileNameForMachine picks a sensible placeholder for the
// "create new profile" textinput — machine's hostname lowercased,
// falling back to "work" since that's the canonical second-machine
// name.
func defaultProfileNameForMachine(ctx *AppContext) string {
	if ctx.HostName != "" {
		return strings.ToLower(ctx.HostName)
	}
	return "work"
}
