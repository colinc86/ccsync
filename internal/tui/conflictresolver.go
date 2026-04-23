package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/humanize"
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

func (m *conflictResolverModel) Title() string {
	return fmt.Sprintf("Conflicts (%d)", len(m.conflicts))
}
func (m *conflictResolverModel) Init() tea.Cmd { return nil }

// IsTerminal returns true once the user has resolved every conflict
// and the push has landed — at that point backing up one step would
// drop the user on a stale SyncPreview whose conflicts no longer exist,
// so ESC should flush the stack all the way to Home.
func (m *conflictResolverModel) IsTerminal() bool { return m.result != nil }

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
		var toast tea.Cmd
		if msg.err != nil {
			toast = showToast("resolve failed: "+msg.err.Error(), toastError)
		} else {
			n := len(m.conflicts)
			unit := "conflict"
			if n != 1 {
				unit += "s"
			}
			toast = showToast(fmt.Sprintf("✓ %d %s resolved and pushed", n, unit), toastSuccess)
		}
		return m, tea.Batch(refreshPlanCmd(m.ctx), toast)
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
			m.cursor = wrapCursor(m.cursor, len(m.conflicts), -1)
		case "down", "j":
			m.cursor = wrapCursor(m.cursor, len(m.conflicts), +1)
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
			// Enter drills into the appropriate per-item picker — JSON
			// goes to per-key, text goes to per-hunk. Pre-fix, enter
			// on a text conflict silently did nothing because the key
			// picker is JSON-only; users then had to hunt for the
			// `h` shortcut. `h` still works, but `enter` is the
			// universal "go deeper" key and should follow that
			// convention.
			if len(m.conflicts) > 0 {
				fc := m.conflicts[m.cursor]
				if fc.IsJSON {
					return m, switchTo(newConflictKeyResolver(m.ctx, m.cursor, fc))
				}
				return m, switchTo(newConflictHunkResolver(m.ctx, m.cursor, fc))
			}
		case "h":
			// Retained as the explicit per-hunk shortcut for users
			// who learned it from earlier versions or the help overlay.
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

	// Hero conflict card — red-bordered so the user immediately
	// understands the stakes. The previous "! N file diverged"
	// prose was correct but easy to miss; a bordered card with a
	// big glyph and caps title stops the eye.
	var card strings.Builder
	card.WriteString(theme.Bad.Render("!  ") +
		theme.Bad.Render(fmt.Sprintf("%d CONFLICT",
			len(m.conflicts))))
	if len(m.conflicts) != 1 {
		card.WriteString(theme.Bad.Render("S"))
	}
	card.WriteString("\n" + theme.Subtle.Render(
		"this machine and the sync repo have diverging versions of these files"))
	// Preview a few.
	card.WriteString("\n")
	for i, fc := range m.conflicts {
		if i >= 5 {
			fmt.Fprintf(&card, "\n"+theme.Hint.Render("  … %d more"), len(m.conflicts)-5)
			break
		}
		fmt.Fprintf(&card, "\n  %s %s", theme.Bad.Render("·"), fc.Path)
	}
	sb.WriteString(theme.CardConflict.Width(56).Render(card.String()) + "\n\n")

	// Resolution choices — numbered, with muted descriptions. Keeps
	// the hierarchy of "big decision up top, details on hover."
	sb.WriteString(theme.Heading.Render("how should we resolve?") + "\n\n")
	writeChoice := func(key, verb, hint string) {
		fmt.Fprintf(&sb, "  %s  %s\n      %s\n\n",
			theme.Keycap.Render(key),
			theme.Primary.Render(verb),
			theme.Hint.Render(hint))
	}
	writeChoice("1", "replace local with cloud",
		"take the repo's version for every file — safest when you trust the fleet")
	writeChoice("2", "replace cloud with local",
		"push your ~/.claude version up as the winner")
	writeChoice("3", "pick per file",
		"detailed picker: per-key JSON conflicts, per-hunk text merges")

	sb.WriteString(renderFooterBar([]footerKey{
		{cap: "1-3", label: "choose"},
		{cap: "esc", label: "cancel"},
	}))
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
	sb.WriteString(theme.Wordmark("resolve conflicts") + "\n\n")

	if m.applying {
		card := theme.CardPending.Width(56).Render(
			theme.Warn.Bold(true).Render("◌ APPLYING") + "\n" +
				theme.Hint.Render("writing resolutions and pushing to the remote…"))
		sb.WriteString(card)
		return sb.String()
	}
	if m.result != nil {
		short := ""
		if m.result.CommitSHA != "" {
			short = m.result.CommitSHA
			if len(short) > 7 {
				short = short[:7]
			}
		}
		var body strings.Builder
		body.WriteString(theme.Good.Bold(true).Render("✓ RESOLVED"))
		if short != "" {
			body.WriteString("  " + theme.Hint.Render(short))
		}
		body.WriteString("\n" + theme.Subtle.Render(fmt.Sprintf(
			"%s reconciled and pushed to the remote", humanize.Count(len(m.conflicts), "conflict"))))
		sb.WriteString(theme.CardClean.Width(56).Render(body.String()) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "any key", label: "return to home"},
		}))
		return sb.String()
	}
	if m.strategyPending {
		return sb.String() + m.renderStrategy()
	}

	resolved := 0
	for _, c := range m.choices {
		if c != choicePending {
			resolved++
		}
	}

	// Progress chip — "3/5 resolved" as a neutral-toned pill so the
	// user can track their progress through the list without
	// counting rows. Changes to Good once all are resolved.
	chipStyle := theme.ChipNeutral
	if m.allResolved() {
		chipStyle = theme.ChipGood
	}
	sb.WriteString(chipStyle.Render(
		fmt.Sprintf("● %d / %d resolved", resolved, len(m.conflicts))) + "\n\n")

	for i, fc := range m.conflicts {
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		mark := m.choices[i].symbol()
		sb.WriteString(fmt.Sprintf("%s%s %s\n", cursor, mark, fc.Path))
		if m.cursor == i {
			unit := "hunk"
			if fc.IsJSON {
				unit = "key-level conflict"
			}
			sb.WriteString(theme.Hint.Render(fmt.Sprintf(
				"     local: %d bytes • remote: %d bytes • %s\n",
				len(fc.LocalData), len(fc.RemoteData), humanize.Count(len(fc.Conflicts), unit),
			)))
		}
	}

	sb.WriteString("\n")
	keys := []footerKey{}
	if m.allResolved() {
		keys = append(keys, footerKey{cap: "a", label: "apply all"})
	}
	keys = append(keys,
		footerKey{cap: "l", label: "local"},
		footerKey{cap: "r", label: "remote"},
		footerKey{cap: "enter", label: "drill in"},
		footerKey{cap: "d", label: "diff"},
		footerKey{cap: "esc", label: "cancel"},
	)
	sb.WriteString(renderFooterBar(keys))
	return sb.String()
}
