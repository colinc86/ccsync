package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

type syncModel struct {
	ctx       *AppContext
	events    []sync.Event
	result    *sync.Result
	err       error
	done      bool
	spin      spinner.Model
	eventCh   chan sync.Event
	doneCh    chan doneMsg
	onlyPaths map[string]bool // if non-nil, this sync is a selective one-shot
}

type doneMsg struct {
	res sync.Result
	err error
}

type eventMsg sync.Event

type startedMsg struct {
	events chan sync.Event
	done   chan doneMsg
}

func newSync(ctx *AppContext) *syncModel {
	return &syncModel{ctx: ctx, spin: newSpinner()}
}

func (m *syncModel) Title() string { return "Syncing" }

func (m *syncModel) Init() tea.Cmd {
	return tea.Batch(startSync(m.ctx, m.onlyPaths), m.spin.Tick)
}

func startSync(ctx *AppContext, onlyPaths map[string]bool) tea.Cmd {
	return func() tea.Msg {
		events := make(chan sync.Event, 128)
		doneCh := make(chan doneMsg, 1)
		go func() {
			in, err := buildSyncInputs(ctx, false)
			if err != nil {
				doneCh <- doneMsg{err: err}
				close(events)
				return
			}
			in.OnlyPaths = onlyPaths
			res, err := sync.Run(context.Background(), in, events)
			close(events)
			doneCh <- doneMsg{res: res, err: err}
		}()
		return startedMsg{events: events, done: doneCh}
	}
}

func awaitNext(events chan sync.Event, done chan doneMsg) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev, ok := <-events:
			if !ok {
				return <-done
			}
			return eventMsg(ev)
		case d := <-done:
			return d
		}
	}
}

func (m *syncModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.done {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case startedMsg:
		m.eventCh = msg.events
		m.doneCh = msg.done
		return m, awaitNext(m.eventCh, m.doneCh)
	case eventMsg:
		m.events = append(m.events, sync.Event(msg))
		return m, awaitNext(m.eventCh, m.doneCh)
	case doneMsg:
		m.done = true
		m.err = msg.err
		if msg.err == nil {
			r := msg.res
			m.result = &r
		}
		return m, nil
	case tea.KeyMsg:
		if !m.done {
			return m, nil
		}
		switch msg.String() {
		case "r":
			if m.result != nil && len(m.result.Plan.Conflicts) > 0 {
				return m, switchTo(newConflictResolver(m.ctx, m.result.Plan.Conflicts))
			}
		case "v":
			if m.result != nil && len(m.result.MissingSecrets) > 0 {
				return m, switchTo(newRedactionReview(m.ctx, m.result.MissingSecrets))
			}
		default:
			return m, popScreen()
		}
	}
	return m, nil
}

func (m *syncModel) View() string {
	var sb strings.Builder
	for _, e := range m.events {
		stage := theme.Secondary.Render(fmt.Sprintf("%-10s", stageGlyph(e.Stage)+e.Stage))
		line := stage + " " + e.Message
		if e.Path != "" {
			line += " " + theme.Hint.Render(e.Path)
		}
		sb.WriteString(line + "\n")
	}
	if !m.done {
		sb.WriteString("\n" + m.spin.View() + " " + theme.Hint.Render(currentStage(m.events)))
		return sb.String()
	}
	sb.WriteString("\n")
	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: ") + m.err.Error() + "\n")
		sb.WriteString("\n" + theme.Hint.Render("press any key to go back"))
		return sb.String()
	}
	if m.result != nil {
		r := m.result
		if r.CommitSHA != "" {
			short := r.CommitSHA
			if len(short) > 7 {
				short = short[:7]
			}
			sb.WriteString(theme.Good.Render("committed ") + short + "\n")
		} else {
			sb.WriteString(theme.Good.Render("no changes to push") + "\n")
		}
		if r.SnapshotID != "" {
			sb.WriteString(theme.Hint.Render("pre-sync snapshot: "+r.SnapshotID) + "\n")
		}
		if len(r.MissingSecrets) > 0 {
			sb.WriteString(theme.Warn.Render(fmt.Sprintf("%d file(s) skipped (missing secrets)", len(r.MissingSecrets))) + "\n")
			for _, p := range r.MissingSecrets {
				sb.WriteString("  " + p + "\n")
			}
		}
		if len(r.Plan.Conflicts) > 0 {
			sb.WriteString(theme.Bad.Render(fmt.Sprintf("%d conflict(s)", len(r.Plan.Conflicts))) + "   " + theme.Primary.Render("r ") + "resolve\n")
		}
		if len(r.MissingSecrets) > 0 {
			sb.WriteString(theme.Warn.Render(fmt.Sprintf("%d missing secret(s)", len(r.MissingSecrets))) + "   " + theme.Primary.Render("v ") + "fill\n")
		}
	}
	sb.WriteString("\n" + theme.Hint.Render("any other key returns to home"))
	return sb.String()
}

// stageGlyph returns a short icon for a sync stage so the streaming log has
// visual rhythm instead of a wall of text.
func stageGlyph(stage string) string {
	switch stage {
	case "fetch":
		return "↓ "
	case "discover":
		return "◎ "
	case "snapshot":
		return "⎘ "
	case "redaction":
		return "✱ "
	case "commit":
		return "✎ "
	case "push":
		return "↑ "
	case "done":
		return "✓ "
	}
	return "· "
}

// currentStage returns a short hint describing what sync is doing right now,
// based on the most recent event. Shown alongside the spinner.
func currentStage(events []sync.Event) string {
	if len(events) == 0 {
		return "starting sync…"
	}
	last := events[len(events)-1]
	if last.Message != "" {
		return last.Message + "…"
	}
	return last.Stage + "…"
}
