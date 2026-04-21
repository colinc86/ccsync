package category

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"claude/agents/foo.md", Agents},
		{"claude/agents/subdir/bar.md", Agents},
		{"claude/skills/summary/SKILL.md", Skills},
		{"claude/commands/deploy.md", Commands},
		{"claude/memory/notes.md", Memory},
		{"claude/CLAUDE.md", ClaudeMD},
		{"claude/settings.json", GeneralSettings},
		{"claude.json", GeneralSettings},
		{"claude/misc/something.sh", Other},
		{"something-outside-claude/foo.md", Other},
	}
	for _, tc := range cases {
		if got := Classify(tc.in); got != tc.want {
			t.Errorf("Classify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestClassifyWithMCP(t *testing.T) {
	// claude.json with mcpOnly=true → MCPServers; false → GeneralSettings.
	if got := ClassifyWithMCP("claude.json", true); got != MCPServers {
		t.Errorf("mcpOnly=true should be mcp_servers, got %q", got)
	}
	if got := ClassifyWithMCP("claude.json", false); got != GeneralSettings {
		t.Errorf("mcpOnly=false should be general_settings, got %q", got)
	}
	// Non-claude.json paths ignore the mcpOnly flag.
	if got := ClassifyWithMCP("claude/agents/foo.md", true); got != Agents {
		t.Errorf("mcpOnly should not affect agents path; got %q", got)
	}
}

func TestMCPServerDiff(t *testing.T) {
	base := []byte(`{"mcpServers":{"a":{"command":"xa"},"b":{"command":"xb"}}}`)
	local := []byte(`{"mcpServers":{"a":{"command":"xa"},"b":{"command":"BB"},"c":{"command":"xc"}}}`)
	remote := []byte(`{"mcpServers":{"a":{"command":"xa"},"b":{"command":"xb"}}}`)

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

func TestMCPServerDiffHandlesAbsent(t *testing.T) {
	// Base absent, one side has mcpServers, the other empty.
	items := MCPServerDiff(nil, []byte(`{"mcpServers":{"x":{"command":"y"}}}`), []byte(`{}`))
	if len(items) != 1 || items[0].Name != "x" || !items[0].IsAdd() {
		t.Errorf("expected single add-item for x; got %+v", items)
	}
}
