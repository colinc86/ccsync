package merge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// JSON three-way merges base, local, and remote JSON bytes.
// Any of base/local/remote may be nil, signaling "absent" at that side.
func JSON(base, local, remote []byte) (Result, error) {
	bv, bOK, err := parseMaybe(base)
	if err != nil {
		return Result{}, fmt.Errorf("parse base: %w", err)
	}
	lv, lOK, err := parseMaybe(local)
	if err != nil {
		return Result{}, fmt.Errorf("parse local: %w", err)
	}
	rv, rOK, err := parseMaybe(remote)
	if err != nil {
		return Result{}, fmt.Errorf("parse remote: %w", err)
	}

	merged, present, conflicts := mergeNode("", bv, bOK, lv, lOK, rv, rOK)

	if !present {
		// File absent in the result; Merged stays nil, caller reads conflicts.
		return Result{Conflicts: conflicts}, nil
	}

	out, err := encodeJSON(merged)
	if err != nil {
		return Result{}, err
	}
	return Result{Merged: out, Conflicts: conflicts}, nil
}

func parseMaybe(data []byte) (any, bool, error) {
	if len(data) == 0 {
		return nil, false, nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, false, err
	}
	return v, true, nil
}

// mergeNode is the recursive heart of JSON merge.
// Returns (mergedValue, present, conflicts). present=false means the key is deleted in the merge.
func mergeNode(path string, base any, hasBase bool, local any, hasLocal bool, remote any, hasRemote bool) (any, bool, []Conflict) {
	switch {
	case !hasLocal && !hasRemote:
		return nil, false, nil
	case !hasLocal:
		if !hasBase {
			return remote, true, nil // pure add from remote (nothing deleted)
		}
		if equalJSON(base, remote) {
			return nil, false, nil // remote unchanged, local deleted → accept delete
		}
		return remote, true, []Conflict{scalarConflict(path, ConflictJSONDeleteMod, base, hasBase, local, hasLocal, remote, hasRemote)}
	case !hasRemote:
		if !hasBase {
			return local, true, nil // pure add from local
		}
		if equalJSON(base, local) {
			return nil, false, nil // local unchanged, remote deleted
		}
		return local, true, []Conflict{scalarConflict(path, ConflictJSONDeleteMod, base, hasBase, local, hasLocal, remote, hasRemote)}
	}

	if equalJSON(local, remote) {
		return local, true, nil
	}
	if hasBase && equalJSON(base, local) {
		return remote, true, nil
	}
	if hasBase && equalJSON(base, remote) {
		return local, true, nil
	}

	lmap, lOK := local.(map[string]any)
	rmap, rOK := remote.(map[string]any)
	if lOK && rOK {
		var bmap map[string]any
		if hasBase {
			bmap, _ = base.(map[string]any)
		}
		merged, conflicts := mergeMaps(path, bmap, lmap, rmap, hasBase && bmap != nil)
		return merged, true, conflicts
	}

	kind := ConflictJSONValue
	if typeOf(local) != typeOf(remote) {
		kind = ConflictJSONStructural
	}
	return local, true, []Conflict{scalarConflict(path, kind, base, hasBase, local, hasLocal, remote, hasRemote)}
}

func mergeMaps(parentPath string, base, local, remote map[string]any, hasBase bool) (map[string]any, []Conflict) {
	keys := uniqueKeys(base, local, remote)
	sort.Strings(keys)

	out := map[string]any{}
	var conflicts []Conflict
	for _, k := range keys {
		bv, bOK := base[k]
		lv, lOK := local[k]
		rv, rOK := remote[k]
		if !hasBase {
			bOK = false
		}
		merged, present, c := mergeNode(joinPath(parentPath, k), bv, bOK, lv, lOK, rv, rOK)
		if present {
			out[k] = merged
		}
		conflicts = append(conflicts, c...)
	}
	return out, conflicts
}

func uniqueKeys(maps ...map[string]any) []string {
	seen := map[string]struct{}{}
	for _, m := range maps {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func equalJSON(a, b any) bool {
	ba, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ba, bb)
}

func typeOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func scalarConflict(path string, kind ConflictKind, base any, hasBase bool, local any, hasLocal bool, remote any, hasRemote bool) Conflict {
	return Conflict{
		Path:   path,
		Kind:   kind,
		Base:   jsonStr(base, hasBase),
		Local:  jsonStr(local, hasLocal),
		Remote: jsonStr(remote, hasRemote),
	}
}

func jsonStr(v any, present bool) string {
	if !present {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<unencodable: %v>", err)
	}
	return string(b)
}

func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
