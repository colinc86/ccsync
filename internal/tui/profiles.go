package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/profile"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

type profilesModel struct {
	ctx      *AppContext
	cursor   int
	profiles []string
	err      error
	message  string

	// create flow — when nameIn is focused, user is typing a new profile name.
	creating    createStage
	nameIn      textinput.Model
	descIn      textinput.Model
	extendsFrom string // pre-filled with active profile so new profile inherits by default
}

type createStage int

const (
	createOff createStage = iota
	createName
	createDesc
)

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

// CapturesEscape keeps esc from popping the whole screen while the user is
// in the inline create flow — esc cancels the create instead.
func (m *profilesModel) CapturesEscape() bool { return m.creating != createOff }

func (m *profilesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.creating != createOff {
			return m.updateCreate(msg)
		}
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
			m.creating = createName
			m.err = nil
			m.nameIn.SetValue("")
			m.descIn.SetValue("")
			m.nameIn.Focus()
			return m, textinput.Blink
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
	}
	return m, nil
}

// updateCreate drives the inline name → description → save flow.
func (m *profilesModel) updateCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.creating = createOff
		m.nameIn.Blur()
		m.descIn.Blur()
		m.err = nil
		return m, nil
	case "enter":
		switch m.creating {
		case createName:
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
			m.creating = createDesc
			m.nameIn.Blur()
			m.descIn.Focus()
			return m, textinput.Blink
		case createDesc:
			name := strings.TrimSpace(m.nameIn.Value())
			desc := strings.TrimSpace(m.descIn.Value())
			if err := profile.Create(m.ctx.Config, m.ctx.ConfigPath(), name, desc); err != nil {
				m.err = err
				return m, nil
			}
			// Default new profiles to extending the currently-active one so
			// users get inheritance without having to hand-edit YAML.
			if m.extendsFrom != "" && m.extendsFrom != name {
				spec := m.ctx.Config.Profiles[name]
				spec.Extends = m.extendsFrom
				m.ctx.Config.Profiles[name] = spec
				if err := m.ctx.Config.SaveWithBackup(m.ctx.ConfigPath()); err != nil {
					m.err = err
					return m, nil
				}
			}
			// Rebuild list
			names := make([]string, 0, len(m.ctx.Config.Profiles))
			for k := range m.ctx.Config.Profiles {
				names = append(names, k)
			}
			sort.Strings(names)
			m.profiles = names
			for i, n := range names {
				if n == name {
					m.cursor = i
					break
				}
			}
			m.creating = createOff
			m.nameIn.Blur()
			m.descIn.Blur()
			m.err = nil
			m.message = fmt.Sprintf("created profile %q", name)
			if m.extendsFrom != "" && m.extendsFrom != name {
				m.message += fmt.Sprintf(" (extends %s)", m.extendsFrom)
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	if m.creating == createName {
		m.nameIn, cmd = m.nameIn.Update(msg)
	} else {
		m.descIn, cmd = m.descIn.Update(msg)
	}
	return m, cmd
}

func (m *profilesModel) View() string {
	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render(m.message) + "\n\n")
	}

	if m.creating != createOff {
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
		theme.Primary.Render("n ") + "new profile • " +
		theme.Hint.Render("↑↓ move"))
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
