package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/humanize"
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
			// Belt-and-braces: if anything inside sync.RunWithRetry
			// panics (nil map write, slice-out-of-bounds in the merge
			// engine, etc.), recover it to a done-with-error instead
			// of letting the goroutine tear down with events still
			// open and doneCh still empty — awaitNext would then block
			// forever and the TUI would hang without error feedback.
			// Also guarantees close(events) fires on every exit path.
			defer close(events)
			defer func() {
				if r := recover(); r != nil {
					// doneCh is buffered(1) and nothing else writes
					// to it here (the panic aborted before the happy
					// send), so this won't block.
					doneCh <- doneMsg{err: fmt.Errorf("sync panicked: %v", r)}
				}
			}()
			in, err := buildSyncInputs(ctx, false)
			if err != nil {
				doneCh <- doneMsg{err: err}
				return
			}
			in.OnlyPaths = onlyPaths
			res, err := sync.RunWithRetry(context.Background(), in, events)
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

		// Toast — success with human-readable change counts; error
		// with the friendly translation. Change counts beat short
		// SHAs for user-facing language: "+1 ~2 -0" tells the user
		// exactly what happened without them cross-referencing a
		// commit hash they won't remember.
		var toast tea.Cmd
		if msg.err != nil {
			toast = showToast("sync failed: "+msg.err.Error(), toastError)
		} else if msg.res.CommitSHA != "" {
			added, modified, deleted := msg.res.Plan.Summary()
			var changes []string
			if added > 0 {
				changes = append(changes, fmt.Sprintf("+%d", added))
			}
			if modified > 0 {
				changes = append(changes, fmt.Sprintf("~%d", modified))
			}
			if deleted > 0 {
				changes = append(changes, fmt.Sprintf("-%d", deleted))
			}
			msgText := "sync complete"
			if len(changes) > 0 {
				msgText += " · " + strings.Join(changes, " ")
			}
			toast = showToast(msgText, toastSuccess)
		} else {
			toast = showToast("already in sync", toastInfo)
		}

		// If the review screen queued any promotes, run them now that
		// the main sync committed. Only runs once per syncModel via
		// promoteRan; subsequent plan-refresh cycles don't re-fire.
		if !m.promoteRan && len(m.pendingPromotes) > 0 && msg.err == nil {
			m.promoteRan = true
			return m, tea.Batch(runPromotes(m.ctx, m.pendingPromotes), refreshPlanCmd(m.ctx), toast)
		}
		return m, tea.Batch(refreshPlanCmd(m.ctx), toast)
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
	sb.WriteString(theme.Wordmark("syncing") + "\n\n")

	// Step tree: render the canonical stage sequence with a checkmark
	// for completed stages, a spinner for the active one, and a muted
	// dot for the still-to-come. Gives sync-in-flight a sense of
	// forward motion instead of a scrolling log that looks identical
	// every frame. Stages that a given sync skips (e.g. "redaction"
	// when nothing with secrets changed) collapse away to keep the
	// tree tight.
	sb.WriteString(renderStageTree(m.events, m.spin.View(), m.done) + "\n")

	if !m.done {
		return sb.String()
	}
	sb.WriteString("\n")
	if m.err != nil {
		sb.WriteString(renderError(m.err) + "\n")
		sb.WriteString("\n" + renderFooterBar([]footerKey{
			{cap: "any key", label: "return", primary: true},
		}))
		return sb.String()
	}
	if m.result != nil {
		r := m.result
		// Result card — summarises what just happened in a single
		// green-bordered pane instead of scattered lines. The
		// follow-up action chips (resolve / fill secrets) live
		// below the card so the keyboard verb stays discoverable.
		sb.WriteString(renderSyncResultCard(r) + "\n")
		var chips []footerKey
		if len(r.Plan.Conflicts) > 0 {
			chips = append(chips, footerKey{cap: "r", label: "resolve conflicts", primary: true})
		}
		if len(r.MissingSecrets) > 0 {
			chips = append(chips, footerKey{cap: "v", label: "fill missing secrets", primary: len(chips) == 0})
		}
		chips = append(chips, footerKey{cap: "any key", label: "return"})
		sb.WriteString("\n" + renderFooterBar(chips))
	}
	return sb.String()
}

// stageOrder is the canonical left-to-right sync progression. Stages
// not in this list (e.g. "align") are appended in event-order below.
var stageOrder = []struct{ key, label string }{
	{"fetch", "fetching remote"},
	{"discover", "walking local files"},
	{"snapshot", "snapshotting pre-sync state"},
	{"redaction", "handling secrets"},
	{"commit", "committing changes"},
	{"push", "pushing to remote"},
	{"done", "finalising"},
}

// renderStageTree turns the streaming Events channel into a static
// tree with completed ✓, in-flight spinner, and muted-◦ upcoming.
// `frame` is the current spinner glyph (caller passes spin.View()).
// After the sync has settled, every stage renders with its final
// glyph — either ✓ for stages we saw, or dim-◦ for stages that
// didn't fire (push-only sync skips snapshot, etc.).
func renderStageTree(events []sync.Event, frame string, done bool) string {
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Stage] = true
	}
	// Index the latest stage we've observed so the "current" one
	// (most recently seen, not yet followed by a later stage) gets
	// the spinner.
	latest := ""
	if len(events) > 0 {
		latest = events[len(events)-1].Stage
	}

	var sb strings.Builder
	for _, s := range stageOrder {
		var glyph string
		var label string
		switch {
		case !seen[s.key] && done:
			// Stage didn't fire this sync; render dim so the user
			// sees the canonical flow but doesn't wonder what was
			// skipped.
			glyph = theme.Hint.Render("◦")
			label = theme.Hint.Render(s.label + " (skipped)")
		case !seen[s.key]:
			glyph = theme.Hint.Render("◦")
			label = theme.Hint.Render(s.label)
		case s.key == latest && !done:
			// The active stage. Animate with the spinner frame and
			// bold the label so the eye knows what's happening now.
			glyph = frame
			label = theme.Primary.Render(s.label)
		default:
			glyph = theme.Good.Render("✓")
			label = theme.Secondary.Render(s.label)
		}
		fmt.Fprintf(&sb, "  %s  %s\n", glyph, label)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// renderSyncResultCard is the post-sync summary: what committed,
// what recap to show, what follow-ups need user attention. Rendered
// inside a clean-state card (green border) for committed syncs, a
// pending-state card (warm border) for no-op syncs, and a
// conflict-state card when there are unresolved conflicts.
func renderSyncResultCard(r *sync.Result) string {
	var sb strings.Builder
	card := theme.CardClean

	if r.CommitSHA != "" {
		short := r.CommitSHA
		if len(short) > 7 {
			short = short[:7]
		}
		sb.WriteString(theme.Good.Bold(true).Render("✓ COMMITTED") + "  " +
			theme.Hint.Render(short) + "\n")
		if recap := renderSyncRecap(r.Plan); recap != "" {
			sb.WriteString(theme.Subtle.Render(recap) + "\n")
		}
	} else {
		sb.WriteString(theme.Good.Bold(true).Render("✓ ALREADY IN SYNC") + "\n")
		sb.WriteString(theme.Subtle.Render("no local changes to push") + "\n")
	}
	if r.SnapshotID != "" {
		sb.WriteString(theme.Hint.Render("snapshot: "+r.SnapshotID) + "\n")
	}
	if len(r.MissingSecrets) > 0 {
		card = theme.CardPending
		sb.WriteString("\n" + theme.Warn.Render(
			humanize.Count(len(r.MissingSecrets), "file")+" skipped (missing secrets)") + "\n")
		max := 5
		for i, p := range r.MissingSecrets {
			if i >= max {
				fmt.Fprintf(&sb, theme.Hint.Render("  … %d more\n"), len(r.MissingSecrets)-max)
				break
			}
			sb.WriteString("  " + theme.Hint.Render(p) + "\n")
		}
	}
	if len(r.Plan.Conflicts) > 0 {
		card = theme.CardConflict
		sb.WriteString("\n" + theme.Bad.Render(
			humanize.Count(len(r.Plan.Conflicts), "conflict unresolved")) + "\n")
	}
	return card.Width(56).Render(strings.TrimRight(sb.String(), "\n"))
}

// renderSyncRecap summarizes a completed plan as a one-liner using
// past-tense verbs ("Added 2 agents, Changed CLAUDE.md"). Called on
// the Sync screen after a commit lands.
func renderSyncRecap(plan sync.Plan) string {
	return renderRecapVerbs(plan, "Added", "Changed", "Removed")
}

// renderSyncPreview is the imperative-tense counterpart: "Add 2
// agents, Change CLAUDE.md". Called on the Home dashboard to give
// the user a specific-file preview of what the next sync will do —
// more informative than the status badge's raw counts.
func renderSyncPreview(plan sync.Plan) string {
	return renderRecapVerbs(plan, "Add", "Change", "Remove")
}

func renderRecapVerbs(plan sync.Plan, vAdd, vChange, vRemove string) string {
	var added, changed, removed []string
	for _, a := range plan.Actions {
		if a.ExcludedByProfile || a.ExcludedByDeny {
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
	if s := recapPhrase(vAdd, added); s != "" {
		parts = append(parts, s)
	}
	if s := recapPhrase(vChange, changed); s != "" {
		parts = append(parts, s)
	}
	if s := recapPhrase(vRemove, removed); s != "" {
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

