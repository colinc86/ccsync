package harness

import (
	"reflect"
	"strings"
)

// AssertClaudeJSONKey asserts that the key at `path` (dotted, e.g.
// "oauthAccount.userId" or just "theme") exists and equals `expected`.
// Uses reflect.DeepEqual so maps/slices are compared structurally.
func (m *Machine) AssertClaudeJSONKey(path string, expected any) {
	m.scenario.t.Helper()
	got, ok := lookupKey(m.ClaudeJSONMap(), path)
	if !ok {
		m.scenario.t.Fatalf("[%s] claude.json %q absent; expected %v", m.Name, path, expected)
	}
	if !reflect.DeepEqual(got, expected) {
		m.scenario.t.Fatalf("[%s] claude.json %q = %v, want %v", m.Name, path, got, expected)
	}
}

// AssertClaudeJSONKeyAbsent asserts that no key exists at `path`. Used
// to prove a field is machine-local (never showed up on pull) or was
// filtered out before push.
func (m *Machine) AssertClaudeJSONKeyAbsent(path string) {
	m.scenario.t.Helper()
	if _, ok := lookupKey(m.ClaudeJSONMap(), path); ok {
		m.scenario.t.Fatalf("[%s] claude.json %q present but should be absent", m.Name, path)
	}
}

// AssertClaudeFile asserts that a file under ~/.claude exists with the
// exact expected contents.
func (m *Machine) AssertClaudeFile(relPath, expected string) {
	m.scenario.t.Helper()
	got, ok := m.ReadClaudeFile(relPath)
	if !ok {
		m.scenario.t.Fatalf("[%s] %s absent; expected %q", m.Name, relPath, expected)
	}
	if got != expected {
		m.scenario.t.Fatalf("[%s] %s = %q, want %q", m.Name, relPath, got, expected)
	}
}

// AssertNoClaudeFile asserts that a file under ~/.claude does not exist.
func (m *Machine) AssertNoClaudeFile(relPath string) {
	m.scenario.t.Helper()
	if _, ok := m.ReadClaudeFile(relPath); ok {
		m.scenario.t.Fatalf("[%s] %s exists but should not", m.Name, relPath)
	}
}

// AssertSyncClean asserts that a fresh DryRun produces no push / pull /
// conflict actions. The canonical "in sync" check.
func (m *Machine) AssertSyncClean() {
	m.scenario.t.Helper()
	plan := m.DryRun()
	if len(plan.Conflicts) > 0 {
		m.scenario.t.Fatalf("[%s] expected clean sync; got %d conflicts", m.Name, len(plan.Conflicts))
	}
	for _, a := range plan.Actions {
		if a.ExcludedByProfile {
			continue
		}
		switch a.Action.String() {
		case "NoOp":
			continue
		default:
			m.scenario.t.Fatalf("[%s] expected clean sync; got action %s on %s", m.Name, a.Action, a.Path)
		}
	}
}

// AssertBareHasPath asserts a path exists in the bare repo's HEAD tree.
// Useful for confirming a push landed at a specific profile-prefixed
// path (e.g. "profiles/default/claude/agents/foo.md").
func (s *Scenario) AssertBareHasPath(path string) {
	s.t.Helper()
	if _, ok := s.BareFile(path); !ok {
		s.t.Fatalf("bare repo missing path %q", path)
	}
}

// AssertBareNoPath asserts a path is absent in the bare repo's HEAD.
func (s *Scenario) AssertBareNoPath(path string) {
	s.t.Helper()
	if _, ok := s.BareFile(path); ok {
		s.t.Fatalf("bare repo has path %q but should not", path)
	}
}

// lookupKey walks a dotted path through a parsed-JSON map and returns
// (value, true) if the path resolves, (nil, false) otherwise. Supports
// object indexing only — no array subscripts, which is fine for
// claude.json's schema.
func lookupKey(doc map[string]any, path string) (any, bool) {
	if doc == nil {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur any = doc
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, present := m[p]
		if !present {
			return nil, false
		}
		cur = v
	}
	return cur, true
}
