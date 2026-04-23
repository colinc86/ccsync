package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

// reviewItem is one reviewable action — a push or pull of a specific
// file — with toggles the user flips before applying. Built from
// sync.FileAction but decoupled so MCP sub-JSON items (added in a
// follow-up commit) can use the same UI.
//
// Three user-facing states per push item:
//   - Allowed=true, Promote=false: push to active profile (default)
//   - Allowed=true, Promote=true: push, then promote to default
//     so every other profile that extends default sees it
//   - Allowed=false: deny (adds to DeniedPaths, file skipped forever)
//
// Pull items only have Allowed/Denied; Promote isn't meaningful for
// an incoming action.
type reviewItem struct {
	Path        string
	Category    string
	DirectionUp bool // true = push (outbound), false = pull (inbound)
	Summary     string
	Allowed     bool // default true; user toggles to deny
	Promote     bool // push items only: after push, promote to default profile

	// RepoRelPath is the path under the active profile's prefix
	// ("claude/commands/foo.md"), suitable for adding to DeniedPaths
	// or passing to sync.PromotePath.
	RepoRelPath string
}

// reviewScreenModel presents all items from sync.PartitionPlan that
// carry policy=review. The user flips each item's allow/deny, then
// hits enter to apply. Denied items are persisted to state.DeniedPaths
// so future syncs silently skip them — which means "review then deny"
// is equivalent to "promote to never-sync" for that specific path.
type reviewScreenModel struct {
	ctx      *AppContext
	items    []reviewItem
	grouped  []reviewGroup // items grouped by category, in display order
	cursor   int           // absolute index into items (flat)
	applying bool
	err      error
}

// reviewGroup is the display-time grouping of items by category for
// cleaner rendering; the flat items slice is the source of truth for
// cursor navigation.
type reviewGroup struct {
	Category string
	Items    []int // indices into reviewScreenModel.items
}

func newReviewScreen(ctx *AppContext, actions []sync.FileAction, profilePrefix string) *reviewScreenModel {
	items := make([]reviewItem, 0, len(actions))
	for _, a := range actions {
		rel := strings.TrimPrefix(a.Path, profilePrefix)
		items = append(items, reviewItem{
			Path:        a.Path,
			Category:    a.Category,
			DirectionUp: sync.ActionIsPush(a.Action),
			Summary:     sync.SummarizeAction(a),
			Allowed:     true,
			RepoRelPath: rel,
		})
	}
	// Sort items into canonical category order so the flat cursor
	// position matches what the user sees. Pre-v0.6.15 the items
	// slice preserved insertion order while View grouped by
	// category — cursor=1 pointed at the 2nd-visible row
	// (post-grouping) but m.items[1] held the 2nd-inserted action,
	// which was often a different file. Users silently toggled the
	// wrong deny flag. Stable within a category (secondary key =
	// Path).
	sortItemsForDisplay(items)
	m := &reviewScreenModel{ctx: ctx, items: items}
	m.rebuildGroups()
	return m
}

// sortItemsForDisplay orders items so that the flat index matches the
// grouped render order. Category rank follows category.All();
// unrecognized categories go to the tail, tie-broken lexically.
func sortItemsForDisplay(items []reviewItem) {
	rank := map[string]int{}
	for i, c := range category.All() {
		rank[c] = i
	}
	catRank := func(c string) int {
		if r, ok := rank[c]; ok {
			return r
		}
		return len(rank)
	}
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := catRank(items[i].Category), catRank(items[j].Category)
		if ri != rj {
			return ri < rj
		}
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		return items[i].Path < items[j].Path
	})
}

func (m *reviewScreenModel) rebuildGroups() {
	byCat := map[string][]int{}
	for i, it := range m.items {
		byCat[it.Category] = append(byCat[it.Category], i)
	}
	var groups []reviewGroup
	// Canonical order from category.All() so sections render deterministically.
	for _, c := range category.All() {
		if idx, ok := byCat[c]; ok {
			groups = append(groups, reviewGroup{Category: c, Items: idx})
		}
	}
	// Unknown categories (shouldn't happen in practice) — append at end, sorted.
	var tail []string
	for c := range byCat {
		if c != category.Agents && c != category.Skills && c != category.Commands &&
			c != category.Memory && c != category.MCPServers && c != category.ClaudeMD &&
			c != category.GeneralSettings && c != category.Other {
			tail = append(tail, c)
		}
	}
	sort.Strings(tail)
	for _, c := range tail {
		groups = append(groups, reviewGroup{Category: c, Items: byCat[c]})
	}
	m.grouped = groups
}

func (m *reviewScreenModel) Title() string { return "Review pending sync" }

func (m *reviewScreenModel) Init() tea.Cmd { return nil }

// reviewDoneMsg marks the end of the persistence step (state.Save) so
// the model can pivot to the actual sync screen.
type reviewDoneMsg struct {
	err error
}

func (m *reviewScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case reviewDoneMsg:
		m.applying = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Persistence done — dispatch the real sync. The newly-saved
		// DeniedPaths are picked up by sync.Run on re-discovery. Any
		// items flagged for promote ride along; the sync screen runs
		// PromotePath for each after the main sync commit lands.
		promotes := m.collectPromotes()
		if len(promotes) > 0 {
			return m, switchTo(newSyncWithPromotes(m.ctx, promotes))
		}
		return m, switchTo(newSync(m.ctx))
	case tea.KeyMsg:
		if m.applying {
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case " ", "x":
			if len(m.items) > 0 {
				m.items[m.cursor].Allowed = !m.items[m.cursor].Allowed
			}
		case "p":
			// Promote toggle — push items only. Denied items can't be
			// promoted (nothing to promote) so we ignore the key there.
			if len(m.items) > 0 {
				it := &m.items[m.cursor]
				if it.DirectionUp && it.Allowed {
					it.Promote = !it.Promote
				}
			}
		case "a":
			// Allow all
			for i := range m.items {
				m.items[i].Allowed = true
			}
		case "d":
			// Deny all
			for i := range m.items {
				m.items[i].Allowed = false
				m.items[i].Promote = false
			}
		case "enter":
			m.applying = true
			return m, m.commit()
		}
	}
	return m, nil
}

// collectPromotes returns one promoteIntent per item the user tagged
// for promote in the review screen. Destination is always "default"
// in v0.6.0 — the canonical "share with everyone" target. Browse
// Tracked Files can promote to arbitrary profiles for users with
// more elaborate extends chains.
func (m *reviewScreenModel) collectPromotes() []promoteIntent {
	active := m.ctx.State.ActiveProfile
	if active == "" {
		active = "default"
	}
	var out []promoteIntent
	for _, it := range m.items {
		if !it.Allowed || !it.Promote {
			continue
		}
		if active == "default" {
			// Nothing to promote to; user is already on the share root.
			continue
		}
		out = append(out, promoteIntent{
			RepoRelPath: it.RepoRelPath,
			From:        active,
			To:          "default",
		})
	}
	return out
}

func (m *reviewScreenModel) commit() tea.Cmd {
	return func() tea.Msg {
		// Persist denials to state; allowed items need no state change
		// (they sync normally).
		for _, it := range m.items {
			if !it.Allowed {
				m.ctx.State.DenyPath(it.RepoRelPath)
			}
		}
		if err := state.Save(m.ctx.StateDir, m.ctx.State); err != nil {
			return reviewDoneMsg{err: err}
		}
		return reviewDoneMsg{}
	}
}

func (m *reviewScreenModel) View() string {
	if m.applying {
		card := theme.CardPending.Width(56).Render(
			theme.Warn.Bold(true).Render("◌ APPLYING") + "\n" +
				theme.Hint.Render("saving your choices and running the sync…"))
		return card
	}

	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(renderError(m.err) + "\n\n")
	}

	allowed, denied, promoted := 0, 0, 0
	for _, it := range m.items {
		if !it.Allowed {
			denied++
			continue
		}
		allowed++
		if it.Promote {
			promoted++
		}
	}
	// Chip row for the review tallies — coloured semantically so the
	// eye reads allow/deny/promote at a glance.
	chips := []string{
		theme.ChipGood.Render(fmt.Sprintf("✓ %d allow", allowed)),
		theme.ChipWarn.Render(fmt.Sprintf("✗ %d deny", denied)),
	}
	if promoted > 0 {
		chips = append(chips, theme.ChipNeutral.Render(fmt.Sprintf("↗ %d promote", promoted)))
	}
	sb.WriteString(strings.Join(chips, theme.Rule.Render("  ·  ")) + "\n\n")

	cursorIdx := m.cursor
	seen := 0
	active := m.ctx.State.ActiveProfile
	if active == "" {
		active = "default"
	}
	for _, g := range m.grouped {
		sb.WriteString(theme.Secondary.Render(category.Label(g.Category)) + "\n")
		for _, idx := range g.Items {
			it := m.items[idx]
			mark := theme.Good.Render("[✓]")
			if !it.Allowed {
				mark = theme.Warn.Render("[✗]")
			}
			dir := theme.Subtle.Render("↑")
			if !it.DirectionUp {
				dir = theme.Subtle.Render("↓")
			}
			cursor := "  "
			if seen == cursorIdx {
				cursor = theme.Primary.Render("▸ ")
			}
			var dest string
			switch {
			case !it.DirectionUp:
				dest = ""
			case it.Promote:
				dest = "  " + theme.Primary.Render("→ default (shared)")
			default:
				dest = "  " + theme.Hint.Render("→ "+active)
			}
			fmt.Fprintf(&sb, "%s%s %s %s%s\n", cursor, mark, dir, it.Summary, dest)
			seen++
		}
		sb.WriteString("\n")
	}

	sb.WriteString(renderFooterBar([]footerKey{
		{cap: "enter", label: "apply"},
		{cap: "space", label: "toggle"},
		{cap: "p", label: "promote to default"},
		{cap: "a", label: "allow all"},
		{cap: "d", label: "deny all"},
		{cap: "↑↓", label: "move"},
	}))
	return sb.String()
}
