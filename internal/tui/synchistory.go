package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	ctx     *AppContext
	items   []historyItem
	cursor  int
	err     error
	message string
}

func newSyncHistory(ctx *AppContext) *syncHistoryModel {
	m := &syncHistoryModel{ctx: ctx}
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
}

func (m *syncHistoryModel) Title() string { return "Sync history" }
func (m *syncHistoryModel) Init() tea.Cmd { return nil }

func (m *syncHistoryModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		case "b":
			if len(m.items) == 0 {
				return m, nil
			}
			it := m.items[m.cursor]
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
			m.reload()
		}
	}
	return m, nil
}

func (m *syncHistoryModel) View() string {
	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	}
	if m.message != "" {
		sb.WriteString(theme.Good.Render(m.message) + "\n\n")
	}
	if len(m.items) == 0 {
		sb.WriteString(theme.Hint.Render("no history yet — run a sync first"))
		return sb.String()
	}

	start, end := windowAround(m.cursor, len(m.items), 18)
	for i := start; i < end; i++ {
		it := m.items[i]
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
			sb.WriteString(fmt.Sprintf("%s%s %s %s  %s\n",
				cursor, theme.Secondary.Render("commit  "),
				theme.Hint.Render(ts), theme.Primary.Render(short), it.subject))
		case historySnapshot:
			sb.WriteString(fmt.Sprintf("%s%s %s %s  %s (%d files)\n",
				cursor, theme.Good.Render("snapshot"),
				theme.Hint.Render(ts), theme.Primary.Render(it.snapID), it.snapOp, it.snapFiles))
		}
	}
	sb.WriteString("\n" + theme.Primary.Render("b ") + "rollback (snapshot only) • " + theme.Hint.Render("↑↓ move"))
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
