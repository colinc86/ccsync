package category

import "encoding/json"

// MCPOnlyDiff reports whether the only top-level key that differs
// between local and remote claude.json is "mcpServers". The sync
// engine uses this to route changes through the MCPServers review
// category when they're MCP-specific rather than general-settings
// touching — which makes a user-configured
// MCPServers.push=review policy actually fire on new MCP additions,
// instead of getting misrouted to GeneralSettings.
//
// Precondition: caller has already determined local != remote (this
// function answers "given a diff, is it mcp-only"; it doesn't return
// true for the no-diff case). A side that's empty bytes strips to {}
// (legitimate absent file); a side that's malformed JSON returns
// false — we won't claim an MCP-only diff against a garbage document,
// because the user's MCPServers-scoped push policy is the wrong
// channel to route JSON-corruption through. The normal sync flow
// never reaches here with malformed bytes (jsonfilter.Apply errors
// first), but the public API is called from other paths too.
func MCPOnlyDiff(local, remote []byte) bool {
	l, lOK := stripMCP(local)
	r, rOK := stripMCP(remote)
	if !lOK || !rOK {
		return false
	}
	return equalJSON(l, r)
}

// stripMCP returns (doc-without-mcpServers, ok). ok=false when data
// was non-empty but failed to parse; ok=true for empty or valid JSON.
func stripMCP(data []byte) (map[string]any, bool) {
	if len(data) == 0 {
		return map[string]any{}, true
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	delete(doc, "mcpServers")
	return doc, true
}

// MCPServerDiff computes the per-server diff for a three-way merge of
// ~/.claude.json. Returns the list of server-name keys whose definition
// is new or has changed across the three sides, so the review UI can
// show one toggle per affected server.
//
// An item's Action reflects the visible-to-user delta (add, modify,
// delete) from the receiving machine's perspective. For push reviews,
// "add" means "new MCP coming from local that remote doesn't have
// yet"; for pull reviews it flips.
//
// Any of base/local/remote may be nil. When mcpServers is absent on a
// side it's treated as {} for comparison.
func MCPServerDiff(base, local, remote []byte) []MCPItem {
	bMap := decodeMCP(base)
	lMap := decodeMCP(local)
	rMap := decodeMCP(remote)

	names := map[string]struct{}{}
	for k := range lMap {
		names[k] = struct{}{}
	}
	for k := range rMap {
		names[k] = struct{}{}
	}
	for k := range bMap {
		names[k] = struct{}{}
	}

	var items []MCPItem
	for name := range names {
		bv, bOK := bMap[name]
		lv, lOK := lMap[name]
		rv, rOK := rMap[name]

		// Same on both sides as of this sync → no review needed.
		if equalJSON(lv, rv) {
			continue
		}

		// Classify the action from the reviewing machine's perspective.
		// The reviewer cares about what *would change locally* or *would
		// be pushed from local*. We include both perspectives.
		item := MCPItem{
			Name:   name,
			Local:  raw(lv, lOK),
			Remote: raw(rv, rOK),
			Base:   raw(bv, bOK),
		}
		items = append(items, item)
	}
	return items
}

// MCPItem is one review unit — a single server-name entry whose
// definition differs across sides.
type MCPItem struct {
	Name string
	// The raw JSON-encoded value at $.mcpServers.<Name> on each side.
	// Zero-length means the key is absent on that side.
	Base   []byte
	Local  []byte
	Remote []byte
}

// IsAdd reports whether this item represents a new server added by the
// pushing side — absent in base+remote, present locally.
func (it MCPItem) IsAdd() bool {
	return len(it.Base) == 0 && len(it.Remote) == 0 && len(it.Local) > 0
}

// IsModify reports whether both sides have the key but with diverging
// values.
func (it MCPItem) IsModify() bool {
	return len(it.Local) > 0 && len(it.Remote) > 0
}

// IsDelete reports whether the key was removed locally vs remote.
func (it MCPItem) IsDelete() bool {
	return len(it.Local) == 0 && len(it.Remote) > 0
}

func decodeMCP(data []byte) map[string]any {
	if len(data) == 0 {
		return map[string]any{}
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return map[string]any{}
	}
	mcp, _ := doc["mcpServers"].(map[string]any)
	if mcp == nil {
		return map[string]any{}
	}
	return mcp
}

func equalJSON(a, b any) bool {
	ba, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ba) == string(bb)
}

func raw(v any, present bool) []byte {
	if !present {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}
