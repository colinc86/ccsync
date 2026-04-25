package category

import "testing"

// TestClassify pins the post-v0.9.0 path mapping. Every path that the
// discover walk or the mcpextract step writes to the repo must resolve
// to a known category. Anything outside that surface returns "" — the
// signal callers use to skip category routing entirely.
func TestClassify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"claude/agents/foo.md", Agents},
		{"claude/agents/subdir/bar.md", Agents},
		{"claude/skills/summary/SKILL.md", Skills},
		{"claude/commands/deploy.md", Commands},
		{"claude/hooks/pre-tool.sh", Hooks},
		{"claude/output-styles/concise.md", OutputStyles},
		{"claude/memory/notes.md", Memory},
		{"claude/CLAUDE.md", ClaudeMD},
		{ManagedMCPClaudeJSONPath, MCPServers},
		{ManagedMCPSettingsJSONPath, MCPServers},
		{ManagedHooksPath, Hooks},
		// Out-of-surface paths return "" — callers skip them.
		{"claude/settings.json", ""},
		{"claude.json", ""},
		{"claude/misc/something.sh", ""},
		{"something-outside-claude/foo.md", ""},
	}
	for _, tc := range cases {
		if got := Classify(tc.in); got != tc.want {
			t.Errorf("Classify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMCPServerDiff pins the per-server diff produced from raw managed-
// file bytes (the new format: `{"name": {...}}` rather than the old
// `{"mcpServers": {"name": {...}}}` envelope).
func TestMCPServerDiff(t *testing.T) {
	base := []byte(`{"a":{"command":"xa"},"b":{"command":"xb"}}`)
	local := []byte(`{"a":{"command":"xa"},"b":{"command":"BB"},"c":{"command":"xc"}}`)
	remote := []byte(`{"a":{"command":"xa"},"b":{"command":"xb"}}`)

	items := MCPServerDiff(base, local, remote)
	byName := map[string]MCPItem{}
	for _, it := range items {
		byName[it.Name] = it
	}

	// `a` unchanged → not in diff.
	if _, ok := byName["a"]; ok {
		t.Error("unchanged server appeared in diff")
	}
	// `b` modified locally → in diff, marked as modify.
	b, ok := byName["b"]
	if !ok {
		t.Fatal("modified server b missing from diff")
	}
	if !b.IsModify() {
		t.Errorf("b should be IsModify(); got base=%q local=%q remote=%q", b.Base, b.Local, b.Remote)
	}
	// `c` added locally → in diff, marked as add.
	c, ok := byName["c"]
	if !ok {
		t.Fatal("added server c missing from diff")
	}
	if !c.IsAdd() {
		t.Error("c should be IsAdd()")
	}
}

// TestMCPServerDiffHandlesAbsent pins the empty-side behavior: nil/empty
// bytes mean "no managed file on that side" and are equivalent to {}.
func TestMCPServerDiffHandlesAbsent(t *testing.T) {
	items := MCPServerDiff(nil, []byte(`{"x":{"command":"y"}}`), []byte(`{}`))
	if len(items) != 1 || items[0].Name != "x" || !items[0].IsAdd() {
		t.Errorf("expected single add-item for x; got %+v", items)
	}
}
