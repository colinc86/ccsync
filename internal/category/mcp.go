package category

import "encoding/json"

// MCPServerDiff computes the per-server diff for a three-way merge of
// a managed MCP file (.ccsync.mcp.json or ccsync.mcp.json). Each input
// is the raw bytes of the managed file — a JSON object whose top-level
// keys are server names and whose values are the per-server configs:
//
//	{"playwright": {...}, "slack": {...}}
//
// Returns the list of server-name keys whose value is new or has
// changed across the three sides, so the review UI can show one toggle
// per affected server.
//
// Any of base/local/remote may be nil. An empty/missing side is treated
// as the empty object {} for comparison.
func MCPServerDiff(base, local, remote []byte) []MCPItem {
	bMap := decodeServers(base)
	lMap := decodeServers(local)
	rMap := decodeServers(remote)

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

		items = append(items, MCPItem{
			Name:   name,
			Local:  raw(lv, lOK),
			Remote: raw(rv, rOK),
			Base:   raw(bv, bOK),
		})
	}
	return items
}

// MCPItem is one review unit — a single server-name entry whose
// definition differs across sides.
type MCPItem struct {
	Name string
	// The raw JSON-encoded value at <Name> on each side. Zero-length
	// means the key is absent on that side.
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

// decodeServers parses a raw managed-file body as a map of server name
// to server config. Empty/malformed bytes return the empty map; the
// caller already pre-validates upstream (mcpextract/jsonfilter), so
// surfacing a parse error here doesn't add value.
func decodeServers(data []byte) map[string]any {
	if len(data) == 0 {
		return map[string]any{}
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return map[string]any{}
	}
	return doc
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
