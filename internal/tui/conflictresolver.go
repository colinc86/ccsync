package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

// resolutionChoice discriminates the user's pick for a conflicted file.
type resolutionChoice int

const (
	choicePending resolutionChoice = iota
	choiceLocal
	choiceRemote
	choicePerKey
)

func (c resolutionChoice) symbol() string {
	switch c {
	case choiceLocal:
		return theme.Good.Render("[L]")
	case choiceRemote:
		return theme.Secondary.Render("[R]")
	case choicePerKey:
		return theme.Primary.Render("[K]")
	}
	return theme.Warn.Render("[ ?]")
}

// conflictResolverModel shows the list of conflicted files and lets the user
// pick local or remote for each, then apply all. For JSON files, the user
// can drill into per-key resolution via `k`. For most cases a user just
// wants "take everything from this side" — a front-page bulk picker
// handles that without forcing them through the detailed UI.
type conflictResolverModel struct {
	ctx       *AppContext
	conflicts []sync.FileConflict
	choices   []resolutionChoice
	override  map[int][]byte // fileIdx → final bytes (from per-key picker)
	cursor    int
	applying  bool
	err       error
	result    *sync.Result

	// strategyPending is true when the bulk picker should show before the
	// detailed list. Flips to false either when the user picks "manual"
	// (reveals the per-file picker) or when a bulk choice has been
	// applied (we proceed directly to runApplyResolutions).
	strategyPending bool
}

func newConflictResolver(ctx *AppContext, conflicts []sync.FileConflict) *conflictResolverModel {
	return &conflictResolverModel{
		ctx:             ctx,
		conflicts:       conflicts,
		choices:         make([]resolutionChoice, len(conflicts)),
		override:        map[int][]byte{},
		strategyPending: len(conflicts) > 0,
	}
}

func (m *conflictResolverModel) Title() string { return fmt.Sprintf("Conflicts (%d)", len(m.conflicts)) }
func (m *conflictResolverModel) Init() tea.Cmd { return nil }

type applyResolutionsDoneMsg struct {
	result sync.Result
	err    error
}

func (m *conflictResolverModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case applyResolutionsDoneMsg:
		m.applying = false
		m.err = msg.err
		if msg.err == nil {
			r := msg.result
			m.result = &r
		}
		// ApplyResolutions commits and advances LastSyncedSHA on disk —
		// pull the change into the TUI's state so the status bar refreshes,
		// and recompute the plan so the next frame reflects the new counts.
		m.ctx.RefreshState()
		return m, refreshPlanCmd(m.ctx)
	case perKeyResolvedMsg:
		m.override[msg.fileIdx] = msg.bytes
		m.choices[msg.fileIdx] = choicePerKey
		m.advance()
		return m, nil
	case tea.KeyMsg:
		if m.applying {
			return m, nil
		}
		if m.result != nil {
			// Conflicts resolved and applied — user's done with this flow.
			// Return to Home, not back to the Sync screen that pushed us.
			return m, popToRoot()
		}
		if m.strategyPending {
			return m.updateStrategy(msg)
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.conflicts)-1 {
				m.cursor++
			}
		case "l":
			if len(m.conflicts) > 0 {
				m.choices[m.cursor] = choiceLocal
				delete(m.override, m.cursor)
				m.advance()
			}
		case "r":
			if len(m.conflicts) > 0 {
				m.choices[m.cursor] = choiceRemote
				delete(m.override, m.cursor)
				m.advance()
			}
		case "enter":
			if len(m.conflicts) > 0 && m.conflicts[m.cursor].IsJSON {
				return m, switchTo(newConflictKeyResolver(m.ctx, m.cursor, m.conflicts[m.cursor]))
			}
		case "h":
			// Per-hunk text resolution. JSON conflicts already have per-
			// key drill-down via enter; hunk resolver is for text files.
			if len(m.conflicts) > 0 && !m.conflicts[m.cursor].IsJSON {
				return m, switchTo(newConflictHunkResolver(m.ctx, m.cursor, m.conflicts[m.cursor]))
			}
		case "a":
			if m.allResolved() {
				m.applying = true
				return m, runApplyResolutions(m.ctx, m.conflicts, m.choices, m.override)
			}
		case "d":
			if len(m.conflicts) > 0 {
				fc := m.conflicts[m.cursor]
				return m, switchTo(newDiffView(fc.Path, fc.LocalData, fc.RemoteData))
			}
		}
	}
	return m, nil
}

// renderStrategy shows the bulk picker as the first view when conflicts
// exist. Most users — especially someone new who just hit a merge they
// didn't expect — want a one-shot "just take their version" or "keep
// mine" button. The manual path is one keystroke away for the 10% of
// cases that need per-file control.
func (m *conflictResolverModel) renderStrategy() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %d file(s) diverged between local and the repo.\n\n",
		theme.Bad.Render("!"), len(m.conflicts))

	// Preview a few of them so the user knows what they're deciding on.
	for i, fc := range m.conflicts {
		if i >= 5 {
			fmt.Fprintf(&sb, theme.Hint.Render("  … %d more\n"), len(m.conflicts)-5)
			break
		}
		fmt.Fprintf(&sb, "  · %s\n", fc.Path)
	}
	sb.WriteString("\n" + theme.Heading.Render("how should we resolve?") + "\n\n")
	fmt.Fprintf(&sb, "  %s  %s %s\n",
		theme.Primary.Render("1"),
		"replace local with cloud",
		theme.Hint.Render("— take the repo's version for every file"))
	fmt.Fprintf(&sb, "  %s  %s %s\n",
		theme.Primary.Render("2"),
		"replace cloud with local",
		theme.Hint.Render("— push your ~/.claude version up as the winner"))
	fmt.Fprintf(&sb, "  %s  %s %s\n",
		theme.Primary.Render("3"),
		"manual — pick per file",
		theme.Hint.Render("— detailed picker with per-key JSON / per-hunk text"))
	sb.WriteString("\n" + theme.Hint.Render("1-3 choose • esc cancel"))
	return sb.String()
}

// updateStrategy handles the bulk front-page picker. Hitting "local" or
// "remote" applies that choice to every conflict and dispatches the apply
// immediately — the user doesn't have to walk the per-file picker just
// to stamp the same answer N times. "manual" reveals the detailed view
// that existed before this front page.
func (m *conflictResolverModel) updateStrategy(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "1", "r":
		// Replace local with cloud — take remote for every conflict.
		for i := range m.choices {
			m.choices[i] = choiceRemote
		}
		m.strategyPending = false
		m.applying = true
		return m, runApplyResolutions(m.ctx, m.conflicts, m.choices, m.override)
	case "2", "l":
		// Replace cloud with local — take local for every conflict.
		for i := range m.choices {
			m.choices[i] = choiceLocal
		}
		m.strategyPending = false
		m.applying = true
		return m, runApplyResolutions(m.ctx, m.conflicts, m.choices, m.override)
	case "3", "m":
		// Manual — fall through to the per-file picker.
		m.strategyPending = false
		return m, nil
	case "esc":
		return m, popScreen()
	}
	return m, nil
}

func (m *conflictResolverModel) advance() {
	if m.cursor < len(m.conflicts)-1 {
		m.cursor++
	}
}

func (m *conflictResolverModel) allResolved() bool {
	for _, c := range m.choices {
		if c == choicePending {
			return false
		}
	}
	return true
}

func runApplyResolutions(ctx *AppContext, conflicts []sync.FileConflict, choices []resolutionChoice, override map[int][]byte) tea.Cmd {
	return func() tea.Msg {
		resolutions := map[string][]byte{}
		for i, fc := range conflicts {
			if data, ok := override[i]; ok && choices[i] == choicePerKey {
				resolutions[fc.Path] = data
				continue
			}
			switch choices[i] {
			case choiceLocal:
				resolutions[fc.Path] = fc.LocalData
			case choiceRemote:
				resolutions[fc.Path] = fc.RemoteData
			}
		}
		in, err := buildSyncInputs(ctx, false)
		if err != nil {
			return applyResolutionsDoneMsg{err: err}
		}
		res, err := sync.ApplyResolutions(context.Background(), in, resolutions)
		return applyResolutionsDoneMsg{result: res, err: err}
	}
}

func (m *conflictResolverModel) View() string {
	if len(m.conflicts) == 0 {
		return theme.Good.Render("no conflicts")
	}
	var sb strings.Builder

	if m.applying {
		return theme.Hint.Render("writing resolutions and pushing…")
	}
	if m.result != nil {
		sb.WriteString(theme.Good.Render("resolved ✓") + "\n")
		if m.result.CommitSHA != "" {
			short := m.result.CommitSHA
			if len(short) > 7 {
				short = short[:7]
			}
			sb.WriteString("committed " + short + "\n")
		}
		sb.WriteString("\n" + theme.Hint.Render("press any key to return"))
		return sb.String()
	}
	if m.strategyPending {
		return m.renderStrategy()
	}

	resolved := 0
	for _, c := range m.choices {
		if c != choicePending {
			resolved++
		}
	}
	sb.WriteString(fmt.Sprintf("%d of %d resolved\n\n", resolved, len(m.conflicts)))

	for i, fc := range m.conflicts {
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		mark := m.choices[i].symbol()
		sb.WriteString(fmt.Sprintf("%s%s %s\n", cursor, mark, fc.Path))
		if m.cursor == i {
			sb.WriteString(theme.Hint.Render(fmt.Sprintf(
				"     local: %d bytes • remote: %d bytes • %d key-level conflict(s)\n",
				len(fc.LocalData), len(fc.RemoteData), len(fc.Conflicts),
			)))
		}
	}

	sb.WriteString("\n")
	if m.allResolved() {
		sb.WriteString(theme.Primary.Render("a ") + "apply all • ")
	}
	sb.WriteString(theme.Hint.Render("l local • r remote • enter per-key (JSON) • h per-hunk (text) • d diff • ↑↓ move"))
	return sb.String()
}
