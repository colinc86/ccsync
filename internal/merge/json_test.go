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
	local := []byte(`{"a":1}`) // deleted b
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
	local := []byte(`{"a":1}`) // deleted b
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
