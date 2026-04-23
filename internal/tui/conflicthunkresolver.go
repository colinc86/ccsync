package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/merge"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

// conflictHunkResolverModel walks the text hunks of a single conflicted
// file and lets the user pick local or remote for each. On completion,
// assembles the resolved bytes and posts them back to the parent
// ConflictResolver via perKeyResolvedMsg (reused — the payload shape is
// identical: fileIdx + bytes).
type conflictHunkResolverModel struct {
	ctx      *AppContext
	fileIdx  int
	path     string
	segments []merge.TextSegment
	// cursor is the index into segments of the current conflict hunk.
	// We auto-skip over Agreed segments since they don't need a choice.
	cursor  int
	choices map[int]hunkChoice // segment index → pick
}

type hunkChoice int

const (
	hunkPending hunkChoice = iota
	hunkLocal
	hunkRemote
)

func newConflictHunkResolver(ctx *AppContext, fileIdx int, fc sync.FileConflict) *conflictHunkResolverModel {
	segs := merge.TextSegments(string(fc.LocalData), string(fc.RemoteData))
	m := &conflictHunkResolverModel{
		ctx:      ctx,
		fileIdx:  fileIdx,
		path:     fc.Path,
		segments: segs,
		choices:  map[int]hunkChoice{},
	}
	m.cursor = m.nextPendingFrom(0)
	return m
}

func (m *conflictHunkResolverModel) Title() string {
	return "Hunk resolver — " + shortPath(m.path)
}

func (m *conflictHunkResolverModel) Init() tea.Cmd { return nil }

func (m *conflictHunkResolverModel) CapturesEscape() bool {
	// Esc inside this screen should pop one level back to ConflictResolver,
	// not all the way to Home — user might be mid-review. Opt out of
	// global esc capture; app handles pop normally.
	return false
}

// nextPendingFrom returns the index of the next unresolved hunk starting
// at i, or len(segments) when done.
func (m *conflictHunkResolverModel) nextPendingFrom(i int) int {
	for j := i; j < len(m.segments); j++ {
		if m.segments[j].Hunk == nil {
			continue
		}
		if m.choices[j] == hunkPending {
			return j
		}
	}
	return len(m.segments)
}

// pendingCount / resolvedCount drive the header counter.
func (m *conflictHunkResolverModel) pendingCount() int {
	n := 0
	for i, s := range m.segments {
		if s.Hunk == nil {
			continue
		}
		if m.choices[i] == hunkPending {
			n++
		}
	}
	return n
}

func (m *conflictHunkResolverModel) totalHunks() int {
	n := 0
	for _, s := range m.segments {
		if s.Hunk != nil {
			n++
		}
	}
	return n
}

func (m *conflictHunkResolverModel) allResolved() bool {
	return m.pendingCount() == 0
}

// assemble walks segments and produces the final bytes using the user's
// choices. Pending hunks default to local to avoid empty output — but
// the UI disallows apply while any are pending, so this shouldn't happen.
func (m *conflictHunkResolverModel) assemble() []byte {
	var sb strings.Builder
	for i, s := range m.segments {
		if s.Hunk == nil {
			sb.WriteString(s.Agreed)
			continue
		}
		switch m.choices[i] {
		case hunkRemote:
			sb.WriteString(s.Hunk.Remote)
		default:
			sb.WriteString(s.Hunk.Local)
		}
	}
	return []byte(sb.String())
}

func (m *conflictHunkResolverModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "l":
			if m.cursor < len(m.segments) && m.segments[m.cursor].Hunk != nil {
				m.choices[m.cursor] = hunkLocal
				m.advance()
			}
		case "r":
			if m.cursor < len(m.segments) && m.segments[m.cursor].Hunk != nil {
				m.choices[m.cursor] = hunkRemote
				m.advance()
			}
		case "a":
			if m.allResolved() {
				// Emit the assembled bytes and pop — the parent resolver
				// stores them under the fileIdx and marks as resolved.
				return m, tea.Sequence(
					func() tea.Msg {
						return perKeyResolvedMsg{fileIdx: m.fileIdx, bytes: m.assemble()}
					},
					popScreen(),
				)
			}
		}
	}
	return m, nil
}

// advance moves the cursor to the next unresolved hunk (if any).
func (m *conflictHunkResolverModel) advance() {
	m.cursor = m.nextPendingFrom(m.cursor + 1)
}

func (m *conflictHunkResolverModel) View() string {
	var sb strings.Builder

	total := m.totalHunks()
	done := total - m.pendingCount()

	// Progress chip — matches the file-level conflict resolver's
	// `● 3/7 resolved` pattern. Flips to green when fully resolved.
	chipStyle := theme.ChipNeutral
	if done == total {
		chipStyle = theme.ChipGood
	}
	fmt.Fprintf(&sb, "%s  %s\n\n",
		chipStyle.Render(fmt.Sprintf("● %d / %d hunks", done, total)),
		theme.Hint.Render("· "+m.path))

	if m.allResolved() {
		body := theme.Good.Bold(true).Render("✓ ALL HUNKS RESOLVED") + "\n" +
			theme.Subtle.Render("press a to apply the merged file")
		sb.WriteString(theme.CardClean.Width(56).Render(body) + "\n\n")
		sb.WriteString(renderFooterBar([]footerKey{
			{cap: "a", label: "apply merged file"},
			{cap: "esc", label: "back"},
		}))
		return sb.String()
	}

	if m.cursor >= len(m.segments) {
		sb.WriteString(theme.Hint.Render("no more hunks"))
		return sb.String()
	}
	seg := m.segments[m.cursor]
	hunk := seg.Hunk
	if hunk == nil {
		sb.WriteString(theme.Hint.Render("advancing…"))
		return sb.String()
	}

	sb.WriteString(theme.Secondary.Bold(true).Render(
		fmt.Sprintf("hunk %d of %d", m.hunkIndexOf(m.cursor), total)) + "\n\n")

	// Side labels as chips (matches conflict-key-resolver):
	// [ L ] and [ R ] pills tag the two sides so the user parses
	// "pick one of these" rather than "these are both part of the file".
	sb.WriteString(theme.ChipGood.Render(" L ") + "  " + theme.Secondary.Render("local") + "\n")
	sb.WriteString(renderHunkSide(hunk.Local, theme.Bad) + "\n")
	sb.WriteString(theme.ChipNeutral.Render(" R ") + "  " + theme.Secondary.Render("remote") + "\n")
	sb.WriteString(renderHunkSide(hunk.Remote, theme.Good) + "\n")

	sb.WriteString("\n" + renderFooterBar([]footerKey{
		{cap: "l", label: "take local"},
		{cap: "r", label: "take remote"},
		{cap: "esc", label: "back"},
	}))
	return sb.String()
}

// hunkIndexOf returns the 1-based ordinal of the hunk at segment index i
// (i.e., how many hunks have we seen up to and including this one).
func (m *conflictHunkResolverModel) hunkIndexOf(i int) int {
	n := 0
	for j := 0; j <= i && j < len(m.segments); j++ {
		if m.segments[j].Hunk != nil {
			n++
		}
	}
	return n
}

// renderHunkSide prints each line of s with a colored "│ " gutter so the
// block visually reads as one side's version.
func renderHunkSide(s string, style interface{ Render(...string) string }) string {
	if s == "" {
		return "  " + theme.Hint.Render("(empty)")
	}
	var sb strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		sb.WriteString("  " + style.Render("│") + " " + line + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// shortPath trims long paths for the Title bar so the header doesn't wrap
// awkwardly on small terminals.
func shortPath(p string) string {
	const max = 50
	if len(p) <= max {
		return p
	}
	return "…" + p[len(p)-max+1:]
}
