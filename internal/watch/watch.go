// Package watch provides an always-on file-change watcher that triggers a
// ccsync sync when the tracked Claude Code config changes. Debounced so a
// burst of edits (like saving several files from an editor) results in one
// sync run, not many.
package watch

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/colinc86/ccsync/internal/humanize"
	"github.com/colinc86/ccsync/internal/ignore"
	syncpkg "github.com/colinc86/ccsync/internal/sync"
)

// Inputs describes what to watch and how to run each sync.
type Inputs struct {
	SyncInputs syncpkg.Inputs
	Debounce   time.Duration   // minimum quiet time before a sync fires
	Out        io.Writer       // progress log sink (os.Stdout in CLI)
	Ignore     *ignore.Matcher // optional .syncignore matcher to skip noisy events
}

// Run blocks until ctx is canceled or a fatal error occurs. It watches the
// Claude config tree for changes and runs sync.Run when the file system
// quiets for `Debounce` time. Bails without retrying on conflicts so the
// user deals with them in the TUI.
func Run(ctx context.Context, in Inputs) error {
	if in.Out == nil {
		in.Out = io.Discard
	}
	if in.Debounce <= 0 {
		in.Debounce = 10 * time.Second
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify: %w", err)
	}
	defer w.Close()

	if err := addRecursive(w, in.SyncInputs.ClaudeDir, in.Ignore); err != nil {
		return err
	}
	if in.SyncInputs.ClaudeJSON != "" {
		// fsnotify auto-adds watches on file-creation inside a watched dir,
		// but claude.json lives at $HOME — we watch the home dir with a filter.
		if err := w.Add(filepath.Dir(in.SyncInputs.ClaudeJSON)); err != nil {
			return fmt.Errorf("watch %s: %w", in.SyncInputs.ClaudeJSON, err)
		}
	}

	fmt.Fprintf(in.Out, "watching %s (debounce %s)\n", in.SyncInputs.ClaudeDir, in.Debounce)

	// Coalescing state. syncing=true means a sync is in flight; events
	// that arrive while syncing set pending=true so we re-fire once the
	// in-flight sync finishes. Pre-v0.6.6 those events were silently
	// dropped, which meant rapid edits during a slow sync could never
	// land — the user's last edit looked lost until they poked a file
	// again.
	var (
		mu      sync.Mutex
		timer   *time.Timer
		syncing bool
		pending bool
	)

	var fire func()
	fire = func() {
		mu.Lock()
		if syncing {
			// Remember that more events came in; fire again after.
			pending = true
			mu.Unlock()
			return
		}
		syncing = true
		pending = false
		mu.Unlock()

		fmt.Fprintf(in.Out, "sync triggered at %s\n", time.Now().Local().Format("15:04:05"))
		res, err := syncpkg.RunWithRetry(ctx, in.SyncInputs, nil)
		if err != nil {
			fmt.Fprintf(in.Out, "  error: %v\n", err)
		} else {
			added, modified, deleted := res.Plan.Summary()
			short := res.CommitSHA
			if len(short) > 7 {
				short = short[:7]
			}
			if res.CommitSHA != "" {
				fmt.Fprintf(in.Out, "  commit %s  +%d ~%d -%d\n", short, added, modified, deleted)
			} else {
				fmt.Fprintf(in.Out, "  no changes to commit\n")
			}
			if len(res.Plan.Conflicts) > 0 {
				fmt.Fprintf(in.Out, "  %s — open the TUI to resolve\n", humanize.Count(len(res.Plan.Conflicts), "conflict"))
			}
			if len(res.MissingSecrets) > 0 {
				fmt.Fprintf(in.Out, "  %s skipped due to missing secrets\n", humanize.Count(len(res.MissingSecrets), "file"))
			}
		}

		mu.Lock()
		syncing = false
		shouldRefire := pending
		pending = false
		mu.Unlock()

		if shouldRefire {
			// Re-fire through a debounce window so another flurry
			// of events can coalesce rather than cascading.
			mu.Lock()
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(in.Debounce, fire)
			mu.Unlock()
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(in.Out, "watch error: %v\n", err)
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if !relevant(ev, in) {
				continue
			}
			// Newly-created directories: add to the watcher so we catch
			// changes inside them too.
			if ev.Op&fsnotify.Create != 0 {
				if st, err := statInfo(ev.Name); err == nil && st.IsDir() {
					_ = addRecursive(w, ev.Name, in.Ignore)
				}
			}
			mu.Lock()
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(in.Debounce, fire)
			mu.Unlock()
		}
	}
}

// addRecursive walks root adding every directory to the watcher. Honors the
// matcher so we don't watch ignored subtrees (e.g. sessions/, cache/).
func addRecursive(w *fsnotify.Watcher, root string, m *ignore.Matcher) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort
		}
		if !d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr == nil && m != nil && rel != "." && m.Matches(filepath.ToSlash(rel)+"/") {
			return filepath.SkipDir
		}
		return w.Add(path)
	})
}

// relevant filters out events that shouldn't trigger a sync. An event
// is relevant only if:
//
//   - it's the ClaudeJSON file exactly (the home-dir watcher catches
//     every file change in $HOME; we care about one), OR
//   - it's under ClaudeDir and not ignored.
//
// Anything else (sibling files in $HOME, paths outside both trees)
// returns false. Pre-v0.6.6 this function returned true for any
// unknown path — harmless because fsnotify's non-recursive $HOME
// watch doesn't fire for subdirs in practice, but a belt-and-braces
// check costs nothing.
func relevant(ev fsnotify.Event, in Inputs) bool {
	name := ev.Name

	// ClaudeJSON match (home-dir watcher).
	if in.SyncInputs.ClaudeJSON != "" {
		if name == in.SyncInputs.ClaudeJSON {
			return true
		}
		// Sibling in the same parent dir as ClaudeJSON — reject
		// regardless of whether it's also under ClaudeDir (the home
		// watcher and the claude-dir watcher don't overlap).
		if filepath.Dir(name) == filepath.Dir(in.SyncInputs.ClaudeJSON) {
			return false
		}
	}

	// Under ClaudeDir (recursive watcher). Honour ignore rules.
	if in.SyncInputs.ClaudeDir != "" {
		rel, err := filepath.Rel(in.SyncInputs.ClaudeDir, name)
		if err != nil || strings.HasPrefix(rel, "..") {
			return false
		}
		rel = filepath.ToSlash(rel)
		if in.Ignore != nil && in.Ignore.Matches(rel) {
			return false
		}
		return true
	}

	return false
}

func statInfo(path string) (fs.FileInfo, error) { return os.Stat(path) }
