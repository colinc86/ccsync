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
	Agents          = "agents"
	Skills          = "skills"
	Commands        = "commands"
	Memory          = "memory"
	MCPServers      = "mcp_servers"
	ClaudeMD        = "claude_md"
	GeneralSettings = "general_settings"
	Other           = "other"
)

// All returns the canonical list of categories, in the order the
// Settings matrix renders them. Kept stable for UI consistency.
func All() []string {
	return []string{Agents, Skills, Commands, Memory, MCPServers, ClaudeMD, GeneralSettings, Other}
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
	case Memory:
		return "Memory"
	case MCPServers:
		return "MCP Servers"
	case ClaudeMD:
		return "CLAUDE.md"
	case GeneralSettings:
		return "General Settings"
	case Other:
		return "Other"
	}
	return c
}

// Classify maps a repo-relative path (already stripped of the
// profiles/<name>/ prefix) to its category. Called with paths like
// "claude/agents/foo.md", "claude.json", or
// "claude/skills/x/SKILL.md". Paths outside the known trees fall into
// Other so the review system never misses a file even if Claude Code
// adds new directories later.
//
// Note: claude.json here is classified as GeneralSettings as a default,
// but sync actions touching mcpServers are reclassified to MCPServers
// by the sync-side code that has access to the actual diff. This
// function is for the common case where only the path is available.
func Classify(repoRelPath string) string {
	p := strings.TrimPrefix(repoRelPath, "claude/")
	if p == repoRelPath && repoRelPath != "claude.json" {
		return Other
	}
	switch {
	case repoRelPath == "claude.json":
		return GeneralSettings
	case p == "CLAUDE.md":
		return ClaudeMD
	case strings.HasPrefix(p, "agents/"):
		return Agents
	case strings.HasPrefix(p, "skills/"):
		return Skills
	case strings.HasPrefix(p, "commands/"):
		return Commands
	case strings.HasPrefix(p, "memory/"):
		return Memory
	case p == "settings.json":
		return GeneralSettings
	}
	return Other
}

// ClassifyWithMCP is Classify plus the mcpServers-aware override: when
// a claude.json action touches only the mcpServers subtree, it returns
// MCPServers instead of GeneralSettings. The boolean hasMCP must be
// true when the caller has confirmed the diff is entirely within
// $.mcpServers.
func ClassifyWithMCP(repoRelPath string, mcpOnly bool) string {
	if repoRelPath == "claude.json" && mcpOnly {
		return MCPServers
	}
	return Classify(repoRelPath)
}
