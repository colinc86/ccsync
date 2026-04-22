package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/manifest"
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

	// pendingPromotes is the list of files the user tagged for promote
	// in the review screen. Run serially after the main sync completes
	// (and only if it committed successfully without conflicts).
	pendingPromotes []promoteIntent
	promoteErr      error
	promoteRan      bool
}

// promoteIntent is the information a syncModel needs to run
// sync.PromotePath after the main sync finishes: the path being
// promoted and the source profile (destination is currently always
// the user's "shared" base — resolved at execution time via the
// active profile's extends chain).
type promoteIntent struct {
	RepoRelPath string // "claude/skills/foo.md"
	From        string // source profile name (usually the active one)
	To          string // destination profile name
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

// newSyncWithPromotes builds a syncModel that runs the usual sync and
// then promotes each of the named paths from source → dest profile.
// Used by the review screen to queue "share this file with other
// profiles after the push lands."
func newSyncWithPromotes(ctx *AppContext, promotes []promoteIntent) *syncModel {
	m := newSync(ctx)
	m.pendingPromotes = promotes
	return m
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
			res, err := sync.RunWithRetry(context.Background(), in, events)
			close(events)
			doneCh <- doneMsg{res: res, err: err}
		}()
		return startedMsg{events: events, done: doneCh}
	}
}

// promotesDoneMsg fires once every queued promote has run (or bailed
// on the first error). Carries the first error if any.
type promotesDoneMsg struct{ err error }

// runPromotes kicks off a goroutine-backed command that runs each
// pending promote serially. Order is preserved because the underlying
// git operations have to commit sequentially anyway. If any step
// fails we stop and surface the error; remaining items are left as
// local overrides, which the user can retry from Browse Tracked Files.
func runPromotes(ctx *AppContext, intents []promoteIntent) tea.Cmd {
	return func() tea.Msg {
		in, err := buildSyncInputs(ctx, false)
		if err != nil {
			return promotesDoneMsg{err: err}
		}
		for _, p := range intents {
			if err := sync.PromotePath(context.Background(), in, p.RepoRelPath, p.From, p.To); err != nil {
				return promotesDoneMsg{err: err}
			}
		}
		return promotesDoneMsg{}
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
		// sync.Run advances LastSyncedSHA on disk; pull that back into the
		// TUI's in-memory copy so Home + status bar reflect the new state,
		// and recompute the cached plan so push/pull counts are fresh.
		m.ctx.RefreshState()
		// If the review screen queued any promotes, run them now that
		// the main sync committed. Only runs once per syncModel via
		// promoteRan; subsequent plan-refresh cycles don't re-fire.
		if !m.promoteRan && len(m.pendingPromotes) > 0 && msg.err == nil {
			m.promoteRan = true
			return m, tea.Batch(runPromotes(m.ctx, m.pendingPromotes), refreshPlanCmd(m.ctx))
		}
		return m, refreshPlanCmd(m.ctx)
	case promotesDoneMsg:
		m.promoteErr = msg.err
		// Re-refresh so the dashboard reflects the promote commit.
		return m, refreshPlanCmd(m.ctx)
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
			// Sync is the end of a Home → SyncPreview → Sync chain. The
			// footer says "return to home" — actually do that.
			return m, popToRoot()
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
			// Human recap — "Added 2 agents, changed CLAUDE.md" — so the
			// user knows at a glance what just happened without reading
			// git log. Reuses the semantic extractor from commit.go.
			if recap := renderSyncRecap(r.Plan); recap != "" {
				sb.WriteString(theme.Hint.Render(recap) + "\n")
			}
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

// renderSyncRecap summarizes a completed plan as a one-liner using the
// same semantic labels the commit body uses. Skips excluded + no-op rows,
// collapses Added/Changed/Removed into compact phrases. Returns empty
// string when there's nothing worth saying.
func renderSyncRecap(plan sync.Plan) string {
	var added, changed, removed []string
	for _, a := range plan.Actions {
		if a.ExcludedByProfile {
			continue
		}
		if a.Action == manifest.ActionNoOp {
			continue
		}
		label := sync.SemanticLabel(a.Path, nil, nil)
		switch a.Action {
		case manifest.ActionAddRemote, manifest.ActionAddLocal:
			added = append(added, label)
		case manifest.ActionPush, manifest.ActionPull, manifest.ActionMerge:
			changed = append(changed, label)
		case manifest.ActionDeleteRemote, manifest.ActionDeleteLocal:
			removed = append(removed, label)
		}
	}
	var parts []string
	if s := recapPhrase("Added", added); s != "" {
		parts = append(parts, s)
	}
	if s := recapPhrase("Changed", changed); s != "" {
		parts = append(parts, s)
	}
	if s := recapPhrase("Removed", removed); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// recapPhrase turns a bucket into something like "Added 2 agents (foo, bar)"
// or "Changed 1 skill (research-quick)". Caps at 3 named items to keep the
// one-liner short; anything beyond that reads as "+N more".
func recapPhrase(verb string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	const maxShown = 3
	shown := items
	extra := 0
	if len(shown) > maxShown {
		shown = shown[:maxShown]
		extra = len(items) - maxShown
	}
	tail := strings.Join(shown, ", ")
	if extra > 0 {
		tail = fmt.Sprintf("%s, +%d more", tail, extra)
	}
	return fmt.Sprintf("%s %d (%s)", verb, len(items), tail)
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
