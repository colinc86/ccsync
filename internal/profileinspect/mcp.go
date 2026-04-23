package profileinspect

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// extractMCPServers parses the mcpServers object out of a
// claude.json payload. Returns one Item per server. Missing key or
// malformed JSON yields an empty slice — callers treat that as "no
// servers to surface" rather than an error.
//
// Each server's Title is the map key ("gemini-embedding"). Its
// Description is synthesised from the `command` + first `args` entry
// when no explicit description is on the entry — the user-facing
// gloss is "launches `gemini-mcp`" or "runs via `npx
// gemini-embedding`" depending on how the server is wired.
func extractMCPServers(data []byte, status Status, pathPrefix string) []Item {
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	if len(doc.MCPServers) == 0 {
		return nil
	}
	names := make([]string, 0, len(doc.MCPServers))
	for k := range doc.MCPServers {
		names = append(names, k)
	}
	sort.Strings(names)

	items := make([]Item, 0, len(names))
	for _, name := range names {
		entry := doc.MCPServers[name]
		desc := mcpServerDescription(entry)
		items = append(items, Item{
			Title:       name,
			Description: desc,
			Path:        pathPrefix + "#mcpServers." + name,
			Bytes:       int64(len(entry)),
			Kind:        KindMCPServer,
			Status:      status,
		})
	}
	return items
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
