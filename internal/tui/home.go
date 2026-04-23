package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/humanize"
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
			key:     "i",
			label:   "What's syncing",
			enabled: bootstrapped,
			onEnter: func() tea.Cmd { return switchTo(newProfileInspect(m.ctx)) },
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
	// Profiles is always in the drawer once we're bootstrapped — the
	// v0.3 "hide when only one profile" rule was wrong because it
	// locked users out of the screen they needed to *create* a second
	// profile. Clutter tradeoff was small; lockout tradeoff wasn't.
	if bootstrapped {
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
		m.moreCursor = wrapCursor(m.moreCursor, len(m.moreItems), -1)
		return m, nil
	case "down", "j":
		m.moreCursor = wrapCursor(m.moreCursor, len(m.moreItems), +1)
		return m, nil
	case "enter":
		if m.moreCursor < len(m.moreItems) {
			c := m.moreItems[m.moreCursor]
			if c.enabled {
				// Leave showMore=true so when the user hits esc from the
				// sub-screen they land back on the drawer, not naked Home.
				// The drawer is dismissible with another esc/m once they're
				// actually done with the "more" flow.
				return m, c.onEnter()
			}
		}
		return m, nil
	}

	// Single-letter shortcut match (key column in the drawer).
	if len(s) == 1 {
		for _, item := range m.moreItems {
			if item.key == s && item.enabled {
				// Same rationale as the enter branch: preserve the drawer
				// so esc-back restores the navigation context the user
				// actually came from.
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

	// Wordmark header — same identity element across home / onboarding
	// so the TUI always tells the user "yes, this is ccsync".
	sb.WriteString(theme.Wordmark("Claude Code settings sync") + "\n\n")

	if !bootstrapped {
		sb.WriteString(renderHeroCard(heroSpec{
			glyph:   "◦",
			title:   "NOT CONFIGURED",
			subtext: "point ccsync at a git repo you control — your Claude Code settings\nwill sync to every machine you bootstrap",
			state:   heroNeutral,
		}) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "enter", label: "start setup"},
			{cap: "?", label: "help"},
			{cap: "q", label: "quit"},
		}))
		return sb.String()
	}

	// Hero status card — the first thing the user parses on every
	// launch, so it carries the most weight. State-reactive border
	// (green/orange/red/muted) lets the eye confirm sync health
	// before reading a single word.
	s := m.ctx.Summary()
	sb.WriteString(renderHeroCard(heroFromSummary(s, m.ctx, profile)) + "\n\n")

	// Specific-file preview of what the next sync will do. Only render
	// when we have a fresh plan and non-trivial actions — the hero card
	// already shows counts; this line names files so users can decide
	// whether to sync without drilling into the preview screen.
	if !m.ctx.Fetching && m.ctx.Plan != nil && !s.Clean() {
		if preview := renderSyncPreview(*m.ctx.Plan); preview != "" {
			sb.WriteString(theme.Hint.Render("next: "+preview) + "\n\n")
		}
	}

	// Detail strip — one line per field, bullet-prefixed, aligned so
	// the values are eye-scannable. Shorter than the old three-line
	// layout and reads as a "metadata panel" rather than a data dump.
	fmt.Fprintf(&sb, " %s %-7s %s\n",
		theme.Rule.Render("·"), theme.Hint.Render("host"), theme.Secondary.Render(m.ctx.HostName))
	fmt.Fprintf(&sb, " %s %-7s %s\n",
		theme.Rule.Render("·"), theme.Hint.Render("profile"), theme.Secondary.Render("◉ "+profile))
	fmt.Fprintf(&sb, " %s %-7s %s\n",
		theme.Rule.Render("·"), theme.Hint.Render("repo"), theme.Secondary.Render(m.ctx.State.SyncRepoURL))
	sb.WriteString("\n")

	// Footer bar — keycap-styled shortcuts. Primary action first, pill-
	// highlighted; secondary actions as muted chips. Replaces the old
	// "[enter] sync now / [m] more" wall of brackets.
	sb.WriteString(renderFooterBar([]footerKey{
		{cap: "enter", label: m.primaryLabel()},
		{cap: "m", label: "more"},
		{cap: "r", label: "re-check"},
		{cap: "?", label: "help"},
		{cap: "q", label: "quit"},
	}))
	return sb.String()
}

// heroState drives the hero-card border colour and the glyph/title
// wording. Keeps renderHeroCard a pure function of (state, copy).
type heroState int

const (
	heroClean heroState = iota
	heroPending
	heroConflict
	heroFetching
	heroNeutral
)

type heroSpec struct {
	glyph   string // leading rune — ✓ / ↑ / ! / ◦ / spinner frame
	title   string // short caps headline — IN SYNC / 3 PENDING / CONFLICT
	subtext string // one or two muted lines below the title
	state   heroState
}

// heroFromSummary translates a SyncSummary into the renderable hero
// spec — glyph, title, sub-lines, state colour. Keeps all the "what
// does this mean visually" decisions in one place so future state
// additions (e.g. "migrating encryption") drop in cleanly.
func heroFromSummary(s SyncSummary, ctx *AppContext, profile string) heroSpec {
	switch {
	case s.Fetching:
		return heroSpec{
			glyph: "◌", title: "CHECKING", state: heroFetching,
			subtext: "reading the remote and comparing with your machine…",
		}
	case s.FetchErr != nil:
		return heroSpec{
			glyph: "!", title: "FETCH FAILED", state: heroConflict,
			subtext: s.FetchErr.Error(),
		}
	case s.Unknown:
		return heroSpec{
			glyph: "◦", title: "STATUS UNKNOWN", state: heroNeutral,
			subtext: "press r to check the remote, or enter to open the sync preview",
		}
	case s.Conflicts > 0:
		return heroSpec{
			glyph: "!", title: fmt.Sprintf("%d CONFLICT", s.Conflicts),
			state:   heroConflict,
			subtext: heroFreshnessLine(ctx, profile),
		}
	case s.Clean():
		return heroSpec{
			glyph: "✓", title: "IN SYNC", state: heroClean,
			subtext: heroFreshnessLine(ctx, profile),
		}
	}
	// Pending push/pull counts.
	parts := []string{}
	if s.Outbound > 0 {
		parts = append(parts, fmt.Sprintf("↑ %d push", s.Outbound))
	}
	if s.Inbound > 0 {
		parts = append(parts, fmt.Sprintf("↓ %d pull", s.Inbound))
	}
	title := strings.Join(parts, "  ·  ")
	return heroSpec{
		glyph: "↻", title: strings.ToUpper(title), state: heroPending,
		subtext: heroFreshnessLine(ctx, profile),
	}
}

// heroFreshnessLine builds the "last synced X · N snapshots" sub-
// line. Prefer state.LastSyncedAt; fall back to the snapshot-activity
// proxy for pre-v0.6.5 bootstraps. Empty for never-synced profiles.
func heroFreshnessLine(ctx *AppContext, profile string) string {
	last := ctx.State.LastSyncedSHA[profile]
	var bits []string
	if last == "" {
		bits = append(bits, "never synced")
	} else {
		ts := ctx.State.LastSyncedAt[profile]
		if ts.IsZero() {
			if proxy, ok := lastSyncActivityTime(ctx, profile); ok {
				ts = proxy
			}
		}
		if ago := humanize.Ago(ts); ago != "" {
			bits = append(bits, "last synced "+ago)
		} else {
			short := last
			if len(short) > 7 {
				short = short[:7]
			}
			bits = append(bits, "last synced "+short)
		}
	}
	if n := countSnapshots(ctx); n > 0 {
		bits = append(bits, humanize.Count(n, "snapshot"))
	}
	return strings.Join(bits, "  ·  ")
}

// renderHeroCard draws the centerpiece status panel — big glyph and
// caps headline, muted sub-copy, state-colored rounded border. The
// card's minimum width keeps the sub-text line readable without
// wrapping at common terminal widths; lipgloss rounds up if the
// content demands more.
func renderHeroCard(h heroSpec) string {
	var glyphStyle lipgloss.Style
	var titleStyle lipgloss.Style
	var card lipgloss.Style
	switch h.state {
	case heroClean:
		glyphStyle = theme.Good.Bold(true)
		titleStyle = theme.Good.Bold(true)
		card = theme.CardClean
	case heroPending:
		glyphStyle = theme.Warn.Bold(true)
		titleStyle = theme.Warn.Bold(true)
		card = theme.CardPending
	case heroConflict:
		glyphStyle = theme.Bad
		titleStyle = theme.Bad
		card = theme.CardConflict
	case heroFetching:
		glyphStyle = theme.Subtle.Bold(true)
		titleStyle = theme.Subtle.Bold(true)
		card = theme.CardNeutral
	default:
		glyphStyle = theme.Subtle.Bold(true)
		titleStyle = theme.Subtle.Bold(true)
		card = theme.CardNeutral
	}
	line1 := glyphStyle.Render(h.glyph) + "  " + titleStyle.Render(h.title)
	var sb strings.Builder
	sb.WriteString(line1)
	if h.subtext != "" {
		sb.WriteString("\n")
		sb.WriteString(theme.Hint.Render(h.subtext))
	}
	return card.Width(56).Render(sb.String())
}

// footerKey describes one key in the persistent shortcut bar. Every
// key renders identically — bold accent glyph + muted label — so no
// one key looks more "themed" than the rest.
type footerKey struct {
	cap   string
	label string
}

// renderFooterBar renders the bottom-of-screen shortcut row. Keys
// render as bold accent-color glyphs followed by their muted-text
// label; entries join with a thin dot separator so the row reads as
// one continuous legend, not a cluttered list.
func renderFooterBar(keys []footerKey) string {
	var parts []string
	for _, k := range keys {
		parts = append(parts, theme.Keycap.Render(k.cap)+" "+theme.Hint.Render(k.label))
	}
	return strings.Join(parts, theme.Rule.Render("  ·  "))
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

// lastSyncActivityTime returns the most recent snapshot's CreatedAt for
// the given profile, or (zero, false) when no snapshot matches.
//
// Snapshots are taken pre-sync-write when pendingLocalWrites is
// non-empty, so this proxies "when did sync last touch this profile".
// It undercounts push-only syncs (no local writes → no snapshot) —
// good enough for the dashboard's "X hours ago" line, but callers that
// need exact last-sync time should read a proper LastSyncedAt field
// (not yet present on state.State).
func lastSyncActivityTime(ctx *AppContext, profile string) (time.Time, bool) {
	snaps, err := snapshot.List(filepath.Join(ctx.StateDir, "snapshots"))
	if err != nil {
		return time.Time{}, false
	}
	for _, s := range snaps {
		if s.Profile != profile {
			continue
		}
		// snapshot.List returns newest first, so the first match wins.
		return s.CreatedAt, true
	}
	return time.Time{}, false
}
