package sync

import (
	"encoding/json"
	"testing"

	"github.com/colinc86/ccsync/internal/merge"
)

func TestBuildPerKeyAllLocal(t *testing.T) {
	// Both a and b are genuine conflicts (base differs from both local and remote).
	base := []byte(`{"a":0,"b":0}`)
	local := []byte(`{"a":1,"b":3}`)
	remote := []byte(`{"a":2,"b":4}`)
	res, err := merge.JSON(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 2 {
		t.Fatalf("expected 2 conflicts, got %d", len(res.Conflicts))
	}
	choices := make([]KeyChoice, len(res.Conflicts))
	for i := range choices {
		choices[i] = KeyLocal
	}
	out, err := BuildPerKeyResolution(res.Merged, res.Conflicts, choices)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if v["a"] != 1 || v["b"] != 3 {
		t.Errorf("all-local merge = %v", v)
	}
}

func TestBuildPerKeyMixed(t *testing.T) {
	base := []byte(`{"a":0,"b":0}`)
	local := []byte(`{"a":1,"b":3}`)
	remote := []byte(`{"a":2,"b":4}`)
	res, _ := merge.JSON(base, local, remote)
	choices := make([]KeyChoice, len(res.Conflicts))
	for i, c := range res.Conflicts {
		if c.Path == "a" {
			choices[i] = KeyRemote
		} else {
			choices[i] = KeyLocal
		}
	}
	out, err := BuildPerKeyResolution(res.Merged, res.Conflicts, choices)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	_ = json.Unmarshal(out, &v)
	if v["a"] != 2 || v["b"] != 3 {
		t.Errorf("mixed merge = %v", v)
	}
}

func TestBuildPerKeyDeleteMod(t *testing.T) {
	// Local deleted b, Remote modified b
	base := []byte(`{"a":1,"b":2}`)
	local := []byte(`{"a":1}`)
	remote := []byte(`{"a":1,"b":3}`)
	res, _ := merge.JSON(base, local, remote)

	// Choose local (delete) for the one conflict.
	choices := []KeyChoice{KeyLocal}
	out, err := BuildPerKeyResolution(res.Merged, res.Conflicts, choices)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	_ = json.Unmarshal(out, &v)
	if _, has := v["b"]; has {
		t.Errorf("expected b to be deleted; got %v", v)
	}

	// Choose remote (keep value) — b should be 3.
	choices = []KeyChoice{KeyRemote}
	out, _ = BuildPerKeyResolution(res.Merged, res.Conflicts, choices)
	_ = json.Unmarshal(out, &v)
	if v["b"] != float64(3) {
		t.Errorf("expected b=3; got %v", v)
	}
}
