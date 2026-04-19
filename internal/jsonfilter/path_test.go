package jsonfilter

import (
	"encoding/json"
	"sort"
	"testing"
)

func mustJSON(t *testing.T, s string) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func matchSorted(t *testing.T, pattern string, doc interface{}) []string {
	t.Helper()
	p, err := Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	got := p.Match(doc)
	sort.Strings(got)
	return got
}

func TestCompileRejectsBad(t *testing.T) {
	for _, bad := range []string{"foo", "$foo", "$.", "$..", "$.*.", "$..a.."} {
		if _, err := Compile(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestMatchExactPath(t *testing.T) {
	doc := mustJSON(t, `{"a": {"b": 1}, "c": 2}`)
	got := matchSorted(t, "$.a.b", doc)
	want := []string{"a.b"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMatchWildcard(t *testing.T) {
	doc := mustJSON(t, `{"mcpServers": {"gem": {"env": {"K": "v"}}, "other": {"env": {"K2": "v2"}}}}`)
	got := matchSorted(t, "$.mcpServers.*.env.*", doc)
	want := []string{"mcpServers.gem.env.K", "mcpServers.other.env.K2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestMatchRecursive(t *testing.T) {
	doc := mustJSON(t, `{"a": {"apiKey": "x"}, "b": [{"apiKey": "y"}], "c": {"nested": {"apiKey": "z"}}}`)
	got := matchSorted(t, "$..apiKey", doc)
	want := []string{"a.apiKey", "b.0.apiKey", "c.nested.apiKey"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestMatchRoot(t *testing.T) {
	doc := mustJSON(t, `{"a": 1}`)
	p, _ := Compile("$")
	got := p.Match(doc)
	if len(got) != 1 || got[0] != "" {
		t.Errorf("root match = %v", got)
	}
}
