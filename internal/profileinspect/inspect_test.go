package profileinspect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/colinc86/ccsync/internal/config"
	cryptopkg "github.com/colinc86/ccsync/internal/crypto"
	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/state"
)

// syncSecretsKeyPassphrase is the keychain entry name sync.Run
// consults for the repo passphrase. Duplicated here as a const
// rather than imported from sync to avoid dragging the whole
// sync package into the inspect tests.
const syncSecretsKeyPassphrase = "repo-encryption-passphrase"

// TestExtractMarkdownMeta_Frontmatter pins the primary extraction
// path: a markdown file with YAML frontmatter containing name +
// description yields exactly those two strings.
func TestExtractMarkdownMeta_Frontmatter(t *testing.T) {
	body := []byte(`---
name: research
description: Multi-source research orchestration
---

# Body

Lorem ipsum.
`)
	title, desc := extractMarkdownMeta(body, "claude/skills/research/SKILL.md")
	if title != "research" {
		t.Errorf("title = %q, want research", title)
	}
	if desc != "Multi-source research orchestration" {
		t.Errorf("desc = %q, want Multi-source research orchestration", desc)
	}
}

// TestExtractMarkdownMeta_H1Fallback — no frontmatter; first H1 wins.
func TestExtractMarkdownMeta_H1Fallback(t *testing.T) {
	body := []byte(`# My Skill

Some body content.
`)
	title, _ := extractMarkdownMeta(body, "claude/skills/x/SKILL.md")
	if title != "My Skill" {
		t.Errorf("title = %q, want 'My Skill'", title)
	}
}

// TestExtractMarkdownMeta_FilenameFallback — no frontmatter, no H1;
// title falls through to the filename stem.
func TestExtractMarkdownMeta_FilenameFallback(t *testing.T) {
	body := []byte(`plain body, no heading.`)
	title, _ := extractMarkdownMeta(body, "claude/skills/my-thing/SKILL.md")
	if title != "my-thing" {
		t.Errorf("title = %q, want my-thing", title)
	}
}

// TestInspect_EndToEnd — the v0.9.0 surface. A populated ~/.claude/
// content tree plus a managed-MCP slice in ~/.claude.json yields the
// expected groups in the inspector view. No repo tree → everything
// pending push.
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

	seen := map[Kind]string{}
	for _, s := range v.Sections {
		for _, it := range s.Items {
			seen[it.Kind] = it.Title
		}
	}
	wantKinds := []Kind{KindSkill, KindCommand, KindAgent, KindMCPServer, KindClaudeMD}
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

// TestInspect_EncryptedRepoDecryptsBeforeCompare pins iter v0.8.7:
// when the repo is encrypted, Inspect must decrypt blobs before
// SHA-comparing against local. Pre-fix, Inspect compared plaintext
// local against ciphertext repo and reported every file as
// pending-push.
func TestInspect_EncryptedRepoDecryptsBeforeCompare(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, "home", ".claude")
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	repoPath := filepath.Join(tmp, "repo")

	t.Setenv("CCSYNC_SECRETS_BACKEND", "file")
	t.Setenv("CCSYNC_STATE_DIR", filepath.Join(tmp, ".ccsync"))

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	skillBody := "---\nname: research\ndescription: Multi-source research\n---\n\n# Body\n"
	writeFile(filepath.Join(claudeDir, "skills/research/SKILL.md"), skillBody)

	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	// Encrypt the repo: mark + write the skill blob as ciphertext.
	passphrase := "test-passphrase"
	if err := secrets.Store(syncSecretsKeyPassphrase, passphrase); err != nil {
		t.Fatalf("store passphrase: %v", err)
	}
	t.Cleanup(func() { _ = secrets.Delete(syncSecretsKeyPassphrase) })
	marker, err := cryptopkg.NewMarker()
	if err != nil {
		t.Fatalf("new marker: %v", err)
	}
	if err := cryptopkg.WriteMarker(repoPath, marker); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	key, err := marker.DeriveKey(passphrase)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	cipher, err := cryptopkg.Encrypt(key, []byte(skillBody))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	repoSkillPath := filepath.Join(repoPath, "profiles", "default", "claude", "skills", "research", "SKILL.md")
	writeFile(repoSkillPath, "")
	if err := os.WriteFile(repoSkillPath, cipher, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{
		ActiveProfile: "default",
		LastSyncedSHA: map[string]string{"default": "deadbeef"},
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
			if s.Items[i].Kind == KindSkill && s.Items[i].Title == "research" {
				got = &s.Items[i]
			}
		}
	}
	if got == nil {
		t.Fatal("expected a research skill item")
	}
	if got.Status != StatusSynced {
		t.Errorf("encrypted repo decrypt path: status = %q, want synced", got.Status.String())
	}
}
