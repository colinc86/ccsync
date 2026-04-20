package why

import (
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/config"
)

func loadCfg(t *testing.T, yaml string) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return c
}

func TestExplainSyncignoreHit(t *testing.T) {
	cfg := loadCfg(t, `
profiles:
  default: {}
defaultSyncignore: |
  projects/
`)
	tr, err := Explain(Inputs{Config: cfg, Profile: "default"}, "claude/projects/foo.json")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Outcome != OutcomeSyncignored {
		t.Fatalf("outcome = %v; want Syncignored", tr.Outcome)
	}
}

func TestExplainProfileExcluded(t *testing.T) {
	cfg := loadCfg(t, `
profiles:
  default: {}
  work:
    extends: default
    exclude:
      paths:
        - "claude/agents/personal-*.md"
`)
	tr, err := Explain(Inputs{Config: cfg, Profile: "work"}, "claude/agents/personal-notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Outcome != OutcomeProfileExcluded {
		t.Fatalf("outcome = %v; want ProfileExcluded", tr.Outcome)
	}
	found := false
	for _, s := range tr.Steps {
		if strings.Contains(s.Rule, "profile[work].exclude") && s.Matched {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no matched profile.exclude step in %+v", tr.Steps)
	}
}

func TestExplainJSONRedact(t *testing.T) {
	cfg := loadCfg(t, `
profiles:
  default: {}
jsonFiles:
  "~/.claude.json":
    include: ["$"]
    redact:
      - $..apiKey
`)
	tr, err := Explain(Inputs{Config: cfg, Profile: "default"},
		"~/.claude.json:$.mcpServers.gemini.apiKey")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Outcome != OutcomeJSONRedacted {
		t.Fatalf("outcome = %v; want JSONRedacted. Steps=%+v", tr.Outcome, tr.Steps)
	}
}

func TestExplainJSONExcluded(t *testing.T) {
	cfg := loadCfg(t, `
profiles:
  default: {}
jsonFiles:
  "~/.claude.json":
    include: ["$"]
    exclude:
      - $.oauthAccount
`)
	tr, err := Explain(Inputs{Config: cfg, Profile: "default"},
		"~/.claude.json:$.oauthAccount")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Outcome != OutcomeJSONExcluded {
		t.Fatalf("outcome = %v; want JSONExcluded", tr.Outcome)
	}
}

func TestExplainSyncedWhenNoRuleMatches(t *testing.T) {
	cfg := loadCfg(t, `
profiles:
  default: {}
defaultSyncignore: |
  projects/
`)
	tr, err := Explain(Inputs{Config: cfg, Profile: "default"}, "claude/agents/foo.md")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Outcome != OutcomeSynced {
		t.Fatalf("outcome = %v; want Synced", tr.Outcome)
	}
}

func TestFormatContainsBasics(t *testing.T) {
	cfg := loadCfg(t, `
profiles:
  default: {}
`)
	tr, _ := Explain(Inputs{Config: cfg, Profile: "default"}, "claude/agents/foo.md")
	out := Format(tr)
	if !strings.Contains(out, "claude/agents/foo.md") || !strings.Contains(out, "synced") {
		t.Fatalf("unexpected format output:\n%s", out)
	}
}
