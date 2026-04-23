package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/snapshot"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

type historyKind int

const (
	historyCommit historyKind = iota
	historySnapshot
)

type historyItem struct {
	kind      historyKind
	timestamp time.Time
	commitSHA string
	subject   string
	snapID    string
	snapOp    string
	snapFiles int
}

type syncHistoryModel struct {
	ctx      *AppContext
	items    []historyItem
	filtered []int // indexes into items that match the current filter
	cursor   int
	err      error
	message  string

	filtering    bool
	filterIn     textinput.Model
	calendarView bool // when true, render the activity calendar instead of the list
}

func newSyncHistory(ctx *AppContext) *syncHistoryModel {
	fi := textinput.New()
	fi.Placeholder = "filter…"
	fi.CharLimit = 48
	fi.Width = 32
	m := &syncHistoryModel{ctx: ctx, filterIn: fi}
	m.reload()
	return m
}

func (m *syncHistoryModel) reload() {
	m.items = nil

	if m.ctx.State.SyncRepoURL != "" {
		repo, err := gitx.Open(m.ctx.RepoPath)
		if err == nil {
			if log, err := repo.Log(30); err == nil {
				for _, l := range log {
					m.items = append(m.items, historyItem{
						kind:      historyCommit,
						timestamp: l.When,
						commitSHA: l.SHA,
						subject:   firstLine(l.Message),
					})
				}
			}
		}
	}

	snaps, _ := snapshot.List(filepath.Join(m.ctx.StateDir, "snapshots"))
	for _, s := range snaps {
		m.items = append(m.items, historyItem{
			kind:      historySnapshot,
			timestamp: s.CreatedAt,
			snapID:    s.ID,
			snapOp:    s.Op,
			snapFiles: len(s.Files),
		})
	}

	sort.Slice(m.items, func(i, j int) bool {
		return m.items[i].timestamp.After(m.items[j].timestamp)
	})
	m.applyFilter()
}

// applyFilter rebuilds m.filtered from the current filter query. An empty
// query matches everything. Matching is case-insensitive substring on the
// rendered line content (commit SHA, subject, or snapshot op).
func (m *syncHistoryModel) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filterIn.Value()))
	m.filtered = m.filtered[:0]
	for i, it := range m.items {
		if q == "" || historyItemMatches(it, q) {
			m.filtered = append(m.filtered, i)
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = 0
	}
}

func historyItemMatches(it historyItem, q string) bool {
	switch it.kind {
	case historyCommit:
		return strings.Contains(strings.ToLower(it.commitSHA), q) ||
			strings.Contains(strings.ToLower(it.subject), q)
	case historySnapshot:
		return strings.Contains(strings.ToLower(it.snapID), q) ||
			strings.Contains(strings.ToLower(it.snapOp), q)
	}
	return false
}

func (m *syncHistoryModel) Title() string { return "Sync history" }
func (m *syncHistoryModel) Init() tea.Cmd { return nil }

func (m *syncHistoryModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
		switch msg.String() {
		case "/":
			m.filtering = true
			m.filterIn.Focus()
			return m, textinput.Blink
		case "c":
			// clear filter
			m.filterIn.SetValue("")
			m.applyFilter()
			return m, nil
		case "v":
			m.calendarView = !m.calendarView
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
		case "b":
			if len(m.filtered) == 0 {
				return m, nil
			}
			it := m.items[m.filtered[m.cursor]]
			if it.kind == historySnapshot {
				err := snapshot.Restore(filepath.Join(m.ctx.StateDir, "snapshots"), it.snapID)
				if err != nil {
					m.err = err
					return m, nil
				}
				m.message = "restored local files from snapshot " + it.snapID
				m.err = nil
				m.reload()
				return m, nil
			}
			// Commit rollback: materialize target tree as a new forward commit.
			in, err := buildSyncInputs(m.ctx, false)
			if err != nil {
				m.err = err
				return m, nil
			}
			res, err := sync.RollbackTo(context.Background(), in, it.commitSHA)
			if err != nil {
				m.err = err
				return m, nil
			}
			short := res.CommitSHA
			if len(short) > 7 {
				short = short[:7]
			}
			if res.CommitSHA != "" {
				m.message = fmt.Sprintf("rolled back to %s (new commit %s)",
					shortSHA(it.commitSHA), short)
			} else {
				m.message = "already matches target"
			}
			m.err = nil
			// Rollback writes a new commit and updates LastSyncedSHA on disk;
			// refresh so the status bar's freshness check sees it.
			m.ctx.RefreshState()
			m.reload()
			return m, refreshPlanCmd(m.ctx)
		}
	}
	return m, nil
}

func (m *syncHistoryModel) View() string {
	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(renderError(m.err) + "\n\n")
	}
	if m.message != "" {
		sb.WriteString(theme.Good.Render("✓ "+m.message) + "\n\n")
	}
	if m.calendarView {
		var logs []gitx.LogEntry
		if repo, err := gitx.Open(m.ctx.RepoPath); err == nil {
			logs, _ = repo.Log(500) // enough to cover the 26-week window
		}
		sb.WriteString(renderActivityCalendar(logs))
		sb.WriteString("\n\n" + renderFooterBar([]footerKey{
			{cap: "v", label: "list view"},
			{cap: "esc", label: "back"},
		}))
		return sb.String()
	}
	if m.filtering || m.filterIn.Value() != "" {
		sb.WriteString(theme.Secondary.Render("filter: ") + m.filterIn.View())
		sb.WriteString(fmt.Sprintf("  %s\n\n",
			theme.Hint.Render(fmt.Sprintf("(%d/%d)", len(m.filtered), len(m.items)))))
	}

	if len(m.filtered) == 0 {
		if m.filterIn.Value() != "" {
			sb.WriteString(theme.Hint.Render("no matches — press c to clear filter"))
		} else {
			sb.WriteString(theme.Hint.Render("no history yet — run a sync first"))
		}
		return sb.String()
	}

	// Summary chips — split commits vs snapshots so the user can
	// see "there are 42 syncs and 18 snapshots to explore" before
	// reading any individual row.
	var commits, snaps int
	for _, it := range m.items {
		switch it.kind {
		case historyCommit:
			commits++
		case historySnapshot:
			snaps++
		}
	}
	chips := []string{}
	if commits > 0 {
		chips = append(chips, theme.ChipNeutral.Render(fmt.Sprintf("● %d commits", commits)))
	}
	if snaps > 0 {
		chips = append(chips, theme.ChipGood.Render(fmt.Sprintf("⎘ %d snapshots", snaps)))
	}
	if len(chips) > 0 {
		sb.WriteString(strings.Join(chips, theme.Rule.Render("  ·  ")) + "\n\n")
	}

	start, end := windowAround(m.cursor, len(m.filtered), 18)
	for i := start; i < end; i++ {
		it := m.items[m.filtered[i]]
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		ts := it.timestamp.Local().Format("01-02 15:04:05")
		switch it.kind {
		case historyCommit:
			short := it.commitSHA
			if len(short) > 7 {
				short = short[:7]
			}
			// ● in secondary as a commit glyph — more scannable than
			// the word "commit " padded out.
			fmt.Fprintf(&sb, "%s%s %s %s  %s\n",
				cursor, theme.Secondary.Render("●"),
				theme.Hint.Render(ts), theme.Primary.Render(short), it.subject)
		case historySnapshot:
			// ⎘ in good — snapshots are safety-net rollback anchors,
			// the "something went right" element of history.
			fmt.Fprintf(&sb, "%s%s %s %s  %s (%d files)\n",
				cursor, theme.Good.Render("⎘"),
				theme.Hint.Render(ts), theme.Primary.Render(it.snapID), it.snapOp, it.snapFiles)
		}
	}
	if len(m.filtered) > 18 {
		sb.WriteString(theme.Hint.Render(fmt.Sprintf(
			"\n  %d–%d of %d · ↑↓ to scroll", start+1, end, len(m.filtered))))
	}

	sb.WriteString("\n" + renderFooterBar([]footerKey{
		{cap: "b", label: "rollback"},
		{cap: "/", label: "filter"},
		{cap: "v", label: "calendar"},
		{cap: "↑↓", label: "move"},
		{cap: "c", label: "clear filter"},
	}))
	return sb.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
