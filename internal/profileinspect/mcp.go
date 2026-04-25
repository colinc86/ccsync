package profileinspect

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/mcpextract"
)

// extractManagedSliceItems expands a managed JSON-slice file into one
// Item per top-level entry. The shape works for both MCP files
// (top-level keys = server names) and the hooks file (top-level keys
// = tool-event names).
//
// Per-entry status lets one drifted server / event show up as
// pending-push without dragging the rest of the slice with it.
func extractManagedSliceItems(slice *mcpextract.Slice, localData, repoData []byte, fileExcluded bool, pathPrefix string) []Item {
	localEntries := decodeSliceEntries(localData)
	repoEntries := decodeSliceEntries(repoData)
	if len(localEntries) == 0 && len(repoEntries) == 0 {
		return nil
	}

	names := unionNames(localEntries, repoEntries)
	sort.Strings(names)

	kind := KindMCPServer
	if slice.JSONPath == "hooks" {
		kind = KindHook
	}

	out := make([]Item, 0, len(names))
	for _, name := range names {
		local, hasLocal := localEntries[name]
		repo, hasRepo := repoEntries[name]
		descBytes := local
		if !hasLocal {
			descBytes = repo
		}
		out = append(out, Item{
			Title:       name,
			Description: sliceEntryDescription(slice, descBytes),
			Path:        pathPrefix + "#" + name,
			Bytes:       int64(len(descBytes)),
			Kind:        kind,
			Status:      mcpServerStatus(hasLocal, hasRepo, local, repo, fileExcluded),
		})
	}
	return out
}

// decodeSliceEntries parses a managed-file body (raw JSON object whose
// keys are servers / events) into a map of name → encoded value.
// Empty / malformed bytes return an empty map so callers can iterate
// without nil checks.
func decodeSliceEntries(data []byte) map[string]json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return doc
}

func unionNames(a, b map[string]json.RawMessage) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// mcpServerStatus picks the per-entry status from (local, repo)
// presence + byte-equality. Mirrors statusFor's file-level logic but
// scoped to a single managed-file entry so drift in one server
// doesn't contaminate stable ones.
func mcpServerStatus(hasLocal, hasRepo bool, local, repo json.RawMessage, fileExcluded bool) Status {
	if fileExcluded {
		return StatusExcluded
	}
	switch {
	case hasLocal && hasRepo:
		if mcpEntriesEqual(local, repo) {
			return StatusSynced
		}
		return StatusPendingPush
	case hasLocal && !hasRepo:
		return StatusPendingPush
	case !hasLocal && hasRepo:
		return StatusPendingPull
	}
	return StatusSynced
}

// mcpEntriesEqual normalises two JSON entries and reports whether
// they're semantically identical. Whitespace, key order, and number
// formatting shouldn't flip an otherwise-synced entry into
// pending-push, so we round-trip through the default marshaller
// before comparing.
func mcpEntriesEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	ab, err := json.Marshal(av)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(bv)
	if err != nil {
		return false
	}
	return string(ab) == string(bb)
}

// sliceEntryDescription dispatches by slice kind to a per-entry one-
// liner: MCP servers describe their command shape; hook events
// describe how many matchers are wired. Failure modes (malformed
// payloads, schemas we don't recognise) fall back to the generic
// category label.
func sliceEntryDescription(slice *mcpextract.Slice, entry json.RawMessage) string {
	if slice == nil {
		return ""
	}
	if slice.JSONPath == "hooks" {
		return hookEventDescription(entry)
	}
	return mcpServerDescription(entry)
}

// mcpServerDescription formats one MCP server entry into a short
// human-readable description. The full entry shape from Claude Code
// can include any of: command, args, env, url, type, description.
// Prefer the explicit description; otherwise synthesise from the
// bits most users would recognise.
func mcpServerDescription(entry json.RawMessage) string {
	var m struct {
		Description string   `json:"description"`
		Command     string   `json:"command"`
		Args        []string `json:"args"`
		URL         string   `json:"url"`
		Type        string   `json:"type"`
	}
	if err := json.Unmarshal(entry, &m); err != nil {
		return category.Label(category.MCPServers)
	}
	if d := strings.TrimSpace(m.Description); d != "" {
		return cleanOneLine(d, 160)
	}
	if m.URL != "" {
		if m.Type != "" {
			return fmt.Sprintf("%s endpoint · %s", m.Type, m.URL)
		}
		return "endpoint · " + m.URL
	}
	if m.Command != "" {
		switch {
		case len(m.Args) == 0:
			return "launches `" + m.Command + "`"
		case m.Command == "npx" || m.Command == "npm":
			return fmt.Sprintf("runs `%s` via %s", m.Args[0], m.Command)
		default:
			return fmt.Sprintf("launches `%s %s`", m.Command, m.Args[0])
		}
	}
	return category.Label(category.MCPServers)
}

// hookEventDescription summarises one $.hooks event entry —
// "PreToolUse", "PostToolUse", etc. Claude Code's hook config nests
// arrays of matchers under each event; show the matcher count so the
// row is at least directionally informative.
func hookEventDescription(entry json.RawMessage) string {
	var matchers []json.RawMessage
	if err := json.Unmarshal(entry, &matchers); err != nil || len(matchers) == 0 {
		return "hook wiring"
	}
	noun := "matcher"
	if len(matchers) != 1 {
		noun = "matchers"
	}
	return fmt.Sprintf("%d %s", len(matchers), noun)
}
