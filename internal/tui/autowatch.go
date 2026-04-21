package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"

	"github.com/colinc86/ccsync/internal/ignore"
	syncpkg "github.com/colinc86/ccsync/internal/sync"
)

// Auto-sync watcher wiring. When the user is in SyncMode == auto and the
// repo is bootstrapped, we run a filesystem watcher over ~/.claude and
// ~/.claude.json for the duration of the TUI session. A debounced change
// event produces autoSyncTickMsg which, if the dry-run plan it triggers
// turns out to be clean (only outbound), auto-applies. Conflicts / inbound
// changes just refresh the dashboard so the user sees pending counts.
//
// Subscription pattern: tea doesn't natively support long-lived streams,
// so we run a goroutine that owns the watcher and feeds a channel; a Cmd
// reads one event per invocation. After each event, AppModel re-invokes
// the Cmd to keep listening.

// autoWatchDebounce controls the quiet time we wait for before treating a
// burst of edits (e.g. a multi-file save from an editor) as a single
// trigger. Tuned to match sync.watch's default.
const autoWatchDebounce = 3 * time.Second

// autoWatchStartedMsg fires once on session start, carrying the channel the
// Cmd will read fired events from. AppModel stashes the chan and issues a
// nextAutoWatchEventCmd to begin listening.
type autoWatchStartedMsg struct {
	ch     <-chan struct{}
	cancel context.CancelFunc
}

// autoSyncTickMsg is emitted by the watcher goroutine after the debounce
// quiets. AppModel handles it by kicking a background sync (dry-run first;
// apply only if clean).
type autoSyncTickMsg struct{}

// autoSyncAppliedMsg is the result of a successful background auto-sync.
// Carries nothing meaningful; AppModel uses it to re-trigger the refresh
// cascade so the dashboard reflects the new status.
type autoSyncAppliedMsg struct{ err error }

// startAutoWatchCmd begins watching ~/.claude + ~/.claude.json when the
// session enters auto mode. Returns nil if the repo isn't bootstrapped or
// the user has opted into manual mode — AppModel re-evaluates on settings
// change, but the initial session decides based on launch state.
func startAutoWatchCmd(ctx *AppContext) tea.Cmd {
	if ctx == nil || ctx.State == nil {
		return nil
	}
	if ctx.State.SyncRepoURL == "" {
		return nil
	}
	if !ctx.State.IsAutoMode() {
		return nil
	}
	return func() tea.Msg {
		ch := make(chan struct{}, 1)
		watchCtx, cancel := context.WithCancel(context.Background())
		go runAutoWatch(watchCtx, ctx, ch)
		return autoWatchStartedMsg{ch: ch, cancel: cancel}
	}
}

// nextAutoWatchEventCmd reads exactly one debounced event from ch and
// returns autoSyncTickMsg. AppModel re-issues this after each event so the
// subscription stays live for the whole session.
func nextAutoWatchEventCmd(ch <-chan struct{}) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		_, ok := <-ch
		if !ok {
			return nil
		}
		return autoSyncTickMsg{}
	}
}

// runAutoWatch is the goroutine body. It wraps fsnotify, applies ignore
// rules, and debounces a burst of events into a single channel write. It
// exits cleanly when ctx is canceled (AppModel's teardown path).
func runAutoWatch(ctx context.Context, app *AppContext, out chan<- struct{}) {
	defer close(out)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer w.Close()

	matcher := ignore.New(app.Config.DefaultSyncignore)
	if app.RepoPath != "" {
		if data, err := os.ReadFile(filepath.Join(app.RepoPath, ".syncignore")); err == nil {
			matcher = ignore.New(string(data))
		}
	}

	if app.ClaudeDir != "" {
		_ = addRecursive(w, app.ClaudeDir, matcher)
	}
	if app.ClaudeJSON != "" {
		// claude.json lives at $HOME; watch the directory and filter events.
		_ = w.Add(filepath.Dir(app.ClaudeJSON))
	}

	var (
		mu    sync.Mutex
		timer *time.Timer
	)
	fire := func() {
		select {
		case out <- struct{}{}:
		default: // buffered(1) — coalesce if receiver is busy
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if !relevantEvent(ev, app, matcher) {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
					_ = addRecursive(w, ev.Name, matcher)
				}
			}
			mu.Lock()
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(autoWatchDebounce, fire)
			mu.Unlock()
		case <-w.Errors:
			// best-effort; swallow
		}
	}
}

// addRecursive walks root adding every directory to the watcher. Honors
// ignore rules so we don't bother watching huge dirs like projects/.
func addRecursive(w *fsnotify.Watcher, root string, m *ignore.Matcher) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if m != nil && rel != "." && rel != "" && m.Matches(filepath.ToSlash(rel)+"/") {
			return filepath.SkipDir
		}
		return w.Add(path)
	})
}

// relevantEvent filters fsnotify events down to things we care about.
// Home-dir events are restricted to claude.json; everything under
// ~/.claude is subject to the ignore matcher so e.g. sessions/ or
// cache/ noise doesn't wake us up.
func relevantEvent(ev fsnotify.Event, app *AppContext, m *ignore.Matcher) bool {
	if app.ClaudeJSON != "" && filepath.Dir(ev.Name) == filepath.Dir(app.ClaudeJSON) {
		return ev.Name == app.ClaudeJSON
	}
	if app.ClaudeDir != "" {
		rel, err := filepath.Rel(app.ClaudeDir, ev.Name)
		if err != nil {
			return false
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "..") {
			return false
		}
		if m != nil && m.Matches(rel) {
			return false
		}
	}
	return true
}

// maybeLaunchAutoSync returns a tea.Cmd that dispatches a background
// auto-sync when the first launch-driven plan refresh shows pending
// actions with no conflicts. Returns nil for any of:
//   - manual mode (the auto-mode gate is the whole point)
//   - already latched AutoSyncedOnLaunch (this is a one-shot)
//   - in-flight AutoSyncing (don't race two real syncs)
//   - no plan yet, or an error fetching it
//   - plan has conflicts (auto never picks a side)
//   - plan is fully clean (nothing to do; common case)
func maybeLaunchAutoSync(ctx *AppContext) tea.Cmd {
	if ctx == nil || ctx.State == nil {
		return nil
	}
	if !ctx.State.IsAutoMode() {
		return nil
	}
	if ctx.AutoSyncedOnLaunch || ctx.AutoSyncing {
		return nil
	}
	if ctx.Plan == nil || ctx.PlanErr != nil {
		return nil
	}
	if len(ctx.Plan.Conflicts) > 0 {
		return nil
	}
	s := ctx.Summary()
	if s.Outbound == 0 && s.Inbound == 0 {
		return nil
	}
	ctx.AutoSyncedOnLaunch = true
	ctx.AutoSyncing = true
	return runAutoSyncCmd(ctx)
}

// runAutoSyncCmd runs a full sync.Run in the background and emits
// autoSyncAppliedMsg on completion. The sync is a no-op when nothing has
// changed (gate inside sync.Run), so firing this on every debounced tick
// is safe even when the user's edit didn't touch a tracked file.
func runAutoSyncCmd(ctx *AppContext) tea.Cmd {
	if ctx == nil || ctx.State == nil || ctx.State.SyncRepoURL == "" {
		return nil
	}
	return func() tea.Msg {
		in, err := buildSyncInputs(ctx, false)
		if err != nil {
			return autoSyncAppliedMsg{err: err}
		}
		// Dry-run first so we can bail cleanly when the plan has conflicts
		// — auto-sync never takes a side on a merge; that's a user choice.
		dryIn := in
		dryIn.DryRun = true
		dryRes, err := syncpkg.Run(context.Background(), dryIn, nil)
		if err != nil {
			return autoSyncAppliedMsg{err: err}
		}
		if len(dryRes.Plan.Conflicts) > 0 {
			// Leave the conflicts surfaced in the plan; Home dashboard
			// will show them on the next refresh.
			return autoSyncAppliedMsg{}
		}
		// Execute the real sync. sync.Run already suppresses no-op commits,
		// and RunWithRetry absorbs the rare non-fast-forward race so the
		// user never sees a raw git error from the silent auto-sync path.
		_, err = syncpkg.RunWithRetry(context.Background(), in, nil)
		return autoSyncAppliedMsg{err: err}
	}
}
