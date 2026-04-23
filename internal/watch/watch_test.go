package watch

import (
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"

	"github.com/colinc86/ccsync/internal/ignore"
	syncpkg "github.com/colinc86/ccsync/internal/sync"
)

// TestRelevantHomeDirFilter exercises the $HOME watch filter: only
// claude.json events should make it through; sibling files like
// .bashrc or Downloads/foo.dmg must not trigger a sync.
func TestRelevantHomeDirFilter(t *testing.T) {
	home := "/home/user"
	in := Inputs{
		SyncInputs: syncpkg.Inputs{
			ClaudeDir:  filepath.Join(home, ".claude"),
			ClaudeJSON: filepath.Join(home, ".claude.json"),
		},
	}
	cases := []struct {
		name string
		ev   fsnotify.Event
		want bool
	}{
		{"claude.json itself", fsnotify.Event{Name: filepath.Join(home, ".claude.json"), Op: fsnotify.Write}, true},
		{"sibling bashrc", fsnotify.Event{Name: filepath.Join(home, ".bashrc"), Op: fsnotify.Write}, false},
		{"sibling download", fsnotify.Event{Name: filepath.Join(home, "Downloads/foo.dmg"), Op: fsnotify.Write}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := relevant(c.ev, in); got != c.want {
				t.Errorf("relevant = %v, want %v", got, c.want)
			}
		})
	}
}

// TestRelevantIgnoreMatcher proves ignored paths under the Claude
// tree don't trigger a sync. This is what keeps noisy subdirs
// (sessions/, cache/) from kicking off a run every second.
func TestRelevantIgnoreMatcher(t *testing.T) {
	home := "/home/user"
	claudeDir := filepath.Join(home, ".claude")
	// Use a different claude.json path so the home-dir filter doesn't
	// short-circuit before the ignore check runs.
	in := Inputs{
		SyncInputs: syncpkg.Inputs{
			ClaudeDir:  claudeDir,
			ClaudeJSON: filepath.Join("/other/place", ".claude.json"),
		},
		Ignore: ignore.New("sessions/\ncache/\n"),
	}

	cases := []struct {
		name string
		ev   fsnotify.Event
		want bool
	}{
		{"agent markdown", fsnotify.Event{Name: filepath.Join(claudeDir, "agents/foo.md"), Op: fsnotify.Write}, true},
		{"ignored sessions", fsnotify.Event{Name: filepath.Join(claudeDir, "sessions/abc.jsonl"), Op: fsnotify.Write}, false},
		{"ignored cache", fsnotify.Event{Name: filepath.Join(claudeDir, "cache/x"), Op: fsnotify.Write}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := relevant(c.ev, in); got != c.want {
				t.Errorf("relevant = %v, want %v", got, c.want)
			}
		})
	}
}

// TestRelevantOutsideTreeRejected — an event for a path outside the
// watched Claude tree (e.g. a symlink that resolved somewhere
// unexpected) must not trigger a sync.
func TestRelevantOutsideTreeRejected(t *testing.T) {
	in := Inputs{
		SyncInputs: syncpkg.Inputs{
			ClaudeDir:  "/home/user/.claude",
			ClaudeJSON: "/other/place/.claude.json",
		},
		Ignore: ignore.New(""),
	}
	// Path clearly outside the tree — filepath.Rel returns "../..."
	ev := fsnotify.Event{Name: "/etc/passwd", Op: fsnotify.Write}
	if relevant(ev, in) {
		t.Error("event outside Claude tree should not be relevant")
	}
}
