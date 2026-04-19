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

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
