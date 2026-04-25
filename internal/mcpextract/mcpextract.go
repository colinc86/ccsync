// Package mcpextract handles the JSON-slice managed-file flow.
//
// ccsync v0.9.0 stopped syncing whole settings/config files and started
// syncing specific subtrees instead — `$.mcpServers` from
// `~/.claude.json`, `$.mcpServers` and `$.hooks` from
// `~/.claude/settings.json`. Each subtree round-trips through a
// per-profile managed file in the repo:
//
//	~/.claude.json:$.mcpServers          ↔  profiles/<n>/.ccsync.mcp.json
//	~/.claude/settings.json:$.mcpServers ↔  profiles/<n>/ccsync.mcp.json
//	~/.claude/settings.json:$.hooks      ↔  profiles/<n>/ccsync.hooks.json
//
// The managed file's body is the raw subtree (a JSON object, no
// envelope) so it's eyeball-readable in a GitHub diff and amenable to
// the existing per-key three-way merger in internal/merge.
//
// On push the sync engine calls Extract against the live source file
// and stages the managed bytes as if they were any other repo path.
// On pull it sees the managed file in pendingLocalWrites, calls Inject
// to splice the new subtree into the live source on disk, and drops
// the managed-path write — the repo's view of the subtree is the
// live file's view, never a separate disk artifact.
package mcpextract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Slice names a subtree round-tripped through a managed file.
type Slice struct {
	// Name is the human label used in error messages.
	Name string
	// SourcePath is the live file the subtree lives in, expanded relative
	// to the user's home (e.g. ".claude.json", ".claude/settings.json").
	SourcePath string
	// JSONPath is the dot-path into SourcePath that the subtree
	// occupies (sjson syntax — "mcpServers" or "hooks", not "$.foo").
	JSONPath string
	// ManagedPath is the repo-relative path of the managed file inside
	// profiles/<name>/ (e.g. ".ccsync.mcp.json").
	ManagedPath string
	// ContentChunk is the state.ContentChunk* identifier that gates this
	// slice via state.IsContentEnabled.
	ContentChunk string
}

// Slices defines the canonical set ccsync v0.9.0 manages. The TUI's
// content section, the sync orchestrator's extract/inject loop, and
// the inspector's grouping all read from this list.
//
// Adding a new slice is a four-step change: append here, add a
// matching state.ContentChunk* constant, add a Settings row in
// internal/tui/settings.go, and update CLAUDE.md's scope section.
func Slices() []Slice {
	return []Slice{
		{
			Name:         "MCP servers (~/.claude.json)",
			SourcePath:   ".claude.json",
			JSONPath:     "mcpServers",
			ManagedPath:  ".ccsync.mcp.json",
			ContentChunk: "mcp_claude_json",
		},
		{
			Name:         "MCP servers (~/.claude/settings.json)",
			SourcePath:   ".claude/settings.json",
			JSONPath:     "mcpServers",
			ManagedPath:  "ccsync.mcp.json",
			ContentChunk: "mcp_settings_json",
		},
		{
			Name:         "Hook wiring (~/.claude/settings.json)",
			SourcePath:   ".claude/settings.json",
			JSONPath:     "hooks",
			ManagedPath:  "ccsync.hooks.json",
			ContentChunk: "hooks_wiring",
		},
	}
}

// SliceByManagedPath returns the Slice whose ManagedPath matches the
// given repo-relative path, or nil if the path doesn't name a managed
// file. Used by the sync orchestrator to decide whether a pending
// pull-write needs the inject treatment.
func SliceByManagedPath(repoRelPath string) *Slice {
	for _, s := range Slices() {
		if s.ManagedPath == repoRelPath {
			sl := s
			return &sl
		}
	}
	return nil
}

// Extract reads the subtree at jsonPath from srcBytes and returns it as
// stable, pretty-printed managed-file bytes. Empty/missing srcBytes
// yields an empty-but-valid `{}` body. Missing subtree (the source has
// no key at jsonPath) likewise yields `{}` — that's what fresh installs
// look like before the user adds a server.
//
// The managed file is the raw subtree, not a wrapper. Example managed
// body for ~/.claude.json:$.mcpServers:
//
//	{
//	  "playwright": {"command": "npx", "args": ["@playwright/mcp"]},
//	  "slack": {"command": "..."}
//	}
//
// Output is deterministic (sorted keys, two-space indent) so identical
// inputs produce identical bytes — important for git's content-
// addressed storage and ccsync's no-op diff detection.
func Extract(srcBytes []byte, jsonPath string) ([]byte, error) {
	if len(srcBytes) == 0 {
		return emptyManaged(), nil
	}
	if !json.Valid(srcBytes) {
		return nil, fmt.Errorf("source is not valid JSON")
	}
	res := gjson.GetBytes(srcBytes, jsonPath)
	if !res.Exists() {
		return emptyManaged(), nil
	}
	// res.Raw is the matched value's bytes verbatim. Re-pretty so the
	// managed file's whitespace is stable regardless of how compact /
	// expanded the source file is.
	return prettyJSON([]byte(res.Raw))
}

// Inject splices managedBytes back into srcBytes at jsonPath, leaving
// every other key in srcBytes untouched. Returns the new source bytes.
//
// Empty/nil srcBytes is treated as `{}` so a first-time injection on a
// machine that's never seen the source file works (creates a one-key
// document). Empty managedBytes is the same as `{}` — tells the
// inject step "drop this subtree, the user has cleared it." Malformed
// managedBytes returns an error rather than producing garbage on disk.
func Inject(srcBytes, managedBytes []byte, jsonPath string) ([]byte, error) {
	if len(managedBytes) == 0 {
		managedBytes = emptyManaged()
	}
	if !json.Valid(managedBytes) {
		return nil, fmt.Errorf("managed file at %q is not valid JSON", jsonPath)
	}
	if len(srcBytes) == 0 {
		srcBytes = []byte("{}")
	}
	if !json.Valid(srcBytes) {
		return nil, fmt.Errorf("source is not valid JSON")
	}
	out, err := sjson.SetRawBytes(srcBytes, jsonPath, managedBytes)
	if err != nil {
		return nil, fmt.Errorf("set %s: %w", jsonPath, err)
	}
	return prettyJSON(out)
}

// ListEntries returns the top-level keys of a managed file in
// stable-sorted order. The drill-down UI calls this to render one
// checkbox per server / hook event.
//
// For nested-shape managed files (e.g. hooks, where each top-level key
// is a tool-event whose value is an array of matchers), this returns
// the event names. The drill-down screen can deepen later if we want
// per-matcher granularity.
//
// Empty / missing managed file returns nil, no error — fresh installs
// have no entries to list.
func ListEntries(managedBytes []byte) ([]string, error) {
	if len(managedBytes) == 0 {
		return nil, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(managedBytes, &doc); err != nil {
		return nil, fmt.Errorf("parse managed file: %w", err)
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// FilterEntries returns a copy of managedBytes with the named entries
// removed. Used on the push side to honour per-entry drill-down
// excludes — a server the user unchecked in Settings → MCP servers
// should not be staged into the managed file at all.
//
// Unknown entries (a name in `excluded` that isn't in managedBytes)
// are silently ignored. Empty `excluded` returns managedBytes
// unchanged (after a re-pretty for byte-stability).
func FilterEntries(managedBytes []byte, excluded []string) ([]byte, error) {
	if len(managedBytes) == 0 {
		return emptyManaged(), nil
	}
	if !json.Valid(managedBytes) {
		return nil, fmt.Errorf("managed file is not valid JSON")
	}
	if len(excluded) == 0 {
		return prettyJSON(managedBytes)
	}
	out := managedBytes
	for _, k := range excluded {
		var err error
		out, err = sjson.DeleteBytes(out, k)
		if err != nil {
			return nil, fmt.Errorf("filter %q: %w", k, err)
		}
	}
	return prettyJSON(out)
}

func emptyManaged() []byte {
	return []byte("{}\n")
}

func prettyJSON(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return emptyManaged(), nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("re-parse for pretty: %w", err)
	}
	if v == nil {
		return emptyManaged(), nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
