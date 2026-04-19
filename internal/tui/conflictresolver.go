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
)

func (c resolutionChoice) symbol() string {
	switch c {
	case choiceLocal:
		return theme.Good.Render("[L]")
	case choiceRemote:
		return theme.Secondary.Render("[R]")
	}
	return theme.Warn.Render("[ ?]")
}

// conflictResolverModel shows the list of conflicted files and lets the user
// pick local or remote for each, then apply all.
type conflictResolverModel struct {
	ctx       *AppContext
	conflicts []sync.FileConflict
	choices   []resolutionChoice
	cursor    int
	applying  bool
	err       error
	result    *sync.Result
}

func newConflictResolver(ctx *AppContext, conflicts []sync.FileConflict) *conflictResolverModel {
	return &conflictResolverModel{
		ctx:       ctx,
		conflicts: conflicts,
		choices:   make([]resolutionChoice, len(conflicts)),
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
		return m, nil
	case tea.KeyMsg:
		if m.applying {
			return m, nil
		}
		if m.result != nil {
			return m, popScreen()
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
				m.advance()
			}
		case "r":
			if len(m.conflicts) > 0 {
				m.choices[m.cursor] = choiceRemote
				m.advance()
			}
		case "a":
			if m.allResolved() {
				m.applying = true
				return m, runApplyResolutions(m.ctx, m.conflicts, m.choices)
			}
		}
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

func runApplyResolutions(ctx *AppContext, conflicts []sync.FileConflict, choices []resolutionChoice) tea.Cmd {
	return func() tea.Msg {
		resolutions := map[string][]byte{}
		for i, fc := range conflicts {
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
	sb.WriteString(theme.Hint.Render("l take local • r take remote • ↑↓ move"))
	return sb.String()
}
