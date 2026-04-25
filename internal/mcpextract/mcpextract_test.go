package mcpextract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestExtract_PullsNamedSubtree pins the happy path: a source with a
// non-trivial mcpServers subtree extracts to that subtree's bytes,
// nothing else.
func TestExtract_PullsNamedSubtree(t *testing.T) {
	src := []byte(`{
		"theme": "dark",
		"sessionId": "abc",
		"mcpServers": {
			"playwright": {"command": "npx", "args": ["@playwright/mcp"]}
		}
	}`)
	got, err := Extract(src, "mcpServers")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !json.Valid(got) {
		t.Fatalf("Extract output is not valid JSON: %s", got)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("unmarshal Extract output: %v", err)
	}
	if _, ok := doc["playwright"]; !ok {
		t.Errorf("expected playwright key in extracted body, got: %s", got)
	}
	// The non-mcpServers keys must NOT leak into the managed file.
	if _, ok := doc["theme"]; ok {
		t.Errorf("Extract leaked unrelated key into managed body: %s", got)
	}
}

// TestExtract_MissingSubtreeIsEmptyManaged pins that a source with no
// mcpServers key still produces a valid managed body. Fresh installs
// hit this path on the first sync.
func TestExtract_MissingSubtreeIsEmptyManaged(t *testing.T) {
	src := []byte(`{"theme":"dark"}`)
	got, err := Extract(src, "mcpServers")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !json.Valid(got) {
		t.Fatalf("Extract on missing subtree must produce valid JSON; got: %s", got)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc) != 0 {
		t.Errorf("expected empty managed body, got: %s", got)
	}
}

// TestExtract_EmptySourceProducesEmptyManaged pins that a missing
// source file (e.g. ~/.claude.json doesn't exist on this machine) is
// treated as if every subtree is absent.
func TestExtract_EmptySourceProducesEmptyManaged(t *testing.T) {
	got, err := Extract(nil, "mcpServers")
	if err != nil {
		t.Fatalf("Extract on nil source: %v", err)
	}
	if !json.Valid(got) {
		t.Fatalf("Extract on nil must produce valid JSON; got: %s", got)
	}
}

// TestInject_RoundTripPreservesUnrelatedKeys is the load-bearing
// invariant. Pull writes the managed file's bytes back into the live
// source — every key OTHER than the named subtree must survive
// untouched. Pre-fix, the v0.8.x flow rewrote ~/.claude.json wholesale
// and kept losing per-machine keys (sessionId, oauthAccount, …);
// this test pins the v0.9.0 promise that that can't happen.
func TestInject_RoundTripPreservesUnrelatedKeys(t *testing.T) {
	src := []byte(`{
		"theme": "dark",
		"sessionId": "abc-123",
		"oauthAccount": {"email": "user@example.com"},
		"mcpServers": {"old": {"command": "x"}}
	}`)
	managed := []byte(`{"new": {"command": "y"}}`)

	out, err := Inject(src, managed, "mcpServers")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if !json.Valid(out) {
		t.Fatalf("Inject produced invalid JSON: %s", out)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Unrelated keys preserved.
	if doc["theme"] != "dark" {
		t.Errorf("Inject lost theme key: %v", doc["theme"])
	}
	if doc["sessionId"] != "abc-123" {
		t.Errorf("Inject lost sessionId: %v", doc["sessionId"])
	}
	if oauth, ok := doc["oauthAccount"].(map[string]any); !ok || oauth["email"] != "user@example.com" {
		t.Errorf("Inject lost oauthAccount: %v", doc["oauthAccount"])
	}
	// Subtree replaced with new content (not merged with old).
	mcp, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or wrong type: %v", doc["mcpServers"])
	}
	if _, ok := mcp["new"]; !ok {
		t.Errorf("Inject didn't write new server: %v", mcp)
	}
	if _, ok := mcp["old"]; ok {
		t.Errorf("Inject merged old subtree instead of replacing: %v", mcp)
	}
}

// TestInject_FreshSourceCreatesDocument pins that a machine that's
// never had ~/.claude.json (or whatever the source is) gets a
// well-formed one-key document on first inject.
func TestInject_FreshSourceCreatesDocument(t *testing.T) {
	managed := []byte(`{"x": {"command": "y"}}`)
	out, err := Inject(nil, managed, "mcpServers")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if !json.Valid(out) {
		t.Fatalf("Inject on fresh source produced invalid JSON: %s", out)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mcp, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcpServers in fresh-source inject: %v", doc)
	}
	if _, ok := mcp["x"]; !ok {
		t.Errorf("expected x server in fresh-source inject: %v", mcp)
	}
}

// TestInject_RejectsMalformedManagedFile pins that a corrupted managed
// file in the repo doesn't get written through to the live source.
// The user can recover by reverting the managed file via git; we want
// to fail loud, not produce JSON soup.
func TestInject_RejectsMalformedManagedFile(t *testing.T) {
	src := []byte(`{"theme":"dark"}`)
	_, err := Inject(src, []byte(`{"broken`), "mcpServers")
	if err == nil {
		t.Error("expected error on malformed managed file, got nil")
	}
	if !strings.Contains(err.Error(), "valid JSON") {
		t.Errorf("error should mention JSON validity, got: %v", err)
	}
}

// TestExtractInject_RoundTrip pins that Extract followed by Inject
// against an unchanged source returns the managed payload verbatim
// (modulo whitespace) — the source is unchanged because the subtree
// is unchanged. This is the no-op sync path.
func TestExtractInject_RoundTrip(t *testing.T) {
	src := []byte(`{"theme":"dark","mcpServers":{"x":{"command":"y"}}}`)
	managed, err := Extract(src, "mcpServers")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	out, err := Inject(src, managed, "mcpServers")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	var srcDoc, outDoc map[string]any
	_ = json.Unmarshal(src, &srcDoc)
	_ = json.Unmarshal(out, &outDoc)
	if !reflect.DeepEqual(srcDoc, outDoc) {
		t.Errorf("round-trip changed source content; src=%s out=%s", src, out)
	}
}

// TestListEntries_StableOrder pins that the drill-down screen sees
// keys in deterministic order — same managed bytes → same row order
// across launches.
func TestListEntries_StableOrder(t *testing.T) {
	managed := []byte(`{"slack":{"x":1},"playwright":{"x":2},"jira":{"x":3}}`)
	got, err := ListEntries(managed)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	want := []string{"jira", "playwright", "slack"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListEntries = %v, want %v", got, want)
	}
}

// TestListEntries_EmptyOrMissing pins zero-state behaviour — fresh
// installs and missing-managed-file paths both return nil with no
// error.
func TestListEntries_EmptyOrMissing(t *testing.T) {
	if got, err := ListEntries(nil); err != nil || got != nil {
		t.Errorf("nil bytes: got %v, err %v; want nil, nil", got, err)
	}
	if got, err := ListEntries([]byte(`{}`)); err != nil || len(got) != 0 {
		t.Errorf("empty object: got %v, err %v; want empty slice, nil", got, err)
	}
}

// TestFilterEntries_RemovesNamed pins that user-unchecked entries
// don't make it into the staged managed bytes — the per-server
// drill-down's whole job depends on this.
func TestFilterEntries_RemovesNamed(t *testing.T) {
	managed := []byte(`{"keep":{"x":1},"drop":{"x":2},"alsokeep":{"x":3}}`)
	out, err := FilterEntries(managed, []string{"drop"})
	if err != nil {
		t.Fatalf("FilterEntries: %v", err)
	}
	var doc map[string]any
	_ = json.Unmarshal(out, &doc)
	if _, ok := doc["drop"]; ok {
		t.Errorf("FilterEntries left %q in output: %s", "drop", out)
	}
	if _, ok := doc["keep"]; !ok {
		t.Errorf("FilterEntries removed unrelated %q: %s", "keep", out)
	}
}

// TestSliceByManagedPath pins the routing table the sync orchestrator
// uses to decide whether a pending pull is a managed file or just a
// regular file write. Misrouting either way means a managed file
// shows up on disk under its repo path (wrong) or a real file gets
// inject'd into the source (wrong shape).
func TestSliceByManagedPath(t *testing.T) {
	if s := SliceByManagedPath(".ccsync.mcp.json"); s == nil || s.SourcePath != ".claude.json" {
		t.Errorf(".ccsync.mcp.json should map to .claude.json slice; got %+v", s)
	}
	if s := SliceByManagedPath("ccsync.mcp.json"); s == nil || s.SourcePath != ".claude/settings.json" {
		t.Errorf("ccsync.mcp.json should map to settings.json slice; got %+v", s)
	}
	if s := SliceByManagedPath("ccsync.hooks.json"); s == nil || s.JSONPath != "hooks" {
		t.Errorf("ccsync.hooks.json should map to settings.json:hooks; got %+v", s)
	}
	if s := SliceByManagedPath("claude/agents/foo.md"); s != nil {
		t.Errorf("regular path should not match a managed slice; got %+v", s)
	}
}
