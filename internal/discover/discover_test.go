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

// TestWalk_TracksOnlyContentRoots pins the v0.9.0 narrowing: the walk
// returns files from the six explicit content directories plus the
// top-level CLAUDE.md, and silently drops everything else. ~/.claude/
// settings.json — which v0.8.x would have tracked — must NOT appear,
// since mcpextract owns the settings.json round-trip now.
func TestWalk_TracksOnlyContentRoots(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")

	// Should be tracked — content roots + top-level CLAUDE.md.
	writeFile(t, filepath.Join(claudeDir, "agents/foo.md"), `agent`)
	writeFile(t, filepath.Join(claudeDir, "skills/x/SKILL.md"), `skill`)
	writeFile(t, filepath.Join(claudeDir, "commands/deploy.md"), `cmd`)
	writeFile(t, filepath.Join(claudeDir, "hooks/pre.sh"), `#!/bin/sh`)
	writeFile(t, filepath.Join(claudeDir, "output-styles/concise.md"), `style`)
	writeFile(t, filepath.Join(claudeDir, "memory/notes.md"), `note`)
	writeFile(t, filepath.Join(claudeDir, "CLAUDE.md"), `# global instructions`)

	// Should NOT be tracked — settings/cache/runtime live outside
	// the content surface.
	writeFile(t, filepath.Join(claudeDir, "settings.json"), `{}`)
	writeFile(t, filepath.Join(claudeDir, "projects/p1/metrics.json"), `m`)
	writeFile(t, filepath.Join(claudeDir, "file-history/big.bin"), `b`)
	writeFile(t, filepath.Join(claudeDir, "plans/myplan.md"), `p`)
	writeFile(t, filepath.Join(claudeDir, "telemetry/events.jsonl"), `t`)
	writeFile(t, filepath.Join(claudeDir, "sessions/abc.json"), `s`)

	res, err := Walk(Inputs{ClaudeDir: claudeDir}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, e := range res.Tracked {
		got[e.RelPath] = true
	}
	for _, want := range []string{
		"claude/CLAUDE.md",
		"claude/agents/foo.md",
		"claude/skills/x/SKILL.md",
		"claude/commands/deploy.md",
		"claude/hooks/pre.sh",
		"claude/output-styles/concise.md",
		"claude/memory/notes.md",
	} {
		if !got[want] {
			t.Errorf("expected %q in tracked, have %v", want, keys(got))
		}
	}
	for _, notWanted := range []string{
		"claude/settings.json",
		"claude/projects/p1/metrics.json",
		"claude/file-history/big.bin",
		"claude/plans/myplan.md",
		"claude/telemetry/events.jsonl",
		"claude/sessions/abc.json",
	} {
		if got[notWanted] {
			t.Errorf("did not expect %q in tracked", notWanted)
		}
	}
}

// TestWalk_IgnoreRulesApplyInsideContentRoots pins that the
// .syncignore matcher still trims scratch *inside* the tracked
// directories — even though the surrounding tree has already been
// narrowed.
func TestWalk_IgnoreRulesApplyInsideContentRoots(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	writeFile(t, filepath.Join(claudeDir, "agents/foo.md"), `keep`)
	writeFile(t, filepath.Join(claudeDir, "agents/foo.md.bak"), `drop`)
	writeFile(t, filepath.Join(claudeDir, "skills/.DS_Store"), `drop`)

	m := ignore.New("*.bak\n.DS_Store\n")
	res, err := Walk(Inputs{ClaudeDir: claudeDir}, m)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range res.Tracked {
		got[e.RelPath] = true
	}
	if !got["claude/agents/foo.md"] {
		t.Errorf("expected agents/foo.md tracked: %v", keys(got))
	}
	if got["claude/agents/foo.md.bak"] {
		t.Errorf("*.bak rule didn't filter: %v", keys(got))
	}
	if got["claude/skills/.DS_Store"] {
		t.Errorf(".DS_Store rule didn't filter: %v", keys(got))
	}
}

// TestWalk_EmptyInputs pins zero-state. No ClaudeDir → empty result.
func TestWalk_EmptyInputs(t *testing.T) {
	res, err := Walk(Inputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tracked) != 0 || len(res.Ignored) != 0 {
		t.Fatalf("expected empty, got %+v", res)
	}
}

// TestWalk_InTreeSymlinkIsTracked pins the common case: a symlink
// inside ~/.claude that points at another file inside ~/.claude must
// be returned as tracked. The symlink-escape guard only filters out
// links whose targets resolve outside ~/.claude.
func TestWalk_InTreeSymlinkIsTracked(t *testing.T) {
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

// TestWalk_RejectsSymlinksEscapingTree pins the safety net: a symlink
// inside ~/.claude whose target resolves outside the tree must NOT be
// returned as tracked. Pre-fix, sync.Run's os.ReadFile(AbsPath)
// followed the symlink to read whatever was on the other end — a
// configuration mistake (or malicious drop-in) could sync secrets
// from /etc, other users' home dirs, anywhere with read access.
func TestWalk_RejectsSymlinksEscapingTree(t *testing.T) {
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
