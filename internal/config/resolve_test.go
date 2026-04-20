package config

import (
	"strings"
	"testing"
)

func TestEffectiveProfileSimple(t *testing.T) {
	cfg, err := Parse([]byte(`
profiles:
  default:
    description: personal
`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := EffectiveProfile(cfg, "default")
	if err != nil {
		t.Fatal(err)
	}
	if r.Chain[0] != "default" || len(r.Chain) != 1 {
		t.Fatalf("chain = %v", r.Chain)
	}
	if r.HasExcludes() {
		t.Fatal("expected no excludes for plain profile")
	}
}

func TestEffectiveProfileExtendsChain(t *testing.T) {
	cfg, err := Parse([]byte(`
profiles:
  default:
    description: personal
    exclude:
      paths:
        - "claude/agents/secret-*.md"
  work:
    description: work laptop
    extends: default
    hostClasses: [work]
    exclude:
      paths:
        - "claude/skills/shopping-*/**"
`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := EffectiveProfile(cfg, "work")
	if err != nil {
		t.Fatal(err)
	}
	if !(len(r.Chain) == 2 && r.Chain[0] == "work" && r.Chain[1] == "default") {
		t.Fatalf("chain = %v", r.Chain)
	}
	if len(r.PathExcludes) != 2 {
		t.Fatalf("expected 2 exclude patterns; got %d: %v", len(r.PathExcludes), r.PathExcludes)
	}
	rules := r.ExcludeRules()
	if !strings.Contains(rules, "claude/skills/shopping-*/**") {
		t.Fatalf("missing child exclude in %q", rules)
	}
	if !strings.Contains(rules, "claude/agents/secret-*.md") {
		t.Fatalf("missing parent exclude in %q", rules)
	}
	if len(r.HostClasses) != 1 || r.HostClasses[0] != "work" {
		t.Fatalf("host classes = %v", r.HostClasses)
	}
	if r.Description != "work laptop" {
		t.Fatalf("description = %q (should be leaf's)", r.Description)
	}
}

func TestEffectiveProfileCycle(t *testing.T) {
	cfg, err := Parse([]byte(`
profiles:
  a:
    extends: b
  b:
    extends: a
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EffectiveProfile(cfg, "a"); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestEffectiveProfileMissingParent(t *testing.T) {
	cfg, err := Parse([]byte(`
profiles:
  work:
    extends: nonexistent
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EffectiveProfile(cfg, "work"); err == nil {
		t.Fatal("expected missing-parent error")
	}
}

func TestEffectiveProfileUnknown(t *testing.T) {
	cfg, err := Parse([]byte(`
profiles:
  default: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EffectiveProfile(cfg, "missing"); err == nil {
		t.Fatal("expected unknown-profile error")
	}
}
