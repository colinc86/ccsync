package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/snapshot"
	"github.com/colinc86/ccsync/internal/theme"
)

// homeModel is the dashboard users land on. It renders a status-first view
// rather than a menu: the big visible element is "are you in sync", the
// primary keybind is enter (sync now / start setup), and the rest of the
// app lives behind `m` (more drawer). Power users keep the full screen
// roster; casual users see one action and a status line.
type homeModel struct {
	ctx *AppContext

	// showMore toggles the more-drawer overlay. When true, we intercept
	// keys inside this model (not via AppModel's `?` path) so the drawer
	// can own navigation without fighting global handlers.
	showMore   bool
	moreCursor int
	moreItems  []homeChoice
}

type homeChoice struct {
	label   string
	key     string // single-char shortcut shown in the drawer
	enabled bool
	onEnter func() tea.Cmd
}

func newHome(ctx *AppContext) homeModel {
	return homeModel{ctx: ctx}
}

func (m homeModel) Title() string { return "ccsync" }

func (m homeModel) Init() tea.Cmd { return nil }

// primaryAction returns the command fired by `enter` on the dashboard. Not
// bootstrapped → open the setup flow; bootstrapped → open sync preview
// (which auto-applies when clean, or shows counts when pending).
func (m *homeModel) primaryAction() tea.Cmd {
	if m.ctx.State.SyncRepoURL == "" {
		return switchTo(newBootstrapWizard(m.ctx))
	}
	return switchTo(newSyncPreview(m.ctx))
}

// primaryLabel is the verb we put next to [enter] on the dashboard.
func (m *homeModel) primaryLabel() string {
	if m.ctx.State.SyncRepoURL == "" {
		return "start setup"
	}
	s := m.ctx.Summary()
	if s.Unknown || s.Fetching {
		return "check now"
	}
	if s.Clean() {
		return "re-check now"
	}
	if s.Conflicts > 0 {
		return "resolve & sync"
	}
	return "sync now"
}

// rebuildMoreItems re-derives the drawer entries every frame so freshly-
// created profiles, new suggestions, and bootstrap state changes are
// reflected without needing an explicit refresh.
func (m *homeModel) rebuildMoreItems() {
	bootstrapped := m.ctx.State.SyncRepoURL != ""
	items := []homeChoice{
		{
			key:     "b",
			label:   "Browse tracked files",
			enabled: bootstrapped,
			onEnter: func() tea.Cmd { return switchTo(newBrowseTracked(m.ctx)) },
		},
		{
			key:     "h",
			label:   "History",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newSyncHistory(m.ctx)) },
		},
	}
	if n := countSuggestions(m.ctx); n > 0 {
		items = append(items, homeChoice{
			key:     "g",
			label:   fmt.Sprintf("Suggestions (%d)", n),
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newSuggestions(m.ctx)) },
		})
	}
	// Profiles only shows up in the drawer when there's an actual choice
	// to be made — one profile is the default/invisible case.
	if bootstrapped && len(m.ctx.Config.Profiles) > 1 {
		items = append(items, homeChoice{
			key:     "p",
			label:   "Profiles",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newProfiles(m.ctx)) },
		})
	}
	items = append(items,
		homeChoice{
			key:     "d",
			label:   "Doctor",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newDoctorScreen(m.ctx)) },
		},
		homeChoice{
			key:     "s",
			label:   "Settings",
			enabled: true,
			onEnter: func() tea.Cmd { return switchTo(newSettings(m.ctx)) },
		},
	)
	m.moreItems = items
}

// CapturesEscape lets Home own `esc` while the drawer is open so the
// global handler doesn't bail out of the app when the user's intent is to
// close the drawer.
func (m homeModel) CapturesEscape() bool { return m.showMore }

func (m homeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.rebuildMoreItems()
	if m.moreCursor >= len(m.moreItems) {
		m.moreCursor = 0
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.showMore {
			return m.updateMore(msg)
		}
		return m.updateDashboard(msg)
	}
	return m, nil
}

func (m homeModel) updateDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m, m.primaryAction()
	case "m":
		if m.ctx.State.SyncRepoURL == "" {
			// Drawer contents are all post-setup; pre-bootstrap user has
			// no reason to see them. Keep the interaction minimal.
			return m, nil
		}
		m.showMore = true
		m.moreCursor = 0
		return m, nil
	case "r":
		if m.ctx.State.SyncRepoURL != "" {
			return m, refreshPlanCmd(m.ctx)
		}
	}
	return m, nil
}

func (m homeModel) updateMore(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()

	switch s {
	case "esc", "m":
		m.showMore = false
		return m, nil
	case "up", "k":
		if m.moreCursor > 0 {
			m.moreCursor--
		}
		return m, nil
	case "down", "j":
		if m.moreCursor < len(m.moreItems)-1 {
			m.moreCursor++
		}
		return m, nil
	case "enter":
		if m.moreCursor < len(m.moreItems) {
			c := m.moreItems[m.moreCursor]
			if c.enabled {
				m.showMore = false
				return m, c.onEnter()
			}
		}
		return m, nil
	}

	// Single-letter shortcut match (key column in the drawer).
	if len(s) == 1 {
		for _, item := range m.moreItems {
			if item.key == s && item.enabled {
				m.showMore = false
				return m, item.onEnter()
			}
		}
	}
	return m, nil
}

func (m homeModel) View() string {
	if m.showMore {
		return m.renderMore()
	}
	return m.renderDashboard()
}

func (m homeModel) renderDashboard() string {
	bootstrapped := m.ctx.State.SyncRepoURL != ""
	profile := m.ctx.State.ActiveProfile
	if profile == "" {
		profile = "(none)"
	}

	var sb strings.Builder

	if !bootstrapped {
		sb.WriteString(theme.Warn.Render("no sync repo configured") + "\n\n")
		sb.WriteString(theme.Hint.Render("Point ccsync at a git repo you control.\n"))
		sb.WriteString(theme.Hint.Render("Your Claude Code settings will sync to every machine you bootstrap.") + "\n\n")
		sb.WriteString(theme.Primary.Render("[enter]") + " start setup\n")
		sb.WriteString(theme.Hint.Render("[?] help   [q] quit"))
		return sb.String()
	}

	// Status line — the primary visible element of the dashboard.
	badge := SummaryBadge(m.ctx.Summary(), false)
	if badge == "" {
		badge = theme.Hint.Render("status unknown")
	}
	sb.WriteString(badge + "\n")

	// Sub-status: last synced + snapshot count.
	var subBits []string
	if last := m.ctx.State.LastSyncedSHA[profile]; last != "" {
		short := last
		if len(short) > 7 {
			short = short[:7]
		}
		subBits = append(subBits, "last synced "+short)
	} else {
		subBits = append(subBits, "never synced")
	}
	if n := countSnapshots(m.ctx); n > 0 {
		subBits = append(subBits, fmt.Sprintf("%d snapshot(s)", n))
	}
	sb.WriteString(theme.Hint.Render(strings.Join(subBits, " · ")) + "\n\n")

	// Detail — still shown on home so users have the context at a glance,
	// but visually demoted. The old home made these the primary content.
	fmt.Fprintf(&sb, "%s  %s\n", theme.Hint.Render("host   "), theme.Secondary.Render(m.ctx.HostName))
	fmt.Fprintf(&sb, "%s  %s\n", theme.Hint.Render("profile"), theme.Secondary.Render(profile))
	fmt.Fprintf(&sb, "%s  %s\n", theme.Hint.Render("repo   "), theme.Secondary.Render(m.ctx.State.SyncRepoURL))
	sb.WriteString("\n")

	// Actions.
	fmt.Fprintf(&sb, "%s %s\n", theme.Primary.Render("[enter]"), m.primaryLabel())
	fmt.Fprintf(&sb, "%s    %s\n", theme.Primary.Render("[m]"), "more")
	sb.WriteString(theme.Hint.Render("[r] re-check   [?] help   [q] quit"))
	return sb.String()
}

func (m homeModel) renderMore() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("more") + "\n\n")
	for i, c := range m.moreItems {
		cursor := "  "
		if i == m.moreCursor {
			cursor = theme.Primary.Render("▸ ")
		}
		label := c.label
		if !c.enabled {
			label = theme.Hint.Render(label + " (unavailable)")
		}
		key := theme.Primary.Render(fmt.Sprintf("[%s]", c.key))
		fmt.Fprintf(&sb, "%s%s  %s\n", cursor, key, label)
	}
	sb.WriteString("\n" + theme.Hint.Render("↑↓ move · enter open · esc/m close"))

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Accent).
		Padding(1, 2).
		Render(sb.String())
	return panel
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
