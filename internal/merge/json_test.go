package merge

import (
	"encoding/json"
	"testing"
)

func equalDocs(t *testing.T, got, want []byte) {
	t.Helper()
	var gv, wv any
	if err := json.Unmarshal(got, &gv); err != nil {
		t.Fatalf("got not valid JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal(want, &wv); err != nil {
		t.Fatalf("want not valid JSON: %v\n%s", err, want)
	}
	gb, _ := json.Marshal(gv)
	wb, _ := json.Marshal(wv)
	if string(gb) != string(wb) {
		t.Errorf("mismatch\n  got:  %s\n  want: %s", gb, wb)
	}
}

func TestJSONCleanDisjointChanges(t *testing.T) {
	base := []byte(`{"a": 1, "b": 2}`)
	local := []byte(`{"a": 1, "b": 3}`)
	remote := []byte(`{"a": 2, "b": 2}`)
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Fatalf("expected clean, got conflicts %v", r.Conflicts)
	}
	equalDocs(t, r.Merged, []byte(`{"a":2,"b":3}`))
}

func TestJSONIdenticalBothSides(t *testing.T) {
	base := []byte(`{"a":1}`)
	local := []byte(`{"a":2}`)
	remote := []byte(`{"a":2}`)
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Errorf("conflicts unexpected: %v", r.Conflicts)
	}
	equalDocs(t, r.Merged, []byte(`{"a":2}`))
}

func TestJSONScalarConflict(t *testing.T) {
	base := []byte(`{"a":1}`)
	local := []byte(`{"a":2}`)
	remote := []byte(`{"a":3}`)
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Conflicts) != 1 || r.Conflicts[0].Path != "a" {
		t.Fatalf("expected 1 conflict at 'a', got %+v", r.Conflicts)
	}
	if r.Conflicts[0].Kind != ConflictJSONValue {
		t.Errorf("kind = %s", r.Conflicts[0].Kind)
	}
	// Merged defaults to local on conflict.
	equalDocs(t, r.Merged, []byte(`{"a":2}`))
}

func TestJSONStructuralConflict(t *testing.T) {
	base := []byte(`{"a":1}`)
	local := []byte(`{"a":{"k":"v"}}`)
	remote := []byte(`{"a":[1,2]}`)
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %+v", len(r.Conflicts), r.Conflicts)
	}
	if r.Conflicts[0].Kind != ConflictJSONStructural {
		t.Errorf("kind = %s", r.Conflicts[0].Kind)
	}
}

func TestJSONDeleteVsModify(t *testing.T) {
	base := []byte(`{"a":1,"b":2}`)
	local := []byte(`{"a":1}`)        // deleted b
	remote := []byte(`{"a":1,"b":3}`) // modified b
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Conflicts) == 0 {
		t.Fatal("expected conflict for delete-vs-modify")
	}
	if r.Conflicts[0].Kind != ConflictJSONDeleteMod {
		t.Errorf("kind = %s", r.Conflicts[0].Kind)
	}
}

func TestJSONDeleteClean(t *testing.T) {
	base := []byte(`{"a":1,"b":2}`)
	local := []byte(`{"a":1}`)        // deleted b
	remote := []byte(`{"a":1,"b":2}`) // unchanged
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Fatalf("expected clean delete, got %+v", r.Conflicts)
	}
	equalDocs(t, r.Merged, []byte(`{"a":1}`))
}

func TestJSONNestedMerge(t *testing.T) {
	base := []byte(`{"settings":{"theme":"dark","font":12}}`)
	local := []byte(`{"settings":{"theme":"light","font":12}}`)
	remote := []byte(`{"settings":{"theme":"dark","font":14}}`)
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Fatalf("expected clean nested merge, got %+v", r.Conflicts)
	}
	equalDocs(t, r.Merged, []byte(`{"settings":{"theme":"light","font":14}}`))
}

func TestJSONBothAddedSame(t *testing.T) {
	local := []byte(`{"a":1}`)
	remote := []byte(`{"a":1}`)
	r, err := JSON(nil, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Errorf("both-added-same should be clean, got %+v", r.Conflicts)
	}
}

func TestJSONBothAddedDifferent(t *testing.T) {
	local := []byte(`{"a":1}`)
	remote := []byte(`{"b":2}`)
	r, err := JSON(nil, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Errorf("disjoint fields with no base should merge cleanly: got %+v", r.Conflicts)
	}
	equalDocs(t, r.Merged, []byte(`{"a":1,"b":2}`))
}

// TestJSONExplicitNullPreservedAcrossSides pins the edge case where a
// key holds an explicit null on one side — JSON distinguishes "key
// present with null value" from "key absent", and the merge must not
// collapse them. If someone refactors mergeNode's hasLocal/hasRemote
// checks and accidentally treats nil-value as "absent", this test
// catches it: a user-set explicit null would get silently replaced by
// whatever the other side has.
func TestJSONExplicitNullPreservedAcrossSides(t *testing.T) {
	// Both sides have explicit null for oauth → merge keeps null.
	base := []byte(`{"oauth":"old"}`)
	local := []byte(`{"oauth":null}`)
	remote := []byte(`{"oauth":null}`)
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean() {
		t.Fatalf("unanimous null should be clean, got %+v", r.Conflicts)
	}
	equalDocs(t, r.Merged, []byte(`{"oauth":null}`))

	// One side sets null, the other modifies → conflict (not silent
	// data loss of the null intent).
	local2 := []byte(`{"oauth":null}`)
	remote2 := []byte(`{"oauth":"remote-val"}`)
	r2, err := JSON(base, local2, remote2)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Clean() {
		t.Errorf("null-vs-modify should be a conflict, not a silent choice")
	}
}

// TestJSONArraysTreatedAtomically documents the design choice: arrays
// are merged whole, not element-by-element. Two sides adding different
// permissions to `.allow` surface as a conflict (user-visible), not
// as a silent concatenation. This is the correct behavior for
// permission lists and similar security-sensitive arrays — a silent
// concat would mean "one machine granted something, the other saw it
// as approved." Pin it.
func TestJSONArraysTreatedAtomically(t *testing.T) {
	base := []byte(`{"allow":["a"]}`)
	local := []byte(`{"allow":["a","b"]}`)  // local added b
	remote := []byte(`{"allow":["a","c"]}`) // remote added c
	r, err := JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if r.Clean() {
		t.Fatal("divergent array edits must conflict, not silent-concat")
	}
	if r.Conflicts[0].Path != "allow" {
		t.Errorf("conflict path = %q, want 'allow'", r.Conflicts[0].Path)
	}
}
