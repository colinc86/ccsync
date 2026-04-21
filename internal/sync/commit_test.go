package sync

import (
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/manifest"
)

func TestSemanticLabel(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"profiles/default/claude/agents/git-helpers.md", "agent: git-helpers"},
		{"profiles/default/claude/skills/research/SKILL.md", "skill: research"},
		{"profiles/default/claude/skills/research/helper.md", "skill: research"},
		{"profiles/work/claude/commands/status.md", "command: status"},
		{"profiles/default/CLAUDE.md", "CLAUDE.md"},
		{"profiles/default/claude/other/thing.md", "claude/other/thing.md"},
	}
	for _, c := range cases {
		got := SemanticLabel(c.in, nil, nil)
		if got != c.want {
			t.Errorf("SemanticLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestChangedTopLevelKeys(t *testing.T) {
	pre := []byte(`{"theme":"dark","autoUpdates":true,"mcpServers":{"a":1}}`)
	post := []byte(`{"theme":"light","autoUpdates":true,"mcpServers":{"a":2},"newField":"x"}`)
	got := changedTopLevelKeys(pre, post)
	want := []string{"mcpServers", "newField", "theme"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("keys = %v, want %v", got, want)
	}
}

func TestCommitMessageStructured(t *testing.T) {
	plan := Plan{
		Actions: []FileAction{
			{Path: "profiles/default/claude/agents/foo.md", Action: manifest.ActionAddRemote},
			{Path: "profiles/default/CLAUDE.md", Action: manifest.ActionPush},
			{Path: "profiles/default/claude/skills/legacy/SKILL.md", Action: manifest.ActionDeleteRemote},
		},
	}
	msg := commitMessage("default", "laptop", plan, nil, nil)
	for _, want := range []string{
		"sync(default): laptop",
		"Added:",
		"- agent: foo",
		"Changed:",
		"- CLAUDE.md",
		"Removed:",
		"- skill: legacy",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in message:\n%s", want, msg)
		}
	}
}
