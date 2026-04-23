package profileinspect

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	items := extractMCPServers(data, StatusSynced, "claude.json")
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	// Alphabetical by name.
	if items[0].Title != "gemini" {
		t.Errorf("first title = %q, want gemini", items[0].Title)
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
	items := extractMCPServers(data, StatusSynced, "claude.json")
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
