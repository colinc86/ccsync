package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

// selectiveSyncModel lets the user pick a subset of actions from a plan and
// apply them one-shot (without advancing the last-synced base commit, so
// un-selected paths remain pending for next time).
type selectiveSyncModel struct {
	ctx    *AppContext
	items  []selectiveItem
	cursor int
	info   string
}

type selectiveItem struct {
	action   sync.FileAction
	selected bool
}

func newSelectiveSync(ctx *AppContext, plan sync.Plan) *selectiveSyncModel {
	items := make([]selectiveItem, 0, len(plan.Actions))
	for _, a := range plan.Actions {
		if a.Action == manifest.ActionNoOp {
			continue
		}
		items = append(items, selectiveItem{action: a, selected: true})
	}
	return &selectiveSyncModel{ctx: ctx, items: items}
}

func (m *selectiveSyncModel) Title() string { return "Selective sync — pick what to apply" }
func (m *selectiveSyncModel) Init() tea.Cmd { return nil }

func (m *selectiveSyncModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
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
				m.items[m.cursor].selected = !m.items[m.cursor].selected
			}
		case "a":
			allOn := true
			for _, it := range m.items {
				if !it.selected {
					allOn = false
					break
				}
			}
			for i := range m.items {
				m.items[i].selected = !allOn
			}
		case "enter":
			only := map[string]bool{}
			for _, it := range m.items {
				if it.selected {
					only[it.action.Path] = true
				}
			}
			if len(only) == 0 {
				m.info = "nothing selected — toggle with space"
				return m, nil
			}
			syncer := newSync(m.ctx)
			syncer.onlyPaths = only
			return m, switchTo(syncer)
		}
	}
	return m, nil
}

func (m *selectiveSyncModel) View() string {
	if len(m.items) == 0 {
		return theme.Good.Render("nothing to do — all in sync")
	}
	var sb strings.Builder
	on := 0
	for _, it := range m.items {
		if it.selected {
			on++
		}
	}
	sb.WriteString(fmt.Sprintf("%d of %d selected — unselected paths stay pending for next sync\n\n",
		on, len(m.items)))

	start, end := windowAround(m.cursor, len(m.items), 20)
	for i := start; i < end; i++ {
		it := m.items[i]
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		box := theme.Hint.Render("[ ]")
		if it.selected {
			box = theme.Good.Render("[x]")
		}
		sb.WriteString(fmt.Sprintf("%s%s %s %s\n",
			cursor, box,
			actionGlyph(it.action.Action),
			it.action.Path,
		))
	}
	if m.info != "" {
		sb.WriteString("\n" + theme.Warn.Render(m.info) + "\n")
	}
	sb.WriteString("\n" + theme.Hint.Render("space toggle • a toggle all • enter apply selected"))
	return sb.String()
}

func windowAround(cursor, total, size int) (int, int) {
	if total <= size {
		return 0, total
	}
	start := cursor - size/2
	if start < 0 {
		start = 0
	}
	end := start + size
	if end > total {
		end = total
		start = end - size
	}
	return start, end
}
