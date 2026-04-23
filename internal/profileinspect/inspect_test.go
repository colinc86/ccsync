package profileinspect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/state"
)

// TestExtractMarkdownMeta_Frontmatter pins the primary extraction
// path: a markdown file with YAML frontmatter containing name +
// description yields exactly those two strings. Fallbacks below.
func TestExtractMarkdownMeta_Frontmatter(t *testing.T) {
	body := []byte(`---
name: research
description: Run a multi-source research pipeline
---

# Research

Body paragraph here.
`)
	title, desc := extractMarkdownMeta(body, "foo.md")
	if title != "research" {
		t.Errorf("title = %q, want research", title)
	}
	if desc != "Run a multi-source research pipeline" {
		t.Errorf("description = %q", desc)
	}
}

// TestExtractMarkdownMeta_H1Fallback — no frontmatter, but an H1
// heading, so the H1 becomes the title and the first paragraph
// becomes the description.
func TestExtractMarkdownMeta_H1Fallback(t *testing.T) {
	body := []byte(`# Research Helper

Helps you research things across multiple sources.

More body below.`)
	title, desc := extractMarkdownMeta(body, "skills/research-orchestration/SKILL.md")
	if title != "Research Helper" {
		t.Errorf("title = %q", title)
	}
	if desc != "Helps you research things across multiple sources." {
		t.Errorf("description = %q", desc)
	}
}

// TestExtractMarkdownMeta_FilenameFallback — no frontmatter, no H1;
// title falls through to the filename stem. Skill convention:
// SKILL.md inside a directory uses the directory name.
func TestExtractMarkdownMeta_FilenameFallback(t *testing.T) {
	body := []byte(`plain body, no heading.`)
	title, _ := extractMarkdownMeta(body, "claude/skills/my-thing/SKILL.md")
	if title != "my-thing" {
		t.Errorf("title = %q, want my-thing", title)
	}
}

// TestExtractMCPServers_Basic pins the command+args synthesis when
// no description field is present on an MCP entry.
func TestExtractMCPServers_Basic(t *testing.T) {
	data := []byte(`{"mcpServers":{"gemini":{"command":"gemini-mcp","args":["--model","gemini-pro"]},"playwright":{"command":"npx","args":["playwright-mcp"]}}}`)
	// Local and repo identical → both servers synced.
	items := extractMCPServers(data, data, config.JSONFileRule{}, "default", false, "claude.json")
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	// Alphabetical by name.
	if items[0].Title != "gemini" {
		t.Errorf("first title = %q, want gemini", items[0].Title)
	}
	if items[0].Status != StatusSynced {
		t.Errorf("gemini status = %q, want synced", items[0].Status.String())
	}
	if items[1].Description != "runs `playwright-mcp` via npx" {
		t.Errorf("npx synth = %q", items[1].Description)
	}
}

// TestExtractMCPServers_WithExplicitDescription pins the explicit-
// description path: when the entry has a description field, it wins
// over command-synthesis.
func TestExtractMCPServers_WithExplicitDescription(t *testing.T) {
	data := []byte(`{"mcpServers":{"gemini":{"command":"gemini-mcp","description":"Embedding + retrieval"}}}`)
	items := extractMCPServers(data, data, config.JSONFileRule{}, "default", false, "claude.json")
	if len(items) != 1 || items[0].Description != "Embedding + retrieval" {
		t.Errorf("unexpected items: %+v", items)
	}
}

// TestExtractSettingsSummary lists the top-level keys, drops
// mcpServers, truncates to 5 with "+N more".
func TestExtractSettingsSummary(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"theme":       "dark",
		"autoUpdates": true,
		"mcpServers":  map[string]any{"x": nil}, // should be dropped
		"editorMode":  "vim",
		"foo":         1,
		"bar":         2,
		"baz":         3,
		"qux":         4,
	})
	item := extractSettingsSummary(raw, StatusSynced, "claude.json")
	if item == nil {
		t.Fatal("expected a settings item")
	}
	// 7 non-mcp keys → first 5 listed, "+2 more" trailer.
	if item.Description == "" {
		t.Error("description should enumerate keys")
	}
	if !containsAll(item.Description, "autoUpdates", "bar", "baz", "editorMode", "foo") {
		t.Errorf("description missing expected keys: %q", item.Description)
	}
}

// TestInspect_MCPServerStatusIsPerEntry pins the per-server status
// computation: an MCP server whose own JSON entry is byte-identical
// in local vs repo claude.json must show StatusSynced even when the
// rest of the file has drifted. The pre-fix behaviour was to stamp
// every server with the whole-file status, so a single local-only
// `theme` change silently marked every synced MCP server as
// "pending push" — users saw the gemini server stuck pending from
// first launch and never returning to synced, which is what
// prompted the v0.8.1 report.
func TestInspect_MCPServerStatusIsPerEntry(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, "home", ".claude")
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	repoPath := filepath.Join(tmp, "repo")

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Local: gemini MCP + a theme override that the repo's copy
	// doesn't know about yet. Repo: same gemini, older theme.
	writeFile(claudeJSON,
		`{"theme":"dark","mcpServers":{"gemini":{"command":"gemini-mcp"}}}`)
	writeFile(filepath.Join(repoPath, "profiles/default/claude.json"),
		`{"theme":"light","mcpServers":{"gemini":{"command":"gemini-mcp"}}}`)

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// Populate LastSyncedSHA so drift counts as a post-sync local
	// edit (pending push) rather than first-sync-takes-remote
	// (pending pull). The test is about per-entry MCP isolation,
	// not first-sync direction flipping.
	st := &state.State{
		ActiveProfile: "default",
		LastSyncedSHA: map[string]string{"default": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
	}
	v, err := Inspect(Inputs{
		Config: cfg, State: st,
		ClaudeDir: claudeDir, ClaudeJSON: claudeJSON, RepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	var gotGemini *Item
	var gotSettings *Item
	for _, s := range v.Sections {
		for i := range s.Items {
			it := &s.Items[i]
			if it.Kind == KindMCPServer && it.Title == "gemini" {
				gotGemini = it
			}
			if it.Kind == KindSettings {
				gotSettings = it
			}
		}
	}
	if gotGemini == nil {
		t.Fatal("expected a gemini MCP server item")
	}
	if gotGemini.Status != StatusSynced {
		t.Errorf("gemini MCP status = %q, want synced — its entry is byte-identical across local and repo; the theme drift in the rest of claude.json should not contaminate per-server status",
			gotGemini.Status.String())
	}
	// The settings summary SHOULD still flag pending-push because
	// the non-mcp portion of claude.json genuinely differs.
	if gotSettings == nil {
		t.Fatal("expected a settings summary item")
	}
	if gotSettings.Status != StatusPendingPush {
		t.Errorf("settings status = %q, want pending push (theme actually differs)", gotSettings.Status.String())
	}
}

// TestInspect_FirstSyncDriftRendersAsPendingPull pins the direction
// flip for drift when the active profile has no LastSyncedSHA yet —
// i.e. the user's first sync under this profile hasn't happened.
// Pre-fix statusFor returned StatusPendingPush for any "both sides
// differ" row, which was misleading: the sync engine's
// first-sync-takes-remote rule (v0.6.4) actually PULLS remote down
// and overwrites local, so rendering the row as "pending push"
// suggests the wrong direction to the user. Users reinstalling on
// a second machine saw "everything pending push" and reasonably
// worried their local content was about to overwrite the fleet
// repo, when in fact the first sync would do the opposite.
//
// Fix: when LastSyncedSHA[active] == "" treat drifted-overlap rows
// as StatusPendingPull to mirror what the sync engine will do.
func TestInspect_FirstSyncDriftRendersAsPendingPull(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, "home", ".claude")
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	repoPath := filepath.Join(tmp, "repo")

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// CLAUDE.md exists on both sides with different content. No
	// prior sync for this profile.
	writeFile(filepath.Join(claudeDir, "CLAUDE.md"), "# Local version\n")
	writeFile(filepath.Join(repoPath, "profiles/default/claude/CLAUDE.md"), "# Repo version\n")
	writeFile(claudeJSON, `{}`)

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// LastSyncedSHA empty for "default" — this is a first-sync
	// scenario.
	st := &state.State{ActiveProfile: "default", LastSyncedSHA: map[string]string{}}
	v, err := Inspect(Inputs{
		Config: cfg, State: st,
		ClaudeDir: claudeDir, ClaudeJSON: claudeJSON, RepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	var got *Item
	for _, s := range v.Sections {
		for i := range s.Items {
			it := &s.Items[i]
			if it.Kind == KindClaudeMD {
				got = it
			}
		}
	}
	if got == nil {
		t.Fatal("expected a CLAUDE.md item in the view")
	}
	if got.Status != StatusPendingPull {
		t.Errorf("CLAUDE.md status = %q, want pending pull — on first sync under this profile the engine takes remote; rendering the row as pending push suggests the wrong direction and alarms users who worry their local is about to overwrite the repo",
			got.Status.String())
	}
}

// TestInspect_PostFirstSyncDriftStillPendingPush pins the opposite
// side of the same coin: once LastSyncedSHA is populated for the
// active profile, drift means the user has made local edits since
// the last sync, which IS a pending push. Without this guard the
// first-sync-pull fix would swallow every subsequent local edit
// into a misleading "pending pull."
func TestInspect_PostFirstSyncDriftStillPendingPush(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, "home", ".claude")
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	repoPath := filepath.Join(tmp, "repo")

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile(filepath.Join(claudeDir, "CLAUDE.md"), "# Local edit after sync\n")
	writeFile(filepath.Join(repoPath, "profiles/default/claude/CLAUDE.md"), "# Previously-synced\n")
	writeFile(claudeJSON, `{}`)

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// LastSyncedSHA populated — a real post-sync edit.
	st := &state.State{
		ActiveProfile: "default",
		LastSyncedSHA: map[string]string{"default": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
	}
	v, err := Inspect(Inputs{
		Config: cfg, State: st,
		ClaudeDir: claudeDir, ClaudeJSON: claudeJSON, RepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	var got *Item
	for _, s := range v.Sections {
		for i := range s.Items {
			it := &s.Items[i]
			if it.Kind == KindClaudeMD {
				got = it
			}
		}
	}
	if got == nil {
		t.Fatal("expected a CLAUDE.md item")
	}
	if got.Status != StatusPendingPush {
		t.Errorf("CLAUDE.md status = %q, want pending push — the user has synced under this profile before and now has local edits, so the sync engine will push; rendering as pending pull would wrongly suggest remote is authoritative",
			got.Status.String())
	}
}

// TestInspect_InheritedExcludeShowsAsExcludedNotPendingPull pins
// the bug reported after the v0.8 "untrack via space" flow on a
// second machine: the child profile extends "default", default
// declares `exclude: [claude/skills/b/**]`, and skills/b exists in
// the repo (pushed before the exclude rule). Inspect should
// surface skills/b as StatusExcluded on the child profile since
// EffectiveProfile folds parent excludes into the child's rule
// set. Pre-fix, the excluded map was only populated from the
// local disk walk, so repo-only-yet-excluded paths fell through
// to StatusPendingPull and tempted the user into a sync that
// re-fought the exclusion the profile already said to honour —
// the "unpushed changes / conflicts" surface.
func TestInspect_InheritedExcludeShowsAsExcludedNotPendingPull(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, "home", ".claude")
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	repoPath := filepath.Join(tmp, "repo")

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Repo has default/skills/b/SKILL.md (excluded rule added later).
	writeFile(filepath.Join(repoPath, "profiles/default/claude/skills/b/SKILL.md"),
		"---\nname: beta\n---\n# body\n")
	// Local has nothing for this path — the file never reached disk
	// because the exclude rule on this machine's profile kept the
	// pull out.
	writeFile(claudeJSON, `{}`)

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["default"] = Profile(cfg.Profiles["default"], "", nil, []string{"claude/skills/b/**"})
	cfg.Profiles["laptop"] = Profile(Spec{}, "default", nil, nil)

	st := &state.State{ActiveProfile: "laptop", LastSyncedSHA: map[string]string{}}
	v, err := Inspect(Inputs{
		Config: cfg, State: st,
		ClaudeDir: claudeDir, ClaudeJSON: claudeJSON, RepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	var got *Item
	for _, s := range v.Sections {
		for i := range s.Items {
			it := &s.Items[i]
			if it.Title == "beta" {
				got = it
			}
		}
	}
	if got == nil {
		t.Fatal("expected the beta skill item to appear in the inspect view")
	}
	if got.Status != StatusExcluded {
		t.Errorf("beta status = %q, want excluded — the laptop profile inherits default's exclude of skills/b/**; the row should render as ☐ excluded instead of ☑ pending pull, otherwise pressing enter on the user's 'apply' action would drag in a file their profile declared they don't want",
			got.Status.String())
	}
}

// Profile + Spec are small test-only constructors that build
// config.ProfileSpec values without the test file having to import
// internal config types with verbose struct literals. Keeps the
// fixture readable.
type Spec = config.ProfileSpec

func Profile(base config.ProfileSpec, extends string, _ []string, excludePaths []string) config.ProfileSpec {
	out := base
	if extends != "" {
		out.Extends = extends
	}
	if len(excludePaths) > 0 {
		if out.Exclude == nil {
			out.Exclude = &config.ProfileExclude{}
		}
		out.Exclude.Paths = excludePaths
	}
	return out
}

// TestInspect_MCPServerRedactedSecretIsSynced pins the fix for
// v0.8.1's report that gemini-embedding stayed stuck at "pending
// push" even on a first-launch machine with no pending changes.
// Root cause: local claude.json keeps the raw GEMINI_API_KEY
// value, but the repo copy has the redacted placeholder
// "<<REDACTED:...>>" in its place. Byte-comparing the two sides
// always diverges on any redacted key, so the per-entry status
// pinned every server with a secret to pending-push.
//
// The fix runs the same jsonfilter.Apply the sync engine uses
// before comparing, so the redaction placeholder on both sides
// matches and the entry reads as synced.
func TestInspect_MCPServerRedactedSecretIsSynced(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, "home", ".claude")
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	repoPath := filepath.Join(tmp, "repo")

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Local has the real secret; repo has the placeholder the
	// redact rule writes.
	writeFile(claudeJSON,
		`{"mcpServers":{"gemini-embedding":{"command":"gemini-mcp","env":{"GEMINI_API_KEY":"real-secret-value"}}}}`)
	writeFile(filepath.Join(repoPath, "profiles/default/claude.json"),
		`{"mcpServers":{"gemini-embedding":{"command":"gemini-mcp","env":{"GEMINI_API_KEY":"<<REDACTED:ccsync:default:mcpServers.gemini-embedding.env.GEMINI_API_KEY>>"}}}}`)

	// Config must declare the redact rule for claude.json the way
	// the shipped default config does.
	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{ActiveProfile: "default", LastSyncedSHA: map[string]string{}}
	v, err := Inspect(Inputs{
		Config: cfg, State: st,
		ClaudeDir: claudeDir, ClaudeJSON: claudeJSON, RepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	var gotGemini *Item
	for _, s := range v.Sections {
		for i := range s.Items {
			it := &s.Items[i]
			if it.Kind == KindMCPServer && it.Title == "gemini-embedding" {
				gotGemini = it
			}
		}
	}
	if gotGemini == nil {
		t.Fatal("expected a gemini-embedding MCP server item")
	}
	if gotGemini.Status != StatusSynced {
		t.Errorf("gemini-embedding status = %q, want synced — the env.GEMINI_API_KEY drift between real-secret-local and redacted-placeholder-repo should resolve to equal once jsonfilter.Apply is applied to local first",
			gotGemini.Status.String())
	}
}

// TestInspect_SyncignoredRepoPathsDontGhost pins the fix for
// items marked permanently "pending pull" because the repo walk
// didn't apply the same .syncignore that local walk does. A user
// with a .syncignored directory (gemini-embedding/, research/)
// that happens to already exist in the repo would see every file
// under it stuck at pending-pull forever — the sync engine never
// pulls because the rule says "ignore," but Inspect did pull the
// bytes into its repo map and reported them as missing locally.
func TestInspect_SyncignoredRepoPathsDontGhost(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, "home", ".claude")
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	repoPath := filepath.Join(tmp, "repo")

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Syncignored directory present locally (ignored so discover
	// skips it) AND in the repo (historical artefact) — should NOT
	// produce a pending-pull item.
	writeFile(filepath.Join(claudeDir, "gemini-embedding/index.sqlite"), "local-bytes")
	writeFile(filepath.Join(repoPath, "profiles/default/claude/gemini-embedding/index.sqlite"), "repo-bytes")
	writeFile(claudeJSON, `{"mcpServers":{}}`)
	// An explicit repo-level .syncignore with gemini-embedding/.
	// The bundled defaults.yaml doesn't include it; this mirrors a
	// real user having added it to their repo's .syncignore after
	// a large local index was discovered.
	writeFile(filepath.Join(repoPath, ".syncignore"), "gemini-embedding/\n")

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{ActiveProfile: "default", LastSyncedSHA: map[string]string{}}
	v, err := Inspect(Inputs{
		Config: cfg, State: st,
		ClaudeDir: claudeDir, ClaudeJSON: claudeJSON, RepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if strings.Contains(it.Path, "gemini-embedding") {
				t.Errorf("item %q (%s) slipped through — gemini-embedding/ is in the default .syncignore; Inspect's repo walk should honour it the way sync.Run does, otherwise the user sees a permanent ghost pending-pull",
					it.Title, it.Path)
			}
		}
	}
}

// TestInspect_JSONFilteredFileCountsAsSyncedWhenOnlyExcludedKeysDiffer
// pins the fix for the settings.json false-positive: local has
// extra `permissions.allow` entries that the jsonFiles exclude
// rule keeps out of the repo, so `ccsync sync --dry-run` reports
// nothing to do — but Inspect compared raw bytes and flagged the
// file as pending push. After the fix, any file covered by a
// jsonFiles rule routes through the same jsonfilter.Apply the
// sync engine uses before comparison.
func TestInspect_JSONFilteredFileCountsAsSyncedWhenOnlyExcludedKeysDiffer(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, "home", ".claude")
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	repoPath := filepath.Join(tmp, "repo")

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Local settings.json has extra `permissions.allow` entries
	// that the default jsonFiles rule excludes from sync, plus a
	// baseline key that ALSO lives in the repo. The filtered
	// local — `{"permissions":{"defaultMode":"default"}}` — is
	// byte-equal to the repo copy, so Inspect should call it
	// synced even though the raw bytes diverge.
	writeFile(filepath.Join(claudeDir, "settings.json"),
		`{"permissions":{"allow":["Bash(foo:*)","Bash(bar:*)"],"defaultMode":"default"}}`)
	writeFile(filepath.Join(repoPath, "profiles/default/claude/settings.json"),
		`{
  "permissions": {
    "defaultMode": "default"
  }
}`)
	writeFile(claudeJSON, `{}`)

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{ActiveProfile: "default", LastSyncedSHA: map[string]string{}}
	v, err := Inspect(Inputs{
		Config: cfg, State: st,
		ClaudeDir: claudeDir, ClaudeJSON: claudeJSON, RepoPath: repoPath,
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	var gotSettings *Item
	for _, s := range v.Sections {
		for i := range s.Items {
			it := &s.Items[i]
			if strings.Contains(it.Path, "settings.json") && it.Kind != KindMCPServer {
				gotSettings = it
			}
		}
	}
	if gotSettings == nil {
		t.Fatal("expected a settings.json item")
	}
	if gotSettings.Status != StatusSynced {
		t.Errorf("settings.json status = %q, want synced — the only drift is in $.permissions.allow which the jsonFiles rule excludes from sync; Inspect should apply the same filter before comparing",
			gotSettings.Status.String())
	}
}

// TestInspect_EndToEnd — write a tiny claude dir + claude.json on
// disk, build ViewInputs, assert Sections come back with the shape
// the screen expects. One test covers the whole pipeline so
// downstream consumers stay honest.
func TestInspect_EndToEnd(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, "home", ".claude")
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	repoPath := filepath.Join(tmp, "repo")

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A skill (frontmatter) + a command (frontmatter) + an agent (H1
	// only) + CLAUDE.md + claude.json with mcp + settings.
	writeFile(filepath.Join(claudeDir, "skills/research-orchestration/SKILL.md"),
		"---\nname: research\ndescription: Multi-source research\n---\n\n# Body\n")
	writeFile(filepath.Join(claudeDir, "commands/ccsync-verify.md"),
		"---\nname: ccsync-verify\ndescription: Run the verify pipeline\n---\n")
	writeFile(filepath.Join(claudeDir, "agents/paper-reader.md"),
		"# Paper Reader\n\nDeeply read a single paper.\n")
	writeFile(filepath.Join(claudeDir, "CLAUDE.md"),
		"# ccsync\n\nThe repo's CLAUDE.md guide.\n")
	writeFile(claudeJSON,
		`{"theme":"dark","mcpServers":{"gemini":{"command":"gemini-mcp"}}}`)

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{
		ActiveProfile: "default",
		LastSyncedSHA: map[string]string{},
	}
	v, err := Inspect(Inputs{
		Config:     cfg,
		State:      st,
		ClaudeDir:  claudeDir,
		ClaudeJSON: claudeJSON,
		RepoPath:   repoPath,
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if v.Empty() {
		t.Fatal("expected non-empty view")
	}

	// Walk sections and assert each expected Kind is present.
	seen := map[Kind]string{}
	for _, s := range v.Sections {
		for _, it := range s.Items {
			seen[it.Kind] = it.Title
		}
	}
	wantKinds := []Kind{KindSkill, KindCommand, KindAgent, KindMCPServer, KindClaudeMD, KindSettings}
	for _, k := range wantKinds {
		if _, ok := seen[k]; !ok {
			t.Errorf("expected an Item of kind %q, got: %+v", k.String(), seen)
		}
	}
	// No repo tree yet → everything is pending push.
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.Status != StatusPendingPush {
				t.Errorf("expected pending-push for %q, got %q", it.Title, it.Status.String())
			}
		}
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !contains(haystack, n) {
			return false
		}
	}
	return true
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
