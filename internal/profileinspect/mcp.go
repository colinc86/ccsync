package profileinspect

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/jsonfilter"
)

// extractMCPServers parses the mcpServers object out of local and
// repo claude.json payloads and returns one Item per server with a
// per-entry status — a server present and identical on both sides
// is StatusSynced even when unrelated keys in claude.json (theme,
// editorMode, autoUpdates) have drifted. Passing only a local copy
// (repoData == nil) reproduces the pre-v0.8.1 "whole-file status"
// behaviour for call sites that don't have both sides.
//
// Comparison happens AFTER applying the ccsync.yaml jsonFiles rule
// for claude.json (exclude + redact) to the local bytes. Otherwise
// a redacted secret in the repo would always diverge from the
// real secret on disk, pinning every server with a redacted field
// into permanent "pending push" — the v0.8.1 report shape.
//
// Each server's Title is the map key ("gemini-embedding"). Its
// Description is synthesised from the `command` + first `args`
// entry when no explicit description is on the entry — the user-
// facing gloss is "launches `gemini-mcp`" or "runs via `npx
// gemini-embedding`" depending on how the server is wired.
func extractMCPServers(localData, repoData []byte, rule config.JSONFileRule, profile string, fileExcluded bool, pathPrefix string) []Item {
	// Apply redaction/filtering so local's real secrets don't
	// register as drift against the repo's placeholders.
	compLocal := effectiveLocalForCompare(localData, rule, profile)
	localServers := parseMCPServers(compLocal)
	repoServers := parseMCPServers(repoData)
	if len(localServers) == 0 && len(repoServers) == 0 {
		return nil
	}

	// Union of server names across both sides so a repo-only server
	// surfaces as a pending pull in the inspector.
	names := make([]string, 0, len(localServers)+len(repoServers))
	seen := map[string]bool{}
	for k := range localServers {
		if !seen[k] {
			names = append(names, k)
			seen[k] = true
		}
	}
	for k := range repoServers {
		if !seen[k] {
			names = append(names, k)
			seen[k] = true
		}
	}
	sort.Strings(names)

	items := make([]Item, 0, len(names))
	for _, name := range names {
		local, hasLocal := localServers[name]
		repo, hasRepo := repoServers[name]

		// Description bytes: prefer the local view (what the user
		// has in front of them); fall back to repo when this is a
		// pending pull.
		descBytes := local
		if !hasLocal {
			descBytes = repo
		}

		items = append(items, Item{
			Title:       name,
			Description: mcpServerDescription(descBytes),
			Path:        pathPrefix + "#mcpServers." + name,
			Bytes:       int64(len(descBytes)),
			Kind:        KindMCPServer,
			Status:      mcpServerStatus(hasLocal, hasRepo, local, repo, fileExcluded),
		})
	}
	return items
}

// effectiveLocalForCompare runs the configured jsonfilter rule over
// local claude.json bytes so comparison against the repo copy is
// apples-to-apples. Without this, a redact rule (e.g. env secrets)
// guarantees drift: local has the real value, repo has the
// placeholder, and every server with a redacted field appears
// permanently pending-push. Returns the raw input unchanged when
// the rule is empty or filtering fails — the caller falls back to
// the pre-v0.8.1 behaviour for those edge cases.
func effectiveLocalForCompare(localData []byte, rule config.JSONFileRule, profile string) []byte {
	if len(localData) == 0 {
		return localData
	}
	if len(rule.Exclude) == 0 && len(rule.Redact) == 0 && len(rule.Include) == 0 {
		return localData
	}
	res, err := jsonfilter.Apply(localData, rule, profile)
	if err != nil {
		return localData
	}
	return res.Data
}

// parseMCPServers pulls the raw $.mcpServers map out of a claude.json
// payload. Nil or malformed JSON returns an empty map so callers can
// iterate without a nil check. The values are json.RawMessage so we
// can byte-compare entries without round-tripping through a struct
// that might lose whitespace-sensitive ordering.
func parseMCPServers(data []byte) map[string]json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return doc.MCPServers
}

// mcpServerStatus picks the per-server status from (local, repo)
// presence + byte-equality. Mirrors statusFor's file-level logic
// but scoped to a single $.mcpServers entry so drift elsewhere in
// claude.json doesn't contaminate stable servers.
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

// mcpEntriesEqual normalises two JSON server entries and reports
// whether they're semantically identical. Whitespace, key order,
// and number formatting shouldn't flip an otherwise-synced server
// into pending-push, so we round-trip through the default marshaller
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

// mcpServerDescription formats one map value under $.mcpServers
// into a short human-readable description. The full entry shape
// from Claude Code can include any of: command, args, env, url,
// type, description. We prefer an explicit description field when
// present; otherwise synthesise from the bits most users would
// recognise ("launches gemini-mcp", "HTTP endpoint at …").
func mcpServerDescription(entry json.RawMessage) string {
	var m struct {
		Description string   `json:"description"`
		Command     string   `json:"command"`
		Args        []string `json:"args"`
		URL         string   `json:"url"`
		Type        string   `json:"type"`
	}
	if err := json.Unmarshal(entry, &m); err != nil {
		return "MCP server"
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
			// `npx foo-mcp` reads cleaner as "runs foo-mcp via npx"
			return fmt.Sprintf("runs `%s` via %s", m.Args[0], m.Command)
		default:
			return fmt.Sprintf("launches `%s %s`", m.Command, m.Args[0])
		}
	}
	return "MCP server"
}

// extractSettingsSummary turns the non-mcpServers portion of
// claude.json into a single Settings Item. The description
// enumerates the top-level keys we'd sync (up to 5, plus "…N more"
// for the rest) so the user can tell at a glance what's in the
// payload without opening the file.
func extractSettingsSummary(data []byte, status Status, path string) *Item {
	if len(data) == 0 {
		return nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return &Item{
			Title:       "Claude Code settings",
			Description: "claude.json (couldn't parse)",
			Path:        path,
			Bytes:       int64(len(data)),
			Kind:        KindSettings,
			Status:      status,
		}
	}
	// Drop mcpServers — it gets its own Items above. Leave everything
	// else as-is so the summary reflects what we'd actually sync.
	delete(doc, "mcpServers")
	if len(doc) == 0 {
		return nil
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	max := 5
	display := keys
	extra := 0
	if len(display) > max {
		display = display[:max]
		extra = len(keys) - max
	}
	desc := strings.Join(display, ", ")
	if extra > 0 {
		desc += fmt.Sprintf(" · +%d more", extra)
	}
	return &Item{
		Title:       "Claude Code settings",
		Description: desc,
		Path:        path,
		Bytes:       int64(len(data)),
		Kind:        KindSettings,
		Status:      status,
	}
}
