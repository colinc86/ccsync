package category

import "testing"

// TestMCPOnlyDiffRejectsMalformedInput pins iter-43 audit fix: a
// malformed side must cause MCPOnlyDiff to return false. Pre-fix,
// stripMCP silently coerced parse failures to {} — so a garbage
// local vs an mcp-only remote both stripped to {} and MCPOnlyDiff
// claimed "yep, mcp-only change." The sync engine would then route
// the "change" through the user's MCPServers push policy rather
// than surfacing the JSON corruption. In the normal flow this is
// unreachable (jsonfilter.Apply parses first), but the public API
// shouldn't misrepresent garbage as a legitimate mcp-scoped diff.
func TestMCPOnlyDiffRejectsMalformedInput(t *testing.T) {
	remote := []byte(`{"mcpServers":{"gemini":{}}}`)
	if got := MCPOnlyDiff([]byte(`{"broken`), remote); got {
		t.Error("malformed local vs mcp-only remote should NOT be classified as MCP-only")
	}
	if got := MCPOnlyDiff(remote, []byte(`{"broken`)); got {
		t.Error("mcp-only local vs malformed remote should NOT be classified as MCP-only")
	}
	// Sanity: the legitimate cases still work.
	if got := MCPOnlyDiff(nil, remote); !got {
		t.Error("nil local vs mcp-only remote should be MCP-only")
	}
}
