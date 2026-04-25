package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/humanize"
	"github.com/colinc86/ccsync/internal/profileinspect"
	"github.com/colinc86/ccsync/internal/theme"
	"github.com/colinc86/ccsync/internal/why"
)

// profileInspectModel is the unified "what's syncing" screen. Before
// v0.8 this was a read-only inventory and the companion Browse
// Tracked screen carried the exclude / .syncignore / promote
// controls. Users complained that the two views answered the same
// question from two different angles, so the controls were folded in
// here and the path-level browse was dropped.
//
// The view still groups items by Kind (Skills, Commands, Agents, MCP
// servers, Memory, Settings, CLAUDE.md) but every row is now an
// action surface: a checkbox reflects the effective include/exclude
// status for the active profile, space toggles the exclude rule, `i`
// opens the .syncignore flow, `w` traces rule provenance, `p`
// promotes to the default profile. MCP servers live inside
// claude.json — exclude-style ops don't apply to them and are
// silently skipped rather than showing confusing error toasts.
type profileInspectModel struct {
	ctx    *AppContext
	view   *profileinspect.View
	err    error
	cursor int
	// flat mirrors Sections's Items so cursor indexing doesn't have
	// to recompute (section-idx, item-idx) pairs on every keystroke.
	flat []flatRow
	// visible is the slice of flat-row indices that pass the current
	// filter. Rebuilt whenever filterIn changes. Headers whose items
	// all get filtered out disappear with them.
	visible []int
	// message is a one-shot success banner ("excluded: foo", "added
	// to .syncignore: bar"). Cleared on the next keystroke so stale
	// notices don't linger under fresh navigation.
	message string

	filtering bool
	filterIn  textinput.Model

	// syncignore flow — triggered by `i` on a highlighted row.
	ignoring     ignoreStage
	ignoreTarget string // the path the user chose to act on
	ignoreChoice int    // cursor in the choose-menu
	patIn        textinput.Model

	// promotingPath is non-empty while waiting for y/N confirmation.
	// promoting latches once the PromotePath cmd is in flight so a
	// second y press can't race-dispatch a parallel commit.
	promotingPath string
	promoting     bool
}

// flatRow is one renderable row in the flattened Sections tree —
// either a section header (no item) or a data row. Headers are
// non-selectable; cursor skips over them so ↑↓ feels linear.
type flatRow struct {
	Header  bool
	Section profileinspect.Section
	Item    profileinspect.Item
}

func newProfileInspect(ctx *AppContext) *profileInspectModel {
	filterIn := textinput.New()
	filterIn.Placeholder = "filter…"
	filterIn.CharLimit = 48
	filterIn.Width = 32

	patIn := textinput.New()
	patIn.CharLimit = 96
	patIn.Width = 40

	m := &profileInspectModel{
		ctx:      ctx,
		filterIn: filterIn,
		patIn:    patIn,
	}
	m.reload()
	return m
}

func (m *profileInspectModel) Title() string { return "What's syncing" }
func (m *profileInspectModel) Init() tea.Cmd { return nil }

// CapturesEscape keeps esc scoped to cancelling a sub-flow (filter,
// ignore picker, promote confirmation) rather than popping the
// whole screen. When no sub-flow is active, esc falls through to
// the global pop-one behaviour.
func (m *profileInspectModel) CapturesEscape() bool {
	return m.filtering || m.ignoring != ignoreOff || m.promotingPath != ""
}

// reload rebuilds the view from the current state. Called on open,
// on `r`, and after any mutation that changes effective exclusion.
// Network-free — Inspect is pure local I/O.
func (m *profileInspectModel) reload() {
	v, err := profileinspect.Inspect(profileinspect.Inputs{
		Config:     m.ctx.Config,
		State:      m.ctx.State,
		ClaudeDir:  m.ctx.ClaudeDir,
		ClaudeJSON: m.ctx.ClaudeJSON,
		RepoPath:   m.ctx.RepoPath,
	})
	m.err = err
	m.view = v
	m.flat = flatten(v)
	m.applyFilter()
}

// flatten walks a View and produces the renderable row list. Each
// Section contributes one Header row + one row per Item. The caller
// skips Header rows in cursor navigation, so the layout stays
// section-aware without a second data structure.
func flatten(v *profileinspect.View) []flatRow {
	if v == nil {
		return nil
	}
	var out []flatRow
	for _, s := range v.Sections {
		out = append(out, flatRow{Header: true, Section: s})
		for _, it := range s.Items {
			out = append(out, flatRow{Item: it, Section: s})
		}
	}
	return out
}

// applyFilter rebuilds m.visible from the current filter query.
// When the query is empty, visible mirrors m.flat. Otherwise each
// section is kept only if at least one of its items matches; the
// header tags along with its surviving items so the layout stays
// grouped.
func (m *profileInspectModel) applyFilter() {
	m.visible = m.visible[:0]
	q := strings.ToLower(strings.TrimSpace(m.filterIn.Value()))
	if q == "" {
		for i := range m.flat {
			m.visible = append(m.visible, i)
		}
		m.clampCursor()
		return
	}

	// Two-pass: collect item indices that match the query, then
	// include their section headers ahead of them.
	headerFor := map[int]int{} // item-idx → preceding-header-idx
	lastHeader := -1
	for i, r := range m.flat {
		if r.Header {
			lastHeader = i
			continue
		}
		headerFor[i] = lastHeader
	}

	seenHeader := map[int]bool{}
	for i, r := range m.flat {
		if r.Header {
			continue
		}
		title := strings.ToLower(r.Item.Title)
		desc := strings.ToLower(r.Item.Description)
		if !strings.Contains(title, q) && !strings.Contains(desc, q) {
			continue
		}
		if h, ok := headerFor[i]; ok && h >= 0 && !seenHeader[h] {
			m.visible = append(m.visible, h)
			seenHeader[h] = true
		}
		m.visible = append(m.visible, i)
	}
	m.clampCursor()
}

// clampCursor makes sure m.cursor points at a visible, non-header
// row. If the current position falls outside the filtered list, it
// snaps to the first selectable row (or -1 for an empty filter).
func (m *profileInspectModel) clampCursor() {
	if len(m.visible) == 0 {
		m.cursor = -1
		return
	}
	// Try to preserve position if still visible.
	for _, idx := range m.visible {
		if idx == m.cursor && !m.flat[idx].Header {
			return
		}
	}
	// Otherwise land on the first non-header visible row.
	for _, idx := range m.visible {
		if !m.flat[idx].Header {
			m.cursor = idx
			return
		}
	}
	m.cursor = -1
}

// nudgeCursor moves to the next/prev visible non-header row with
// wrap-around at the top and bottom. Header rows are invisible to
// the cursor, so ↑↓ feels linear across section boundaries; after
// wrap, the next header also gets skipped. nudge(0) is a
// normalise-only call: if the cursor is out of range or resting on
// a header (e.g. first entry to the screen), it snaps to the first
// selectable row without moving past it.
func (m *profileInspectModel) nudgeCursor(delta int) {
	m.ensureVisible()
	if len(m.visible) == 0 {
		return
	}
	// Map m.cursor to its position in visible.
	pos := -1
	for i, idx := range m.visible {
		if idx == m.cursor {
			pos = i
			break
		}
	}
	if pos < 0 || m.flat[m.cursor].Header {
		// Cursor not in visible set or resting on a header. Snap
		// onto the first selectable row.
		for _, idx := range m.visible {
			if !m.flat[idx].Header {
				m.cursor = idx
				return
			}
		}
		return
	}
	if delta == 0 {
		return
	}
	// Wrap-aware walk: step at most len(visible) positions to find
	// the next non-header, circling around at the bounds. Bounded
	// loop count means we can't infinite-loop even when every
	// visible row is a header (shouldn't happen post-filter).
	target := pos
	for step := 0; step < len(m.visible); step++ {
		target = wrapCursor(target, len(m.visible), delta)
		if !m.flat[m.visible[target]].Header {
			m.cursor = m.visible[target]
			return
		}
	}
}

// ensureVisible lazily populates m.visible the first time it's
// needed. Tests (and any code path that constructs the model
// directly without going through newProfileInspect) may set m.flat
// without triggering applyFilter; this guard keeps rendering and
// navigation correct in those cases.
func (m *profileInspectModel) ensureVisible() {
	if m.visible != nil || len(m.flat) == 0 {
		return
	}
	m.applyFilter()
}

// cursorItem returns the currently-highlighted item, or nil if the
// cursor is off (empty list or hovering a header — the latter
// shouldn't happen post-clamp but we guard anyway).
func (m *profileInspectModel) cursorItem() *profileinspect.Item {
	if m.cursor < 0 || m.cursor >= len(m.flat) {
		return nil
	}
	r := m.flat[m.cursor]
	if r.Header {
		return nil
	}
	return &r.Item
}

// Update drives the screen's state machine. Sub-flows (filter,
// ignore picker, promote confirm) intercept keys via the switch at
// the top; the main navigation runs when no sub-flow is active.
func (m *profileInspectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case promoteDoneMsg:
		m.promoting = false
		if msg.err != nil {
			m.err = msg.err
			return m, showToast("promote failed: "+msg.err.Error(), toastError)
		}
		m.message = ""
		m.reload()
		return m, showToast("promoted to default · shared across profiles", toastSuccess)

	case tea.KeyMsg:
		if m.filtering {
			switch msg.String() {
			case "enter", "esc":
				m.filtering = false
				m.filterIn.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.filterIn, cmd = m.filterIn.Update(msg)
			m.applyFilter()
			return m, cmd
		}
		if m.ignoring != ignoreOff {
			return m.updateIgnore(msg)
		}
		if m.promotingPath != "" {
			switch msg.String() {
			case "y":
				target := m.promotingPath
				m.promotingPath = ""
				m.promoting = true
				return m, runPromote(m.ctx, target)
			case "n", "esc":
				m.promotingPath = ""
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "up", "k":
			m.nudgeCursor(-1)
			m.message = ""
		case "down", "j":
			m.nudgeCursor(1)
			m.message = ""
		case "r":
			m.reload()
		case "/":
			m.filtering = true
			m.filterIn.Focus()
			return m, textinput.Blink
		case "c":
			m.filterIn.SetValue("")
			m.applyFilter()
		case " ":
			return m, m.toggleExcludeCursor()
		case "i":
			it := m.cursorItem()
			if it == nil || it.Kind == profileinspect.KindMCPServer {
				return m, nil
			}
			m.ignoring = ignoreChoose
			m.ignoreTarget = it.Path
			m.ignoreChoice = 0
			m.err = nil
			return m, nil
		case "p":
			it := m.cursorItem()
			if it == nil || it.Kind == profileinspect.KindMCPServer {
				return m, nil
			}
			active := m.ctx.State.ActiveProfile
			if active == "" || active == "default" {
				m.err = fmt.Errorf("already on the default profile — nothing to promote")
				return m, nil
			}
			m.promotingPath = it.Path
			m.err = nil
			return m, nil
		case "w":
			it := m.cursorItem()
			if it == nil {
				return m, nil
			}
			target := whyTargetForItem(*it)
			syncignore := m.ctx.Config.DefaultSyncignore
			if data, err := os.ReadFile(filepath.Join(m.ctx.RepoPath, ".syncignore")); err == nil {
				syncignore = string(data)
			}
			tr, werr := why.Explain(why.Inputs{
				Config: m.ctx.Config, Profile: m.ctx.State.ActiveProfile,
				Syncignore: syncignore,
				ClaudeDir:  m.ctx.ClaudeDir, ClaudeJSON: m.ctx.ClaudeJSON,
			}, target)
			if werr != nil {
				m.err = werr
				return m, nil
			}
			m.message = "\n" + why.Format(tr)
		}
	}
	return m, nil
}

// toggleExcludeCursor flips the active profile's exclusion rule for
// the highlighted item's path. Skill directories toggle the whole
// subtree via a `**` glob; everything else toggles the exact path.
// Already-present rules in the active profile are removed on
// toggle; otherwise they're appended. MCP servers don't have a
// file-backed exclusion surface and are silently skipped.
func (m *profileInspectModel) toggleExcludeCursor() tea.Cmd {
	it := m.cursorItem()
	if it == nil || it.Kind == profileinspect.KindMCPServer {
		return nil
	}
	pat := patternForPath(it.Path)

	profName := m.ctx.State.ActiveProfile
	spec, ok := m.ctx.Config.Profiles[profName]
	if !ok {
		m.err = fmt.Errorf("profile %q not found", profName)
		return nil
	}
	if spec.Exclude == nil {
		spec.Exclude = &config.ProfileExclude{}
	}
	paths := spec.Exclude.Paths

	// Was the exact pattern already on the active profile? If so,
	// treat the toggle as an un-exclude.
	removeIdx := -1
	for i, p := range paths {
		if p == pat {
			removeIdx = i
			break
		}
	}

	if removeIdx >= 0 {
		spec.Exclude.Paths = append(paths[:removeIdx], paths[removeIdx+1:]...)
		m.message = fmt.Sprintf("re-included: %s", it.Path)
	} else {
		// Inherited rules can't be overridden with an add; tell the
		// user to edit ccsync.yaml instead of silently duplicating.
		if it.Status == profileinspect.StatusExcluded {
			m.err = fmt.Errorf("excluded by an inherited rule; edit ccsync.yaml to change")
			return nil
		}
		spec.Exclude.Paths = append(paths, pat)
		m.message = fmt.Sprintf("excluded: %s (rule %q added to %q)", it.Path, pat, profName)
	}

	// yaml's omitempty doesn't fire on non-nil pointers to empty
	// structs, so we'd serialize `exclude: {}` otherwise. Null it
	// out when empty so ccsync.yaml stays tidy.
	if spec.Exclude != nil && len(spec.Exclude.Paths) == 0 {
		spec.Exclude = nil
	}

	m.ctx.Config.Profiles[profName] = spec
	if err := m.ctx.Config.SaveWithBackup(m.ctx.ConfigPath()); err != nil {
		m.err = err
		return nil
	}
	m.err = nil
	m.reload()
	return nil
}

// ignoreOptions builds the menu for the current ignoreTarget. Keep
// this in sync with renderIgnoreFlow and updateIgnore; all three
// iterate the same slice.
func (m *profileInspectModel) ignoreOptions() []ignoreOption {
	pathPat := syncignoreRel(m.ignoreTarget)
	parentPat := parentSyncignorePattern(m.ignoreTarget)
	extPat := defaultExtensionPattern(m.ignoreTarget)
	opts := []ignoreOption{
		{
			label: "this exact path", preview: pathPat, enabled: true,
			run: func() tea.Cmd { return m.applyIgnore(pathPat) },
		},
	}
	if parentPat != "" {
		opts = append(opts, ignoreOption{
			label: "parent directory", preview: parentPat, enabled: true,
			run: func() tea.Cmd { return m.applyIgnore(parentPat) },
		})
	} else {
		opts = append(opts, ignoreOption{
			label: "parent directory", hint: "(top-level file has no parent)",
		})
	}
	opts = append(opts, ignoreOption{
		label: "pattern…", preview: "starts at " + extPat, enabled: true,
		run: func() tea.Cmd {
			m.ignoring = ignorePattern
			m.patIn.SetValue(extPat)
			m.patIn.CursorEnd()
			m.patIn.Focus()
			return textinput.Blink
		},
	})
	return opts
}

// updateIgnore drives the "add a rule to .syncignore" flow: a small
// picker (path / parent-dir / pattern) followed by an optional
// pattern edit.
func (m *profileInspectModel) updateIgnore(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ignoring == ignorePattern {
		switch msg.String() {
		case "esc":
			m.ignoring = ignoreChoose
			m.patIn.Blur()
			return m, nil
		case "enter":
			pat := strings.TrimSpace(m.patIn.Value())
			if pat == "" {
				m.err = fmt.Errorf("pattern required")
				return m, nil
			}
			return m, m.applyIgnore(pat)
		}
		var cmd tea.Cmd
		m.patIn, cmd = m.patIn.Update(msg)
		return m, cmd
	}

	opts := m.ignoreOptions()
	switch msg.String() {
	case "esc":
		m.ignoring = ignoreOff
		m.ignoreTarget = ""
		return m, nil
	case "up", "k":
		m.ignoreChoice = prevEnabled(opts, m.ignoreChoice)
		return m, nil
	case "down", "j":
		m.ignoreChoice = nextEnabled(opts, m.ignoreChoice)
		return m, nil
	case "enter":
		if m.ignoreChoice < 0 || m.ignoreChoice >= len(opts) {
			return m, nil
		}
		opt := opts[m.ignoreChoice]
		if !opt.enabled || opt.run == nil {
			return m, nil
		}
		return m, opt.run()
	}
	if len(msg.String()) == 1 {
		c := msg.String()[0]
		if c >= '1' && c <= '9' {
			idx := int(c - '1')
			if idx >= 0 && idx < len(opts) && opts[idx].enabled && opts[idx].run != nil {
				m.ignoreChoice = idx
				return m, opts[idx].run()
			}
		}
	}
	return m, nil
}

// applyIgnore appends pattern to .syncignore, exits the flow, and
// reloads the view so the newly-ignored entries update their
// status.
func (m *profileInspectModel) applyIgnore(pattern string) tea.Cmd {
	path := filepath.Join(m.ctx.RepoPath, ".syncignore")
	if err := appendSyncignore(path, pattern); err != nil {
		m.err = err
		return nil
	}
	m.err = nil
	m.message = fmt.Sprintf("added to .syncignore: %s", pattern)
	m.ignoring = ignoreOff
	m.ignoreTarget = ""
	m.patIn.Blur()
	m.reload()
	return nil
}

func (m *profileInspectModel) View() string {
	var sb strings.Builder

	if m.promoting {
		card := theme.CardPending.Width(56).Render(
			theme.Warn.Bold(true).Render("◌ PROMOTING") + "\n" +
				theme.Hint.Render("committing the move and pushing to the remote…"))
		return card
	}

	if m.err != nil {
		sb.WriteString(renderError(m.err) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render("✓ "+m.message) + "\n\n")
	}

	if m.promotingPath != "" {
		var body strings.Builder
		body.WriteString(theme.Warn.Bold(true).Render("↗ PROMOTE") + "  " +
			theme.Subtle.Render(m.promotingPath) + "\n\n")
		body.WriteString(theme.Hint.Render(
			"moves this file from profiles/" + m.ctx.State.ActiveProfile + "/\n" +
				"to profiles/default/ in the repo so every profile\n" +
				"that extends default picks it up on next sync."))
		sb.WriteString(theme.CardPending.Width(60).Render(body.String()) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "y", label: "confirm"},
			{cap: "n", label: "cancel"},
		}))
		return sb.String()
	}

	if m.ignoring != ignoreOff {
		return sb.String() + m.renderIgnoreFlow()
	}

	// Empty-state hero card: fresh bootstrap, nothing on either
	// side yet. Lands on something reassuring + actionable.
	if m.view == nil || m.view.Empty() {
		body := theme.Subtle.Bold(true).Render("◦ NOTHING SYNCED YET") + "\n" +
			theme.Hint.Render(
				"add a skill under ~/.claude/skills/, a command under\n"+
					"~/.claude/commands/, or an MCP server in ~/.claude.json\n"+
					"— then run sync. this screen will show what's flowing.")
		sb.WriteString(theme.CardNeutral.Width(60).Render(body) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "r", label: "refresh"},
			{cap: "esc", label: "back"},
		}))
		return sb.String()
	}

	// Summary chips — total + per-section counts in a dot-separated
	// row at top. Lets the user see the inventory shape before
	// scanning rows.
	sb.WriteString(renderInspectorChipRow(m.view) + "\n\n")

	m.ensureVisible()
	if m.filtering || m.filterIn.Value() != "" {
		sb.WriteString(theme.Secondary.Render("filter: ") + m.filterIn.View())
		fmt.Fprintf(&sb, "  %s\n\n",
			theme.Hint.Render(fmt.Sprintf("(%d matches)", countItemRows(m.visible, m.flat))))
	}

	if len(m.visible) == 0 && m.filterIn.Value() != "" {
		sb.WriteString(theme.Hint.Render("no matches — press c to clear filter"))
		sb.WriteString("\n\n" + renderFooterBar([]footerKey{
			{cap: "/", label: "filter"},
			{cap: "c", label: "clear"},
			{cap: "esc", label: "back"},
		}))
		return sb.String()
	}

	// Grouped sections. Only render headers whose section still has
	// visible items; the filter pass in applyFilter already dropped
	// sections with no surviving children.
	termWidth := 0
	// termWidth isn't plumbed through AppContext to this screen
	// today; descriptions and titles get a conservative cap below.
	_ = termWidth
	for _, idx := range m.visible {
		row := m.flat[idx]
		if row.Header {
			// Blank line between sections — but skip the very first
			// one to keep the chip row snug against the initial
			// header.
			if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n\n") {
				sb.WriteString("\n")
			}
			sb.WriteString(theme.Secondary.Bold(true).Render(
				fmt.Sprintf("%s (%d)", row.Section.Label, len(row.Section.Items))))
			sb.WriteString("\n")
			sb.WriteString(theme.Rule.Render(strings.Repeat("─", 28)) + "\n")
			continue
		}
		cursor := "  "
		if idx == m.cursor {
			cursor = theme.Primary.Render("▸ ")
		}
		sb.WriteString(cursor + renderInspectorRow(row.Item) + "\n")
	}

	sb.WriteString("\n" + renderFooterBar([]footerKey{
		{cap: "↑↓", label: "move"},
		{cap: "space", label: "toggle"},
		{cap: "i", label: "syncignore"},
		{cap: "p", label: "promote"},
		{cap: "w", label: "why"},
		{cap: "/", label: "filter"},
		{cap: "r", label: "refresh"},
	}))
	return sb.String()
}

// whyTargetForItem translates a profileinspect.Item.Path into the
// target string why.Explain accepts. Regular rel-paths pass through
// unchanged; MCP-server synthetic paths ("claude.json#mcpServers.x")
// convert to the file+json-key form why.Explain parses via
// splitJSONTarget ("claude.json:$.mcpServers.x") so the trace can
// actually reach the per-key rule lookup in jsonFiles rules.
func whyTargetForItem(it profileinspect.Item) string {
	if hashIdx := strings.Index(it.Path, "#"); hashIdx > 0 {
		file := it.Path[:hashIdx]
		key := it.Path[hashIdx+1:]
		return file + ":$." + key
	}
	return it.Path
}

// countItemRows returns how many non-header rows live in visible —
// used for the "(N matches)" hint next to the filter field.
func countItemRows(visible []int, flat []flatRow) int {
	n := 0
	for _, idx := range visible {
		if idx >= 0 && idx < len(flat) && !flat[idx].Header {
			n++
		}
	}
	return n
}

// renderIgnoreFlow returns the UI for the per-row "add to
// .syncignore" action: a cursor-driven picker, plus an optional
// textinput for the pattern branch.
func (m *profileInspectModel) renderIgnoreFlow() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("add to .syncignore") + "\n\n")
	fmt.Fprintf(&sb, "  %s  %s\n\n", theme.Secondary.Render("target:"), m.ignoreTarget)

	if m.ignoring == ignoreChoose {
		for i, opt := range m.ignoreOptions() {
			cursor := "  "
			if i == m.ignoreChoice {
				cursor = theme.Primary.Render("▸ ")
			}
			label := opt.label
			detail := ""
			switch {
			case opt.hint != "":
				detail = theme.Hint.Render(opt.hint)
			case opt.preview != "":
				detail = theme.Hint.Render("→ " + opt.preview)
			}
			if !opt.enabled {
				label = theme.Hint.Render(label)
			}
			fmt.Fprintf(&sb, "%s%s  %-20s  %s\n", cursor,
				theme.Hint.Render(fmt.Sprintf("%d", i+1)), label, detail)
		}
		sb.WriteString("\n" + theme.Hint.Render("↑↓ move • enter select • 1-3 jump • esc cancel"))
		return sb.String()
	}

	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("pattern:"), m.patIn.View())
	sb.WriteString("\n" + theme.Hint.Render("enter save • esc back"))
	return sb.String()
}

// renderInspectorChipRow builds the "5 skills · 3 commands · 2
// agents · 1 mcp" summary strip. Ordered by sectionOrder (the same
// order Sections render in) so the pill sequence matches the layout
// below.
func renderInspectorChipRow(v *profileinspect.View) string {
	if v == nil {
		return ""
	}
	var chips []string
	for _, s := range v.Sections {
		n := len(s.Items)
		if n == 0 {
			continue
		}
		chips = append(chips, theme.ChipNeutral.Render(
			fmt.Sprintf("%d %s", n, strings.ToLower(s.Label))))
	}
	return strings.Join(chips, theme.Rule.Render("  ·  "))
}

// renderInspectorRow returns a single item row: checkbox, leading
// glyph, title, em-dashed description, status chip at the right.
// Description gets truncated so the status chip stays visible on
// narrow terminals — the previous unbounded rendering pushed
// "synced / pending push" off-screen on long paths.
func renderInspectorRow(it profileinspect.Item) string {
	box := theme.Good.Render("☑")
	if it.Status == profileinspect.StatusExcluded {
		box = theme.Hint.Render("☐")
	}
	glyph := inspectorGlyph(it.Kind)
	title := theme.Primary.Render(humanize.Truncate(it.Title, 40))
	if it.Kind == profileinspect.KindCommand {
		title = theme.Primary.Render("/" + humanize.Truncate(it.Title, 39))
	}
	desc := ""
	if it.Description != "" {
		desc = "  " + theme.Hint.Render("— "+humanize.Truncate(it.Description, 60))
	}
	chip := inspectorStatusChip(it.Status)
	return box + " " + glyph + " " + title + desc + "  " + chip
}

// inspectorGlyph is the leading character next to each item row,
// hinting at what kind of thing it is. Bolded in a kind-specific
// accent so it reads as "the skills rows, the MCP rows" even when
// the user is scrolling fast.
func inspectorGlyph(k profileinspect.Kind) string {
	switch k {
	case profileinspect.KindSkill:
		return theme.Secondary.Bold(true).Render("✎")
	case profileinspect.KindCommand:
		return theme.Primary.Render("›")
	case profileinspect.KindAgent:
		return theme.Secondary.Bold(true).Render("◎")
	case profileinspect.KindHook:
		return theme.Warn.Bold(true).Render("⤷")
	case profileinspect.KindOutputStyle:
		return theme.Secondary.Bold(true).Render("◐")
	case profileinspect.KindMCPServer:
		return theme.Warn.Bold(true).Render("⚡")
	case profileinspect.KindMemory:
		return theme.Subtle.Bold(true).Render("✧")
	case profileinspect.KindClaudeMD:
		return theme.Subtle.Bold(true).Render("📄")
	}
	return theme.Subtle.Render("·")
}

// inspectorStatusChip colour-codes each item's sync state:
//   - synced: green (✓)
//   - pending push: warn (↑)
//   - pending pull: warn (↓)
//   - excluded: neutral (⊘)
func inspectorStatusChip(s profileinspect.Status) string {
	switch s {
	case profileinspect.StatusSynced:
		return theme.ChipGood.Render("✓ synced")
	case profileinspect.StatusPendingPush:
		return theme.ChipWarn.Render("↑ pending push")
	case profileinspect.StatusPendingPull:
		return theme.ChipWarn.Render("↓ pending pull")
	case profileinspect.StatusExcluded:
		return theme.ChipNeutral.Render("⊘ excluded")
	}
	return ""
}

// Ensure lipgloss is referenced so the import isn't dropped on a
// future refactor — some rendering helpers still rely on it.
var _ = lipgloss.Width
