package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

// paletteCommand is one entry in the command palette. Label is what
// the user reads; keywords is the space-separated search surface
// (lowercased) so a label like "Browse tracked" can still match a
// query for "files" because keywords says so. shortcut, when set,
// is the per-screen key the user could have pressed instead — shown
// as a hint chip so the palette doubles as a keyboard reference.
type paletteCommand struct {
	label    string
	keywords string
	shortcut string
	hint     string
	action   func(ctx *AppContext) tea.Cmd
	// available is an optional gate — when non-nil, the command
	// only appears in the palette if available(ctx) returns true.
	// Used to hide bootstrap-dependent actions pre-setup.
	available func(ctx *AppContext) bool
	// contextScore is a 0-100 relevance boost applied when the
	// palette opens with an empty query. Commands with higher
	// context scores for the current app state bubble to the top.
	// Returning 0 means "don't surface specially"; commands below
	// that sort in declared order.
	contextScore func(ctx *AppContext) int
}

// paletteModel is the ctrl+k overlay: a fuzzy-match list of every
// navigation and action in the app, searchable by label or keyword.
// Models like lazygit's ? screen but driven by live input so the user
// can type a few letters and hit enter rather than scrolling.
type paletteModel struct {
	ctx      *AppContext
	input    textinput.Model
	commands []paletteCommand
	matches  []int // indices into commands, sorted by score desc
	cursor   int
}

// newPalette builds the model for the current app state. Filters
// commands whose `available` gate is false, then presents them
// match-score-sorted under an empty query (so the palette opens with
// a sensible "recent / important" preview, not a blank list).
func newPalette(ctx *AppContext) *paletteModel {
	input := textinput.New()
	// Placeholder seeds example searches so first-timers see what the
	// palette is for before they've typed anything. "sync, profile,
	// doctor, unlock…" reads as a menu rather than a black-box input
	// box and teaches the vocabulary users can type.
	input.Placeholder = "try: sync · profile · doctor · history · unlock"
	input.Prompt = "› "
	input.Focus()
	input.CharLimit = 64
	input.Width = 42

	var cmds []paletteCommand
	for _, c := range allPaletteCommands() {
		if c.available != nil && !c.available(ctx) {
			continue
		}
		cmds = append(cmds, c)
	}
	m := &paletteModel{ctx: ctx, input: input, commands: cmds}
	m.recompute()
	return m
}

// paletteClosedMsg fires when the palette should be dismissed. The
// App-level handler clears its `palette` field on receipt.
type paletteClosedMsg struct{}

func closePalette() tea.Cmd {
	return func() tea.Msg { return paletteClosedMsg{} }
}

// Update drives input + navigation while the palette is visible. The
// palette's Cmd is returned unchanged by AppModel — one of:
//   - enter → closePalette() + the action's Cmd (batched).
//   - esc → closePalette().
//   - up/down → no Cmd, cursor moved.
//   - anything else → pass through to textinput, recompute matches.
func (m *paletteModel) Update(msg tea.Msg) (*paletteModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc", "ctrl+k":
			// Toggle semantics: ctrl+k open, ctrl+k close. Less
			// mental load than "learn one key to open, another to
			// close" — every Cmd-palette-style UI the user has used
			// in an editor works this way.
			return m, closePalette()
		case "enter":
			if len(m.matches) == 0 {
				return m, closePalette()
			}
			cmd := m.commands[m.matches[m.cursor]].action(m.ctx)
			return m, tea.Batch(closePalette(), cmd)
		case "up", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "ctrl+n":
			if m.cursor < len(m.matches)-1 {
				m.cursor++
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.recompute()
	return m, cmd
}

// recompute rebuilds the match list from the current query. Empty
// query applies the per-command contextScore so the top of the list
// reflects "what's most relevant right now" (resolve on conflicts,
// unlock when locked, sync now when pending). Non-empty query scores
// each command against the query using a simple substring algorithm:
// label prefix > label substring > keyword substring.
func (m *paletteModel) recompute() {
	q := strings.ToLower(strings.TrimSpace(m.input.Value()))
	type scored struct{ idx, score int }
	var out []scored
	for i, c := range m.commands {
		if q == "" {
			ctxScore := 0
			if c.contextScore != nil {
				ctxScore = c.contextScore(m.ctx)
			}
			out = append(out, scored{i, ctxScore})
			continue
		}
		score := paletteScore(c, q)
		if score > 0 {
			out = append(out, scored{i, score})
		}
	}
	// Sort by score desc, stable so ties keep declared order. Under
	// empty query this surfaces contextual commands (high score)
	// above always-available ones (zero score); under a typed query
	// it's purely relevance-based.
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	m.matches = m.matches[:0]
	for _, s := range out {
		m.matches = append(m.matches, s.idx)
	}
	if m.cursor >= len(m.matches) {
		m.cursor = 0
	}
}

// paletteScore assigns a relevance score for a command against a
// lowercased query. Higher is better; zero means no match.
//
// Tiered to favour the intent users actually have:
//  1. Label starts with the query — the user is typing the thing.
//  2. Label contains the query — close enough, near-top of list.
//  3. Keywords contain the query — still relevant, show lower.
func paletteScore(c paletteCommand, q string) int {
	label := strings.ToLower(c.label)
	keywords := strings.ToLower(c.keywords)
	switch {
	case strings.HasPrefix(label, q):
		return 300 - len(label) // shorter labels edge out longer ones
	case strings.Contains(label, q):
		return 200 - len(label)
	case strings.Contains(keywords, q):
		return 100
	}
	return 0
}

// renderPalette composes the overlay. Rounded-border panel centered
// on the viewport with: input row, divider, match list (up to 10),
// and a muted "query/total" counter. Empty-matches state shows a
// "no results" hint rather than a blank list.
func renderPalette(m *paletteModel) string {
	if m == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(theme.WordmarkStyle.Render("ccsync") + "  " +
		theme.Subtle.Render("quick actions") + "\n")
	sb.WriteString(theme.Rule.Render(strings.Repeat("─", 48)) + "\n\n")

	sb.WriteString(m.input.View() + "\n\n")

	if len(m.matches) == 0 {
		sb.WriteString(theme.Hint.Render("  no matches — try a different search"))
	} else {
		const maxShown = 10
		shown := len(m.matches)
		if shown > maxShown {
			shown = maxShown
		}
		// Compute the widest shortcut among the visible rows so the
		// label column aligns regardless of which commands are in the
		// result set. Without this, a row with a "ctrl+k" shortcut
		// would push its label right, while a row with "b" would not
		// — jagged columns are hard to scan.
		const pad = 2
		keyColWidth := 0
		for i := 0; i < shown; i++ {
			c := m.commands[m.matches[i]]
			if c.shortcut == "" {
				continue
			}
			w := lipgloss.Width(theme.KeycapMuted.Render(c.shortcut))
			if w > keyColWidth {
				keyColWidth = w
			}
		}
		for i := 0; i < shown; i++ {
			c := m.commands[m.matches[i]]
			cursor := "  "
			if i == m.cursor {
				cursor = theme.Primary.Render("▸ ")
			}
			var keycap string
			if c.shortcut != "" {
				keycap = theme.KeycapMuted.Render(c.shortcut)
				gap := keyColWidth - lipgloss.Width(keycap)
				if gap < 0 {
					gap = 0
				}
				keycap += strings.Repeat(" ", gap+pad)
			} else if keyColWidth > 0 {
				keycap = strings.Repeat(" ", keyColWidth+pad)
			}
			line := cursor + keycap + theme.Primary.Render(c.label)
			if c.hint != "" {
				line += "  " + theme.Hint.Render(c.hint)
			}
			sb.WriteString(line + "\n")
		}
		if len(m.matches) > maxShown {
			fmt.Fprintf(&sb, theme.Hint.Render("\n  … %d more — keep typing to narrow"),
				len(m.matches)-maxShown)
		}
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Accent).
		Padding(1, 2).
		Width(52).
		Render(sb.String())
}

// allPaletteCommands is the master command list. Kept in this one
// place so adding a new screen to the palette is a single-file
// change. Order matters: it's what an empty-query palette displays,
// so put "most useful" first (sync, switch, history).
func allPaletteCommands() []paletteCommand {
	bootstrapped := func(c *AppContext) bool {
		return c != nil && c.State != nil && c.State.SyncRepoURL != ""
	}
	return []paletteCommand{
		{
			label: "Inspect profile", keywords: "inspect what syncing things inventory skills commands agents mcp view",
			hint: "see what's in this profile — skills, commands, subagents, MCP servers",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newProfileInspect(c))
			},
			available: bootstrapped,
			// Strong context boost on bootstrapped accounts — the
			// inspector is the new default discovery surface. Ranks
			// below hard-forcing commands (Resolve = 100, Unlock = 95)
			// so those still win during conflict or lock state.
			contextScore: func(c *AppContext) int { return 70 },
		},
		{
			label: "Sync now", keywords: "sync push pull apply",
			hint: "run the full push/pull/merge cycle",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newSyncPreview(c))
			},
			available: bootstrapped,
			// Bubble to top when there are pending pushes/pulls
			// (but no conflicts — conflicts outrank with their
			// own command). "Sync now" is what you want most
			// often; surface it first when it has work to do.
			contextScore: func(c *AppContext) int {
				if c == nil || c.Plan == nil {
					return 0
				}
				s := c.Summary()
				if s.Conflicts > 0 {
					return 0 // resolve wins this context
				}
				if s.Outbound+s.Inbound > 0 {
					return 80
				}
				return 0
			},
		},
		{
			label: "Re-check remote", keywords: "refresh status fetch", shortcut: "r",
			hint: "re-fetch the remote to refresh the dashboard",
			action: func(c *AppContext) tea.Cmd {
				return refreshPlanCmd(c)
			},
			available: bootstrapped,
		},
		{
			label: "Resolve conflicts", keywords: "resolve conflict merge",
			hint: "pick a side (or per-key) for every diverging file",
			action: func(c *AppContext) tea.Cmd {
				if c.Plan == nil || len(c.Plan.Conflicts) == 0 {
					return nil
				}
				return switchTo(newConflictResolver(c, c.Plan.Conflicts))
			},
			available: func(c *AppContext) bool {
				return bootstrapped(c) && c.Plan != nil && len(c.Plan.Conflicts) > 0
			},
			// Highest priority when conflicts exist — user came to
			// ccsync because of them, make the remedy one key away.
			contextScore: func(c *AppContext) int { return 100 },
		},
		{
			label: "Unlock encrypted repo", keywords: "unlock encryption passphrase",
			hint: "enter the passphrase so this machine can sync",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newEncryptionScreen(c))
			},
			available: func(c *AppContext) bool {
				// Only surface when the repo is actually locked on
				// this machine — no reason to nudge users whose
				// machine already has the passphrase cached.
				return bootstrapped(c) && detectEncStatus(c) == encLocked
			},
			contextScore: func(c *AppContext) int { return 95 },
		},
		{
			label: "Sync history", keywords: "history rollback commits log", shortcut: "h",
			hint: "browse past syncs + snapshots, revert if needed",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newSyncHistory(c))
			},
		},
		{
			label: "Browse tracked files", keywords: "browse files tracked list", shortcut: "b",
			hint: "inspect every synced path; exclude, promote, or trace rules",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newBrowseTracked(c))
			},
			available: bootstrapped,
		},
		{
			label: "Profiles", keywords: "profile switch create delete edit", shortcut: "p",
			hint: "manage ccsync profiles and switch between them",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newProfiles(c))
			},
			available: bootstrapped,
		},
		{
			label: "Settings", keywords: "settings preferences identity repo", shortcut: "s",
			hint: "repo URL, auth, auto-sync, review policies, encryption",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newSettings(c))
			},
		},
		{
			label: "Doctor", keywords: "doctor health diagnostic check", shortcut: "d",
			hint: "integrity checks: repo, keychain, encryption, snapshots",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newDoctorScreen(c))
			},
		},
		{
			label: "Suggestions", keywords: "suggestions propose rules tips",
			hint: "rule-change proposals derived from the cached plan",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newSuggestions(c))
			},
			available: bootstrapped,
		},
		{
			label: "Review policies", keywords: "policies review auto push pull",
			hint: "per-category push/pull policies (auto / review / never)",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newPoliciesScreen(c))
			},
		},
		{
			label: "Repo encryption", keywords: "encrypt encryption passphrase unlock",
			hint: "enable, disable, or unlock at-rest encryption",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newEncryptionScreen(c))
			},
		},
		{
			label: "Check for updates", keywords: "update upgrade version",
			hint: "fetch the latest release and install in place",
			action: func(c *AppContext) tea.Cmd {
				return switchTo(newUpdateScreen(c))
			},
		},
		{
			label: "Home", keywords: "home dashboard back", shortcut: "esc",
			hint: "return to the dashboard",
			action: func(c *AppContext) tea.Cmd {
				return popToRoot()
			},
		},
		{
			label: "Help", keywords: "help keybindings shortcuts cheatsheet", shortcut: "?",
			hint: "show the keybinding cheat sheet",
			action: func(c *AppContext) tea.Cmd {
				return func() tea.Msg { return paletteOpenHelpMsg{} }
			},
		},
	}
}

// paletteOpenHelpMsg is the signal that the palette wants to open the
// help overlay after it closes. AppModel handles it by flipping the
// help flag after processing paletteClosedMsg.
type paletteOpenHelpMsg struct{}

// tipIDPalette is the state.TipsSeen id for the one-time ctrl+k
// teaching toast fired at Init time. Kept next to the palette so
// adding a second tip is an obviously-local change.
const tipIDPalette = "palette"

// paletteTipOnceCmd fires a welcome toast on first launch (per
// state.TipsSeen) pointing at ctrl+k. Returns nil once the tip has
// been shown. Also nil when the user isn't bootstrapped yet —
// first-timers have onboarding to handle; the palette tip is for
// returning users who may never have discovered ctrl+k from the
// footer hint.
func paletteTipOnceCmd(ctx *AppContext) tea.Cmd {
	if ctx == nil || ctx.State == nil {
		return nil
	}
	if ctx.State.SyncRepoURL == "" {
		return nil
	}
	if ctx.State.TipSeen(tipIDPalette) {
		return nil
	}
	// Mark-as-seen up front so a crash or quit mid-session doesn't
	// re-fire the tip on next launch. Persisted best-effort via
	// state.Save; failures here are silent because the tip is
	// informational, not load-bearing.
	ctx.State.MarkTipSeen(tipIDPalette)
	_ = state.Save(ctx.StateDir, ctx.State)
	return showToast("tip: ctrl+k opens the command palette", toastInfo)
}
