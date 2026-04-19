package doctor

import (
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
)

func TestCheckAllOK(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeJSON := filepath.Join(tmp, ".claude.json")
	if err := os.WriteFile(claudeJSON, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	repoPath := filepath.Join(tmp, "repo")
	if _, err := gogit.PlainInit(repoPath, false); err != nil {
		t.Fatal(err)
	}

	r := Check(Inputs{
		ClaudeDir: claudeDir, ClaudeJSON: claudeJSON,
		RepoPath: repoPath, StateDir: filepath.Join(tmp, ".ccsync"),
	})
	if r.Worst() != SeverityOK {
		t.Errorf("expected all OK, got worst=%s findings=%v", r.Worst(), r.Findings)
	}
}

func TestCheckFlagsDanglingPlaceholders(t *testing.T) {
	tmp := t.TempDir()
	claudeJSON := filepath.Join(tmp, ".claude.json")
	if err := os.WriteFile(claudeJSON, []byte(`{"key":"<<REDACTED:ccsync:default:x.y>>"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Check(Inputs{ClaudeJSON: claudeJSON})
	if r.Worst() != SeverityFail {
		t.Errorf("expected FAIL, got %s: %v", r.Worst(), r.Findings)
	}
}
