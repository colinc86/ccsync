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

	"github.com/colinc86/ccsync/internal/ignore"
	syncpkg "github.com/colinc86/ccsync/internal/sync"
)

// Inputs describes what to watch and how to run each sync.
type Inputs struct {
	SyncInputs syncpkg.Inputs
	Debounce   time.Duration // minimum quiet time before a sync fires
	Out        io.Writer     // progress log sink (os.Stdout in CLI)
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

	var (
		mu      sync.Mutex
		timer   *time.Timer
		syncing bool
	)

	fire := func() {
		mu.Lock()
		if syncing {
			mu.Unlock()
			return
		}
		syncing = true
		mu.Unlock()

		defer func() {
			mu.Lock()
			syncing = false
			mu.Unlock()
		}()

		fmt.Fprintf(in.Out, "sync triggered at %s\n", time.Now().Local().Format("15:04:05"))
		res, err := syncpkg.RunWithRetry(ctx, in.SyncInputs, nil)
		if err != nil {
			fmt.Fprintf(in.Out, "  error: %v\n", err)
			return
		}
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
			fmt.Fprintf(in.Out, "  %d conflict(s) — open the TUI to resolve\n", len(res.Plan.Conflicts))
		}
		if len(res.MissingSecrets) > 0 {
			fmt.Fprintf(in.Out, "  %d file(s) skipped due to missing secrets\n", len(res.MissingSecrets))
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

// relevant filters out events that shouldn't trigger a sync (ignored paths,
// and for the $HOME watcher, anything that isn't claude.json).
func relevant(ev fsnotify.Event, in Inputs) bool {
	name := ev.Name
	if in.SyncInputs.ClaudeJSON != "" && filepath.Dir(name) == filepath.Dir(in.SyncInputs.ClaudeJSON) {
		// Home-dir watcher: only care about claude.json.
		if name != in.SyncInputs.ClaudeJSON {
			return false
		}
		return true
	}
	if in.Ignore != nil && in.SyncInputs.ClaudeDir != "" {
		if rel, err := filepath.Rel(in.SyncInputs.ClaudeDir, name); err == nil {
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "..") {
				return false
			}
			if in.Ignore.Matches(rel) {
				return false
			}
		}
	}
	return true
}

func statInfo(path string) (fs.FileInfo, error) { return os.Stat(path) }
