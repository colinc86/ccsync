// Package category classifies ccsync sync actions by the user-facing
// Claude Code component they map to. The review system uses these
// categories to decide whether a given push or pull needs manual
// approval: "I always want skills to sync, but I want to review which
// commands my machine pushes up."
//
// Category names are stable, lowercase, snake_case identifiers — they
// appear both in state.json (as the CategoryPolicies keys) and in the
// review UI labels.
package category

import (
	"strings"
)

// Canonical category identifiers. Any new category added here must also
// get a corresponding slot in state.CategoryPolicies.
const (
	Agents       = "agents"
	Skills       = "skills"
	Commands     = "commands"
	Hooks        = "hooks"
	OutputStyles = "output_styles"
	Memory       = "memory"
	ClaudeMD     = "claude_md"
	MCPServers   = "mcp_servers"
)

// All returns the canonical list of categories, in the order the
// Settings matrix and review screen render them. Kept stable for UI
// consistency.
func All() []string {
	return []string{
		Agents,
		Skills,
		Commands,
		Hooks,
		OutputStyles,
		Memory,
		ClaudeMD,
		MCPServers,
	}
}

// Label maps a category identifier to the human-facing name used in
// the review screen and Settings matrix. Unknown categories fall back
// to the identifier itself.
func Label(c string) string {
	switch c {
	case Agents:
		return "Agents"
	case Skills:
		return "Skills"
	case Commands:
		return "Commands"
	case Hooks:
		return "Hooks"
	case OutputStyles:
		return "Output Styles"
	case Memory:
		return "Memory"
	case ClaudeMD:
		return "CLAUDE.md"
	case MCPServers:
		return "MCP Servers"
	}
	return c
}

// Managed-file paths produced by the mcpextract step. These live at
// the top of profiles/<name>/ rather than under claude/, so they're
// easy to spot when eyeballing the repo.
const (
	ManagedMCPClaudeJSONPath   = ".ccsync.mcp.json"
	ManagedMCPSettingsJSONPath = "ccsync.mcp.json"
	ManagedHooksPath           = "ccsync.hooks.json"
)

// Classify maps a repo-relative path (already stripped of the
// profiles/<name>/ prefix) to its category. Called with paths like
// "claude/agents/foo.md", "claude/CLAUDE.md", or "ccsync.mcp.json".
//
// Every path that ccsync writes into the repo resolves to exactly one
// category — the discover walk and the mcpextract step are the only
// producers, both narrow. An empty return means "this is not a path
// ccsync sync owns" (e.g. manifest.json, .syncignore, ccsync.yaml,
// the repo README); callers treat that as "ignore for category
// routing."
func Classify(repoRelPath string) string {
	switch repoRelPath {
	case ManagedMCPClaudeJSONPath, ManagedMCPSettingsJSONPath:
		return MCPServers
	case ManagedHooksPath:
		return Hooks
	}
	p := strings.TrimPrefix(repoRelPath, "claude/")
	if p == repoRelPath {
		return ""
	}
	switch {
	case p == "CLAUDE.md":
		return ClaudeMD
	case strings.HasPrefix(p, "agents/"):
		return Agents
	case strings.HasPrefix(p, "skills/"):
		return Skills
	case strings.HasPrefix(p, "commands/"):
		return Commands
	case strings.HasPrefix(p, "hooks/"):
		return Hooks
	case strings.HasPrefix(p, "output-styles/"):
		return OutputStyles
	case strings.HasPrefix(p, "memory/"):
		return Memory
	}
	return ""
}
