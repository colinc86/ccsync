package sync

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/colinc86/ccsync/internal/manifest"
)

// commitMessage renders the structured human-readable commit for a sync.
// Header keeps the `sync(<profile>): <host> +N ~M -K` shape so anyone
// browsing git log on the web still gets the summary at a glance; the body
// adds Added/Changed/Removed sections with semantic extraction for the
// file types ccsync knows about (agents, skills, commands, CLAUDE.md,
// claude.json).
//
// pre and post map repo-relative path → bytes at that slot before/after
// this sync. Pass empty maps for a dry-run test.
func commitMessage(profile, host string, plan Plan, pre, post map[string][]byte) string {
	added, modified, deleted := plan.Summary()
	var buf strings.Builder
	fmt.Fprintf(&buf, "sync(%s): %s +%d ~%d -%d\n", profile, host, added, modified, deleted)

	var addedEntries, changedEntries, removedEntries []string

	paths := make([]string, 0, len(plan.Actions))
	byPath := map[string]FileAction{}
	for _, a := range plan.Actions {
		if a.ExcludedByProfile {
			continue
		}
		if a.Action == manifest.ActionNoOp {
			continue
		}
		paths = append(paths, a.Path)
		byPath[a.Path] = a
	}
	sort.Strings(paths)

	for _, p := range paths {
		a := byPath[p]
		label := semanticLabel(p, pre[p], post[p])
		switch a.Action {
		case manifest.ActionAddRemote:
			addedEntries = append(addedEntries, label)
		case manifest.ActionPush, manifest.ActionMerge:
			changedEntries = append(changedEntries, label)
		case manifest.ActionDeleteRemote:
			removedEntries = append(removedEntries, label)
		}
	}

	writeSection := func(name string, entries []string) {
		if len(entries) == 0 {
			return
		}
		buf.WriteString("\n" + name + ":\n")
		// Cap at 20 per section so a large sync doesn't produce a huge
		// message body; git log surfaces the first 20 most relevant items.
		limit := 20
		if len(entries) > limit {
			entries = append(entries[:limit:limit], fmt.Sprintf("… %d more", len(entries)-limit))
		}
		for _, e := range entries {
			buf.WriteString("- " + e + "\n")
		}
	}

	writeSection("Added", addedEntries)
	writeSection("Changed", changedEntries)
	writeSection("Removed", removedEntries)
	return strings.TrimRight(buf.String(), "\n")
}

// semanticLabel turns a repo-relative path into a readable label. Falls
// back to the bare path when it doesn't match a recognized shape.
//
// Examples:
//   profiles/default/claude/agents/git-helpers.md → "agent: git-helpers"
//   profiles/default/claude/skills/research/SKILL.md → "skill: research"
//   profiles/default/claude/commands/status.md → "command: status"
//   profiles/default/CLAUDE.md → "CLAUDE.md"
//   profiles/default/claude.json → "claude.json: mcpServers.gemini, theme"
func semanticLabel(repoPath string, pre, post []byte) string {
	rel := stripProfilePrefix(repoPath)
	switch {
	case rel == "claude.json":
		keys := changedTopLevelKeys(pre, post)
		if len(keys) == 0 {
			return "claude.json"
		}
		if len(keys) > 5 {
			keys = append(keys[:5:5], fmt.Sprintf("+%d more", len(keys)-5))
		}
		return "claude.json: " + strings.Join(keys, ", ")
	case rel == "CLAUDE.md":
		return "CLAUDE.md"
	case strings.HasPrefix(rel, "claude/agents/") && strings.HasSuffix(rel, ".md"):
		name := strings.TrimSuffix(filepath.Base(rel), ".md")
		return "agent: " + name
	case strings.HasPrefix(rel, "claude/commands/") && strings.HasSuffix(rel, ".md"):
		name := strings.TrimSuffix(filepath.Base(rel), ".md")
		return "command: " + name
	case strings.HasPrefix(rel, "claude/skills/"):
		// skills live under claude/skills/<name>/...; extract <name>.
		parts := strings.SplitN(strings.TrimPrefix(rel, "claude/skills/"), "/", 2)
		if len(parts) >= 1 && parts[0] != "" {
			return "skill: " + parts[0]
		}
	}
	return rel
}

// stripProfilePrefix drops the leading "profiles/<name>/" so labels read
// uniformly regardless of which profile is active.
func stripProfilePrefix(p string) string {
	if !strings.HasPrefix(p, "profiles/") {
		return p
	}
	rest := strings.TrimPrefix(p, "profiles/")
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[i+1:]
	}
	return rest
}

// changedTopLevelKeys returns the top-level JSON keys whose values differ
// between pre and post. Missing or invalid JSON yields an empty list —
// callers fall back to a bare filename in that case.
func changedTopLevelKeys(pre, post []byte) []string {
	var pm, om map[string]json.RawMessage
	if len(pre) > 0 {
		_ = json.Unmarshal(pre, &pm)
	}
	if len(post) > 0 {
		_ = json.Unmarshal(post, &om)
	}
	keys := map[string]bool{}
	for k, v := range om {
		if !bytesEqual(pm[k], v) {
			keys[k] = true
		}
	}
	for k, v := range pm {
		if _, ok := om[k]; !ok {
			keys[k] = true
			_ = v
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func bytesEqual(a, b json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
