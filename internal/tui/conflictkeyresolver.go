package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/humanize"
	"github.com/colinc86/ccsync/internal/merge"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

// conflictKeyResolverModel is the per-key picker for a single JSON conflict file.
// l = take local, r = take remote, a = apply (compute final bytes and hand
// them back to the parent ConflictResolver via perKeyResolvedMsg).
type conflictKeyResolverModel struct {
	ctx      *AppContext
	fileIdx  int
	filePath string
	merged   []byte
	conflict []merge.Conflict
	choices  []sync.KeyChoice
	cursor   int
}

// perKeyResolvedMsg carries the final per-key-resolved bytes back to the
// parent ConflictResolver when the user accepts.
type perKeyResolvedMsg struct {
	fileIdx int
	bytes   []byte
}

func newConflictKeyResolver(ctx *AppContext, fileIdx int, fc sync.FileConflict) *conflictKeyResolverModel {
	choices := make([]sync.KeyChoice, len(fc.Conflicts))
	for i := range choices {
		choices[i] = sync.KeyLocal
	}
	return &conflictKeyResolverModel{
		ctx:      ctx,
		fileIdx:  fileIdx,
		filePath: fc.Path,
		merged:   fc.MergedData,
		conflict: fc.Conflicts,
		choices:  choices,
	}
}

func (m *conflictKeyResolverModel) Title() string {
	return "Per-key conflicts — " + m.filePath
}

func (m *conflictKeyResolverModel) Init() tea.Cmd { return nil }

func (m *conflictKeyResolverModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.conflict)-1 {
				m.cursor++
			}
		case "l":
			if len(m.conflict) > 0 {
				m.choices[m.cursor] = sync.KeyLocal
				if m.cursor < len(m.conflict)-1 {
					m.cursor++
				}
			}
		case "r":
			if len(m.conflict) > 0 {
				m.choices[m.cursor] = sync.KeyRemote
				if m.cursor < len(m.conflict)-1 {
					m.cursor++
				}
			}
		case "a":
			bytes, err := sync.BuildPerKeyResolution(m.merged, m.conflict, m.choices)
			if err != nil {
				// Fall back to merged as-is; surface nothing special for v1.
				bytes = m.merged
			}
			idx := m.fileIdx
			return m, tea.Batch(
				func() tea.Msg { return perKeyResolvedMsg{fileIdx: idx, bytes: bytes} },
				popScreen(),
			)
		case "esc":
			// Bail without applying — user is backing out of the
			// per-key resolver (to the file-level resolver or parent
			// screen). Pre-fix no esc case meant the screen was a
			// one-way trapdoor; user had to make a choice for every
			// key just to leave.
			return m, popScreen()
		}
	}
	return m, nil
}

func (m *conflictKeyResolverModel) View() string {
	var sb strings.Builder

	// Progress chip — "3/7 chosen" tracks the user through the list
	// the same way the parent conflict resolver does for files.
	chosen := 0
	for _, c := range m.choices {
		if c != sync.KeyPending {
			chosen++
		}
	}
	chipStyle := theme.ChipNeutral
	if chosen == len(m.conflict) {
		chipStyle = theme.ChipGood
	}
	sb.WriteString(chipStyle.Render(
		fmt.Sprintf("● %d / %d chosen", chosen, len(m.conflict))) + "  " +
		theme.Hint.Render("(default is LOCAL)") + "\n\n")

	start, end := windowAround(m.cursor, len(m.conflict), 16)
	for i := start; i < end; i++ {
		c := m.conflict[i]
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		mark := theme.Warn.Render("[ ?]")
		switch m.choices[i] {
		case sync.KeyLocal:
			mark = theme.Good.Render("[L]")
		case sync.KeyRemote:
			mark = theme.Secondary.Render("[R]")
		}
		pathDisp := c.Path
		if pathDisp == "" {
			pathDisp = "(root)"
		}
		sb.WriteString(fmt.Sprintf("%s%s %s\n", cursor, mark, pathDisp))

		if m.cursor == i {
			localDisp := humanize.Truncate(c.Local, 60)
			remoteDisp := humanize.Truncate(c.Remote, 60)
			if !c.LocalPresent {
				localDisp = theme.Hint.Render("(absent — local deleted)")
			}
			if !c.RemotePresent {
				remoteDisp = theme.Hint.Render("(absent — remote deleted)")
			}
			// Side-by-side-ish diff — tag each side with a chip so
			// the user can distinguish at a glance which they're
			// reading, and the indentation keeps them clearly
			// subordinate to the key path above.
			sb.WriteString("     " + theme.ChipGood.Render(" L ") + "  " + localDisp + "\n")
			sb.WriteString("     " + theme.ChipNeutral.Render(" R ") + "  " + remoteDisp + "\n")
		}
	}

	sb.WriteString("\n" + renderFooterBar([]footerKey{
		{cap: "a", label: "accept"},
		{cap: "l", label: "local"},
		{cap: "r", label: "remote"},
		{cap: "↑↓", label: "move"},
		{cap: "esc", label: "cancel"},
	}))
	return sb.String()
}
