package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	syncpkg "github.com/colinc86/ccsync/internal/sync"
)

// Helpers shared by any screen that mutates the active profile's
// exclude rules or .syncignore file. Lifted out of the old browse-
// tracked screen so "What's syncing" can reuse them without a
// second implementation.

// ignoreStage drives the modal .syncignore picker: off → choose
// (this path / parent dir / pattern) → pattern (textinput for a
// hand-edited rule). State is owned by whichever screen is hosting
// the flow, not by this file.
type ignoreStage int

const (
	ignoreOff     ignoreStage = iota
	ignoreChoose              // picking path / parent / pattern
	ignorePattern             // editing the pattern via textinput
)

// ignoreOption describes one row in the ignore-choose picker.
// Disabled options render greyed-out and skip cursor moves. `run`
// is an opaque command factory captured by the hosting screen —
// the helper doesn't know or care which model type owns the flow.
type ignoreOption struct {
	label   string // short label shown to the left of the arrow
	preview string // the pattern that would be written
	hint    string // optional extra hint text (e.g. "disabled…")
	run     func() tea.Cmd
	enabled bool
}

// nextEnabled / prevEnabled advance the ignore-picker cursor while
// skipping disabled rows, so the selector never rests on an option
// the user can't pick.
func nextEnabled(opts []ignoreOption, cur int) int {
	for i := cur + 1; i < len(opts); i++ {
		if opts[i].enabled {
			return i
		}
	}
	return cur
}

func prevEnabled(opts []ignoreOption, cur int) int {
	for i := cur - 1; i >= 0; i-- {
		if opts[i].enabled {
			return i
		}
	}
	return cur
}

// patternForPath chooses the ccsync.yaml exclude pattern to use
// for a repo-relative path. Skill dirs get the whole subtree via
// `**` because "a skill" is folder-shaped; everything else gets
// the exact path.
func patternForPath(rel string) string {
	if strings.HasPrefix(rel, "claude/skills/") {
		parts := strings.Split(rel, "/")
		if len(parts) >= 3 { // claude / skills / <skill>/...
			return strings.Join(parts[:3], "/") + "/**"
		}
	}
	return rel
}

// syncignoreRel returns the path the way .syncignore expects —
// patterns are written relative to ~/.claude, not the repo tree, so
// we strip the leading "claude/" prefix for files under the Claude
// directory. "claude.json" passes through unchanged.
func syncignoreRel(rel string) string {
	if after, ok := strings.CutPrefix(rel, "claude/"); ok {
		return after
	}
	return rel
}

// parentSyncignorePattern returns the directory pattern for the
// row's parent, or "" if it has none (top-level file like
// claude.json).
func parentSyncignorePattern(rel string) string {
	rel = syncignoreRel(rel)
	dir, _ := filepath.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		return ""
	}
	return dir + "/"
}

// defaultExtensionPattern returns a sensible starting pattern for
// the "pattern…" branch — typically "*.ext" when the file has an
// extension, otherwise the file's base name.
func defaultExtensionPattern(rel string) string {
	base := filepath.Base(rel)
	ext := filepath.Ext(base)
	if ext != "" {
		return "*" + ext
	}
	return base
}

// appendSyncignore writes a rule to .syncignore. Creates the file
// if missing and skips the write entirely if the exact pattern is
// already present. Writes via tmp+rename to match every other
// atomic-write path in the codebase — a mid-write crash would
// otherwise leave .syncignore truncated and silently drop patterns
// the user thinks are applied.
func appendSyncignore(path, pattern string) error {
	var existing []byte
	if data, err := os.ReadFile(path); err == nil {
		existing = data
	} else if !os.IsNotExist(err) {
		return err
	}
	// Dedup: bail if the pattern is already on its own line.
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		existing = append(existing, '\n')
	}
	existing = append(existing, []byte(pattern+"\n")...)

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, existing, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// promoteDoneMsg is emitted when a PromotePath invocation finishes.
// Keyed with a distinct name from syncpkg's promotesDoneMsg so the
// active sync screen and the hosting inspector don't fight over the
// same message type.
type promoteDoneMsg struct{ err error }

// runPromote fires syncpkg.PromotePath from any hosting screen,
// moving relPath from the active profile to "default". Destination
// is hardcoded because "default" is the canonical share target;
// other fan-outs aren't in the v1 scope.
func runPromote(ctx *AppContext, relPath string) tea.Cmd {
	return func() tea.Msg {
		in, err := buildSyncInputs(ctx, false)
		if err != nil {
			return promoteDoneMsg{err: err}
		}
		active := ctx.State.ActiveProfile
		if active == "" {
			active = "default"
		}
		if err := syncpkg.PromotePath(context.Background(), in, relPath, active, "default"); err != nil {
			return promoteDoneMsg{err: err}
		}
		return promoteDoneMsg{}
	}
}
