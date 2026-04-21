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

// profileMode drives which UI the Profiles screen shows. The list mode is
// the default; everything else is a modal sub-flow that captures esc.
type profileMode int

const (
	modeList profileMode = iota
	modeCreateName
	modeCreateDesc
	modeEditName // name field editable; desc filled with current
	modeEditDesc
	modeConfirmDelete
)

type profilesModel struct {
	ctx      *AppContext
	cursor   int
	profiles []string
	err      error
	message  string

	mode        profileMode
	nameIn      textinput.Model
	descIn      textinput.Model
	extendsFrom string // pre-filled with active profile so new profile inherits by default

	// editTarget is the original name of the profile being edited — needed
	// so we can tell "unchanged" from "renamed" on save.
	editTarget string
}

func newProfiles(ctx *AppContext) *profilesModel {
	names := make([]string, 0, len(ctx.Config.Profiles))
	for k := range ctx.Config.Profiles {
		names = append(names, k)
	}
	sort.Strings(names)

	active := ctx.State.ActiveProfile
	startIdx := 0
	for i, n := range names {
		if n == active {
			startIdx = i
			break
		}
	}

	nameIn := textinput.New()
	nameIn.Placeholder = "profile-name"
	nameIn.CharLimit = 32
	nameIn.Width = 24

	descIn := textinput.New()
	descIn.Placeholder = "short description (optional)"
	descIn.CharLimit = 80
	descIn.Width = 48

	return &profilesModel{
		ctx: ctx, profiles: names, cursor: startIdx,
		nameIn: nameIn, descIn: descIn,
		extendsFrom: active,
	}
}

func (m *profilesModel) Title() string { return "Profiles" }
func (m *profilesModel) Init() tea.Cmd { return nil }

// CapturesEscape keeps esc scoped to cancelling a sub-flow (create / edit /
// confirm delete) rather than popping the whole Profiles screen — users
// would lose their typing otherwise.
func (m *profilesModel) CapturesEscape() bool { return m.mode != modeList }

func (m *profilesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.mode {
		case modeCreateName, modeCreateDesc:
			return m.updateCreate(msg)
		case modeEditName, modeEditDesc:
			return m.updateEdit(msg)
		case modeConfirmDelete:
			return m.updateConfirmDelete(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

// --- list mode ---

func (m *profilesModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.message = ""
	case "down", "j":
		if m.cursor < len(m.profiles)-1 {
			m.cursor++
		}
		m.message = ""
	case "n":
		m.mode = modeCreateName
		m.err = nil
		m.nameIn.SetValue("")
		m.descIn.SetValue("")
		m.nameIn.Focus()
		return m, textinput.Blink
	case "e":
		if len(m.profiles) == 0 {
			return m, nil
		}
		target := m.profiles[m.cursor]
		m.editTarget = target
		m.mode = modeEditName
		m.err = nil
		m.nameIn.SetValue(target)
		m.nameIn.CursorEnd()
		m.descIn.SetValue(m.ctx.Config.Profiles[target].Description)
		m.descIn.CursorEnd()
		m.nameIn.Focus()
		return m, textinput.Blink
	case "d":
		if len(m.profiles) == 0 {
			return m, nil
		}
		target := m.profiles[m.cursor]
		// Pre-check validity so we don't even bother confirming an
		// impossible delete (active / last profile).
		if target == m.ctx.State.ActiveProfile {
			m.err = fmt.Errorf("can't delete the active profile — switch first")
			return m, nil
		}
		if len(m.ctx.Config.Profiles) <= 1 {
			m.err = fmt.Errorf("can't delete the last remaining profile")
			return m, nil
		}
		m.mode = modeConfirmDelete
		m.err = nil
		return m, nil
	case "enter":
		if len(m.profiles) == 0 {
			return m, nil
		}
		target := m.profiles[m.cursor]
		m.ctx.State.ActiveProfile = target
		if err := state.Save(m.ctx.StateDir, m.ctx.State); err != nil {
			m.err = err
			return m, nil
		}
		return m, popScreen()
	}
	return m, nil
}

// --- create mode ---

func (m *profilesModel) updateCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.resetModal()
		return m, nil
	case "enter":
		switch m.mode {
		case modeCreateName:
			name := strings.TrimSpace(m.nameIn.Value())
			if name == "" {
				m.err = fmt.Errorf("profile name required")
				return m, nil
			}
			if _, exists := m.ctx.Config.Profiles[name]; exists {
				m.err = fmt.Errorf("profile %q already exists", name)
				return m, nil
			}
			m.err = nil
			m.mode = modeCreateDesc
			m.nameIn.Blur()
			m.descIn.Focus()
			return m, textinput.Blink
		case modeCreateDesc:
			name := strings.TrimSpace(m.nameIn.Value())
			desc := strings.TrimSpace(m.descIn.Value())
			if err := profile.Create(m.ctx.Config, m.ctx.ConfigPath(), name, desc); err != nil {
				m.err = err
				return m, nil
			}
			// Default new profiles to extending the currently-active one
			// so users get inheritance without editing YAML by hand.
			if m.extendsFrom != "" && m.extendsFrom != name {
				spec := m.ctx.Config.Profiles[name]
				spec.Extends = m.extendsFrom
				m.ctx.Config.Profiles[name] = spec
				if err := m.ctx.Config.SaveWithBackup(m.ctx.ConfigPath()); err != nil {
					m.err = err
					return m, nil
				}
			}
			m.rebuildList(name)
			m.message = fmt.Sprintf("created profile %q", name)
			if m.extendsFrom != "" && m.extendsFrom != name {
				m.message += fmt.Sprintf(" (extends %s)", m.extendsFrom)
			}
			m.resetModal()
			return m, nil
		}
	}
	var cmd tea.Cmd
	if m.mode == modeCreateName {
		m.nameIn, cmd = m.nameIn.Update(msg)
	} else {
		m.descIn, cmd = m.descIn.Update(msg)
	}
	return m, cmd
}

// --- edit mode ---

func (m *profilesModel) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.resetModal()
		return m, nil
	case "tab", "down":
		if m.mode == modeEditName {
			m.mode = modeEditDesc
			m.nameIn.Blur()
			m.descIn.Focus()
			return m, textinput.Blink
		}
	case "shift+tab", "up":
		if m.mode == modeEditDesc {
			m.mode = modeEditName
			m.descIn.Blur()
			m.nameIn.Focus()
			return m, textinput.Blink
		}
	case "enter":
		if m.mode == modeEditName {
			// First enter — advance to description field.
			m.mode = modeEditDesc
			m.nameIn.Blur()
			m.descIn.Focus()
			return m, textinput.Blink
		}
		return m, m.applyEdit()
	}
	var cmd tea.Cmd
	if m.mode == modeEditName {
		m.nameIn, cmd = m.nameIn.Update(msg)
	} else {
		m.descIn, cmd = m.descIn.Update(msg)
	}
	return m, cmd
}

// applyEdit commits a profile rename (if any) + description change. Rename
// is an atomic-ish operation that covers the ccsync.yaml key, the repo
// worktree directory, state.ActiveProfile, state.LastSyncedSHA, and any
// other profile's Extends pointing at the old name.
func (m *profilesModel) applyEdit() tea.Cmd {
	oldName := m.editTarget
	newName := strings.TrimSpace(m.nameIn.Value())
	newDesc := strings.TrimSpace(m.descIn.Value())

	if newName == "" {
		m.err = fmt.Errorf("profile name can't be empty")
		return nil
	}
	if newName != oldName {
		if _, exists := m.ctx.Config.Profiles[newName]; exists {
			m.err = fmt.Errorf("profile %q already exists", newName)
			return nil
		}
	}

	cfg := m.ctx.Config
	spec := cfg.Profiles[oldName]
	spec.Description = newDesc

	if newName == oldName {
		// Description-only change.
		cfg.Profiles[oldName] = spec
		if err := cfg.SaveWithBackup(m.ctx.ConfigPath()); err != nil {
			m.err = err
			return nil
		}
		m.message = fmt.Sprintf("updated description for %q", oldName)
		m.resetModal()
		return nil
	}

	// Rename. Move repo directory first — if that fails we haven't
	// touched any config state and the user can retry cleanly.
	oldDir := filepath.Join(m.ctx.RepoPath, "profiles", oldName)
	newDir := filepath.Join(m.ctx.RepoPath, "profiles", newName)
	if _, err := os.Stat(oldDir); err == nil {
		if err := os.Rename(oldDir, newDir); err != nil {
			m.err = fmt.Errorf("move repo profile dir: %w", err)
			return nil
		}
	}

	// Rename in ccsync.yaml — and also fix up any other profile whose
	// Extends points at the old name, or we'd leave a broken chain.
	delete(cfg.Profiles, oldName)
	cfg.Profiles[newName] = spec
	for name, s := range cfg.Profiles {
		if s.Extends == oldName {
			s.Extends = newName
			cfg.Profiles[name] = s
		}
	}
	if err := cfg.SaveWithBackup(m.ctx.ConfigPath()); err != nil {
		// Best-effort rollback of the dir move — the yaml save is the
		// canonical source of truth, so if yaml is unchanged we don't
		// want the dir in a mismatched state.
		_ = os.Rename(newDir, oldDir)
		m.err = err
		return nil
	}

	// Update state: active profile + last-synced-sha map.
	if m.ctx.State.ActiveProfile == oldName {
		m.ctx.State.ActiveProfile = newName
	}
	if sha, ok := m.ctx.State.LastSyncedSHA[oldName]; ok {
		m.ctx.State.LastSyncedSHA[newName] = sha
		delete(m.ctx.State.LastSyncedSHA, oldName)
	}
	_ = state.Save(m.ctx.StateDir, m.ctx.State)

	m.message = fmt.Sprintf("renamed %q → %q", oldName, newName)
	m.rebuildList(newName)
	m.resetModal()
	return nil
}

// --- confirm-delete mode ---

func (m *profilesModel) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n":
		m.mode = modeList
		return m, nil
	case "y":
		target := m.profiles[m.cursor]
		if err := profile.Delete(m.ctx.Config, m.ctx.ConfigPath(), target, m.ctx.State.ActiveProfile); err != nil {
			m.err = err
			m.mode = modeList
			return m, nil
		}
		// Remove the profile's directory from the repo worktree — next
		// sync will commit the deletion and push it up. Other profiles
		// with Extends pointing at this one become broken chains; we
		// surface that with a warning but don't auto-fix since the user
		// might intend to rename first.
		dir := filepath.Join(m.ctx.RepoPath, "profiles", target)
		if _, err := os.Stat(dir); err == nil {
			_ = os.RemoveAll(dir)
		}
		// Drop the last-synced pointer so it doesn't linger in state.
		delete(m.ctx.State.LastSyncedSHA, target)
		_ = state.Save(m.ctx.StateDir, m.ctx.State)

		m.message = fmt.Sprintf("deleted profile %q", target)
		m.rebuildList("")
		m.mode = modeList
		m.err = nil
		return m, nil
	}
	return m, nil
}

// --- helpers ---

func (m *profilesModel) resetModal() {
	m.mode = modeList
	m.nameIn.Blur()
	m.descIn.Blur()
	m.err = nil
	m.editTarget = ""
}

// rebuildList refreshes m.profiles from ctx.Config.Profiles. If selectName
// is non-empty, the cursor lands on that profile; otherwise it stays at 0
// or clamps to the new list length.
func (m *profilesModel) rebuildList(selectName string) {
	names := make([]string, 0, len(m.ctx.Config.Profiles))
	for k := range m.ctx.Config.Profiles {
		names = append(names, k)
	}
	sort.Strings(names)
	m.profiles = names
	if selectName != "" {
		for i, n := range names {
			if n == selectName {
				m.cursor = i
				return
			}
		}
	}
	if m.cursor >= len(m.profiles) {
		if len(m.profiles) == 0 {
			m.cursor = 0
		} else {
			m.cursor = len(m.profiles) - 1
		}
	}
}

// --- view ---

func (m *profilesModel) View() string {
	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render(m.message) + "\n\n")
	}

	switch m.mode {
	case modeCreateName, modeCreateDesc:
		sb.WriteString(m.renderCreate())
		return sb.String()
	case modeEditName, modeEditDesc:
		sb.WriteString(m.renderEdit())
		return sb.String()
	case modeConfirmDelete:
		sb.WriteString(m.renderConfirmDelete())
		return sb.String()
	}

	if len(m.profiles) == 0 {
		sb.WriteString(theme.Hint.Render("no profiles configured — press n to create one"))
		return sb.String()
	}
	for i, name := range m.profiles {
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		marker := ""
		if name == m.ctx.State.ActiveProfile {
			marker = theme.Good.Render(" (active)")
		}
		spec := m.ctx.Config.Profiles[name]
		desc := spec.Description
		line := name + marker
		if desc != "" {
			line += "  " + theme.Hint.Render("— "+desc)
		}
		sb.WriteString(cursor + line + "\n")

		details := profileDetails(m.ctx.Config, name, spec)
		if details != "" {
			sb.WriteString("    " + theme.Hint.Render(details) + "\n")
		}
	}
	sb.WriteString("\n" +
		theme.Primary.Render("enter ") + "switch • " +
		theme.Primary.Render("n ") + "new • " +
		theme.Primary.Render("e ") + "edit • " +
		theme.Primary.Render("d ") + "delete • " +
		theme.Hint.Render("↑↓ move"))
	return sb.String()
}

func (m *profilesModel) renderCreate() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("new profile") + "\n\n")
	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("name:       "), m.nameIn.View())
	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("description:"), m.descIn.View())
	if m.extendsFrom != "" {
		fmt.Fprintf(&sb, "  %s  %s %s\n",
			theme.Secondary.Render("extends:    "),
			m.extendsFrom,
			theme.Hint.Render("(will inherit from active profile)"))
	}
	sb.WriteString("\n" + theme.Hint.Render("enter next • esc cancel"))
	return sb.String()
}

func (m *profilesModel) renderEdit() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %s\n\n",
		theme.Heading.Render("edit profile"),
		theme.Hint.Render("("+m.editTarget+")"))
	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("name:       "), m.nameIn.View())
	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("description:"), m.descIn.View())
	sb.WriteString("\n" +
		theme.Hint.Render("tab/↑↓ switch field • enter save • esc cancel"))
	if m.editTarget != "" {
		sb.WriteString("\n\n" + theme.Hint.Render(
			"renaming moves repo profiles/ dir + updates state.ActiveProfile + "+
				"fixes any other profile's extends pointer"))
	}
	return sb.String()
}

func (m *profilesModel) renderConfirmDelete() string {
	target := m.profiles[m.cursor]
	var sb strings.Builder
	sb.WriteString(theme.Warn.Render("delete profile?") + "\n\n")
	fmt.Fprintf(&sb, "  profile: %s\n\n", theme.Primary.Render(target))
	sb.WriteString(theme.Hint.Render(
		"this removes the profile from ccsync.yaml, drops the repo's profiles/"+target+"/\n"+
			"directory (next sync will commit the deletion), and clears the\n"+
			"last-synced pointer. other profiles extending this one will end up\n"+
			"with a broken chain — consider renaming instead.") + "\n\n")
	sb.WriteString(
		theme.Primary.Render("y") + "  confirm delete • " +
			theme.Hint.Render("n / esc cancel"))
	return sb.String()
}

// profileDetails renders one-line info about a profile's extends chain,
// exclude count, and host classes. Empty string when none apply.
func profileDetails(cfg *config.Config, name string, spec config.ProfileSpec) string {
	var parts []string
	if resolved, err := config.EffectiveProfile(cfg, name); err == nil {
		if len(resolved.Chain) > 1 {
			parts = append(parts, "extends "+strings.Join(resolved.Chain[1:], " ← "))
		}
		if n := len(resolved.PathExcludes); n > 0 {
			parts = append(parts, fmt.Sprintf("%d exclude rule(s)", n))
		}
	}
	if len(spec.HostClasses) > 0 {
		parts = append(parts, "host-class: "+strings.Join(spec.HostClasses, ","))
	}
	return strings.Join(parts, " • ")
}
