package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/snapshot"
	"github.com/colinc86/ccsync/internal/theme"
)

type homeModel struct {
	ctx     *AppContext
	cursor  int
	choices []homeChoice
}

type homeChoice struct {
	label   string
	enabled bool
	onEnter func() tea.Cmd
}

func newHome(ctx *AppContext) homeModel {
	h := homeModel{ctx: ctx}
	h.rebuildChoices()
	return h
}

func (m *homeModel) rebuildChoices() {
	bootstrapped := m.ctx.State.SyncRepoURL != ""
	m.choices = []homeChoice{}
	if !bootstrapped {
		m.choices = append(m.choices, homeChoice{
			label:   "Bootstrap…",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newBootstrapWizard(m.ctx)) },
		})
	}
	m.choices = append(m.choices,
		homeChoice{
			label:   "Sync now",
			enabled: bootstrapped,
			onEnter: func() tea.Cmd { return switchTo(newSyncPreview(m.ctx)) },
		},
		homeChoice{
			label:   "Browse tracked files",
			enabled: bootstrapped,
			onEnter: func() tea.Cmd { return switchTo(newBrowseTracked(m.ctx)) },
		},
		homeChoice{
			label:   "History",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newSyncHistory(m.ctx)) },
		},
		homeChoice{
			label:   "Profiles",
			enabled: bootstrapped,
			onEnter: func() tea.Cmd { return switchTo(newProfiles(m.ctx)) },
		},
		homeChoice{
			label:   "Doctor",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newDoctorScreen(m.ctx)) },
		},
		homeChoice{
			label:   "Settings",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newSettings(m.ctx)) },
		},
	)
}

func (m homeModel) Title() string { return "ccsync" }

func (m homeModel) Init() tea.Cmd { return nil }

func (m homeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Rebuild choices every tick so returning from Bootstrap or Profiles
	// immediately reflects state changes.
	m.rebuildChoices()
	if m.cursor >= len(m.choices) {
		m.cursor = 0
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		s := msg.String()
		// Numeric shortcuts — "1" picks the first menu item, etc.
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			idx := int(s[0] - '1')
			if idx < len(m.choices) && m.choices[idx].enabled {
				m.cursor = idx
				return m, m.choices[idx].onEnter()
			}
		}
		switch s {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter":
			c := m.choices[m.cursor]
			if c.enabled {
				return m, c.onEnter()
			}
		}
	}
	return m, nil
}

func (m homeModel) View() string {
	var sb strings.Builder

	bootstrapped := m.ctx.State.SyncRepoURL != ""
	profile := m.ctx.State.ActiveProfile
	if profile == "" {
		profile = "(none)"
	}

	fmt.Fprintf(&sb, "host:    %s\n", theme.Secondary.Render(m.ctx.HostName))
	fmt.Fprintf(&sb, "profile: %s\n", theme.Secondary.Render(profile))
	if bootstrapped {
		fmt.Fprintf(&sb, "repo:    %s\n", theme.Hint.Render(m.ctx.State.SyncRepoURL))
		last := m.ctx.State.LastSyncedSHA[profile]
		if last == "" {
			last = "never"
		} else if len(last) > 7 {
			last = last[:7]
		}
		fmt.Fprintf(&sb, "last:    %s", theme.Hint.Render(last))
		if n := countSnapshots(m.ctx); n > 0 {
			fmt.Fprintf(&sb, "   %s", theme.Hint.Render(fmt.Sprintf("· %d snapshot(s)", n)))
		}
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "status:  %s\n", FreshnessBadge(Freshness(m.ctx)))
	} else {
		sb.WriteString(theme.Warn.Render("no sync repo configured — run: ccsync bootstrap --repo <URL>") + "\n")
	}
	sb.WriteString("\n")

	for i, c := range m.choices {
		cursor := "  "
		line := c.label
		shortcut := theme.Hint.Render(fmt.Sprintf("%d ", i+1))
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		if !c.enabled {
			line = theme.Hint.Render(line + " (unavailable)")
		}
		sb.WriteString(cursor + shortcut + line + "\n")
	}
	sb.WriteString("\n" + theme.Hint.Render("↑↓ move • 1-9 jump • enter select"))

	return sb.String()
}

// countSnapshots returns how many pre-sync snapshots live on disk. Used to
// give the Home dashboard a pulse of "something has happened here".
func countSnapshots(ctx *AppContext) int {
	snaps, err := snapshot.List(filepath.Join(ctx.StateDir, "snapshots"))
	if err != nil {
		return 0
	}
	return len(snaps)
}
