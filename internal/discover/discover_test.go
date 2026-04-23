package discover

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/colinc86/ccsync/internal/ignore"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWalkClassifies(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	claudeJSON := filepath.Join(tmp, ".claude.json")

	writeFile(t, filepath.Join(claudeDir, "settings.json"), `{}`)
	writeFile(t, filepath.Join(claudeDir, "agents/foo.md"), `agent`)
	writeFile(t, filepath.Join(claudeDir, "projects/p1/metrics.json"), `m`)
	writeFile(t, filepath.Join(claudeDir, "file-history/big.bin"), `b`)
	writeFile(t, filepath.Join(claudeDir, "plans/myplan.md"), `p`)
	writeFile(t, claudeJSON, `{}`)

	m := ignore.New("projects/\nfile-history/\nplans/\n")

	res, err := Walk(Inputs{ClaudeDir: claudeDir, ClaudeJSON: claudeJSON}, m)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, e := range res.Tracked {
		got[e.RelPath] = true
	}
	for _, want := range []string{"claude.json", "claude/settings.json", "claude/agents/foo.md"} {
		if !got[want] {
			t.Errorf("expected %q in tracked, have %v", want, keys(got))
		}
	}
	for _, notWanted := range []string{
		"claude/projects/p1/metrics.json",
		"claude/file-history/big.bin",
		"claude/plans/myplan.md",
	} {
		if got[notWanted] {
			t.Errorf("did not expect %q in tracked", notWanted)
		}
	}
}

func TestWalkEmptyInputs(t *testing.T) {
	res, err := Walk(Inputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tracked) != 0 || len(res.Ignored) != 0 {
		t.Fatalf("expected empty, got %+v", res)
	}
}

func TestWalkNoMatcherTreatsAllAsTracked(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".claude/a.md"), "a")
	res, err := Walk(Inputs{ClaudeDir: filepath.Join(tmp, ".claude")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tracked) != 1 {
		t.Fatalf("expected 1 tracked, got %d", len(res.Tracked))
	}
}

// TestWalkInTreeSymlinkIsTracked pins the common case: a symlink inside
// ~/.claude that points at another file inside ~/.claude (e.g. a user
// organising agents across subdirectories and aliasing for convenience)
// MUST be returned as tracked. The iter-40 symlink-escape guard only
// filters out symlinks whose targets resolve outside the tree; in-tree
// links are legitimate and must still sync.
func TestWalkInTreeSymlinkIsTracked(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(claudeDir, "agents/real.md")
	if err := os.WriteFile(target, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(claudeDir, "agents/alias.md")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	res, err := Walk(Inputs{ClaudeDir: claudeDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range res.Tracked {
		if e.RelPath == "claude/agents/alias.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("in-tree symlink should appear in Tracked; only out-of-tree links should be filtered")
	}
}

// TestWalkRejectsSymlinksEscapingTree pins the iteration-40 safety net:
// a symlink inside ~/.claude whose target resolves OUTSIDE the tree
// must NOT be returned as a tracked file. Pre-fix, discover happily
// listed the entry and sync.Run's os.ReadFile(AbsPath) followed the
// symlink to read whatever was on the other end — a configuration
// mistake (or malicious drop-in) could sync secrets from /etc, other
// users' home dirs, or anywhere else the process had read access to.
// Treating out-of-tree links as ignored keeps discovery hermetic.
func TestWalkRejectsSymlinksEscapingTree(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	outside := filepath.Join(tmp, "outside-secret")
	if err := os.MkdirAll(filepath.Join(claudeDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(outside, "leak.md")
	if err := os.WriteFile(secretPath, []byte("OUTSIDE-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(claudeDir, "agents/escapes.md")
	if err := os.Symlink(secretPath, linkPath); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	// An in-tree symlink (target also under ~/.claude) IS allowed —
	// this is the relative-path / organized-subtree use case.
	innerTarget := filepath.Join(claudeDir, "agents/real.md")
	if err := os.WriteFile(innerTarget, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	innerLink := filepath.Join(claudeDir, "agents/alias.md")
	if err := os.Symlink(innerTarget, innerLink); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	res, err := Walk(Inputs{ClaudeDir: claudeDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Tracked {
		if e.RelPath == "claude/agents/escapes.md" {
			t.Fatal("out-of-tree symlink was returned as tracked — sync would leak content from outside ~/.claude")
		}
	}
	// real.md and alias.md (in-tree link) should both appear.
	tracked := map[string]bool{}
	for _, e := range res.Tracked {
		tracked[e.RelPath] = true
	}
	if !tracked["claude/agents/real.md"] {
		t.Errorf("regular file was dropped alongside the symlink filter")
	}
	if !tracked["claude/agents/alias.md"] {
		t.Errorf("in-tree symlink was dropped; only out-of-tree links should be filtered")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
