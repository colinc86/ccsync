// Package merge implements three-way merges for the file kinds ccsync tracks:
// JSON (deep merge with per-key conflict detection), text (via go-diff, per-file
// conflict detection), and binary (last-write-wins by mtime).
package merge

// ConflictKind discriminates the cause of a Conflict.
type ConflictKind int

const (
	ConflictJSONValue      ConflictKind = iota // scalar mismatch at a JSON path
	ConflictJSONStructural                     // type changed (object vs array vs scalar)
	ConflictJSONDeleteMod                      // one side deleted, other modified
	ConflictTextHunk                           // overlapping edits in a text file
)

func (k ConflictKind) String() string {
	switch k {
	case ConflictJSONValue:
		return "JSONValue"
	case ConflictJSONStructural:
		return "JSONStructural"
	case ConflictJSONDeleteMod:
		return "JSONDeleteMod"
	case ConflictTextHunk:
		return "TextHunk"
	}
	return "Unknown"
}

// Conflict describes one unresolved disagreement from a three-way merge.
// Base/Local/Remote hold string representations (JSON-encoded for JSON kinds,
// raw text for text kind) suitable for display AND for reuse when the user
// applies their resolution via sjson.SetRawBytes / DeleteBytes.
// The *Present flags distinguish "absent at this side" from "absent value"
// (e.g. a JSON null or empty string) — needed to correctly apply a choice
// to a delete-vs-modify conflict.
type Conflict struct {
	Path          string
	Kind          ConflictKind
	Base          string
	Local         string
	Remote        string
	BasePresent   bool
	LocalPresent  bool
	RemotePresent bool
}

// Result is the output of a three-way merge. Conflicts empty means clean merge.
type Result struct {
	Merged    []byte
	Conflicts []Conflict
}

// Clean reports whether the merge completed without conflicts.
func (r Result) Clean() bool { return len(r.Conflicts) == 0 }
