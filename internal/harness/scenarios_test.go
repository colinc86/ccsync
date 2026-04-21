package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/harness"
	"github.com/colinc86/ccsync/internal/secrets"
)

// writeStringFile is a tiny helper for scenarios that poke directly at
// the repo worktree (outside ccsync's normal code paths) to simulate
// legacy state or pre-seeded content.
func writeStringFile(absPath, content string) error {
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(absPath, []byte(content), 0o644)
}

// pushDirect commits-and-pushes the current worktree of repoPath with
// the given message — used when a scenario needs to land content on
// origin without going through sync.Run (e.g. to simulate legacy
// content from a pre-v0.3 repo).
func pushDirect(t *testing.T, repoPath, msg string) {
	t.Helper()
	r, err := gitx.Open(repoPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	if err := r.AddAll(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := r.Commit(msg, "test", "test@example.com"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := r.Push(context.Background(), nil); err != nil {
		t.Fatalf("push: %v", err)
	}
}

// TestScenarios is the cross-machine behavior suite. Each subtest is a
// single deterministic scenario that maps to a guarantee the tool should
// uphold. New cross-machine bugs should land as a failing subtest first,
// then the fix flips it green.
func TestScenarios(t *testing.T) {
	// --- Setup & bootstrap ---

	t.Run("bootstrap_empty_repo", func(t *testing.T) {
		s := harness.NewScenario(t)
		a := s.NewMachine("a").
			WriteClaudeFile("agents/foo.md", "agent body")
		a.Sync()
		s.AssertBareHasPath("profiles/default/claude/agents/foo.md")
	})

	t.Run("fresh_machine_joins_repo_with_agents", func(t *testing.T) {
		s := harness.NewScenario(t)
		s.NewMachine("a").
			WriteClaudeFile("agents/foo.md", "agent body").
			Sync()
		b := s.NewMachine("b")
		b.Sync()
		b.AssertClaudeFile("agents/foo.md", "agent body")
	})

	t.Run("missing_local_claude_json_pulls_inherited", func(t *testing.T) {
		// Work extends default. Default pushes a claude.json; work on a
		// fresh home (no ~/.claude.json) should pull it, not DeleteRemote.
		s := harness.NewScenario(t, harness.WithProfiles(map[string]config.ProfileSpec{
			"work": {Extends: "default"},
		}))
		s.NewMachine("a").
			WriteClaudeJSON(`{"theme":"dark","mcpServers":{"m":{"command":"x"}}}`).
			Sync()
		b := s.NewMachine("b").UseProfile("work")
		b.Sync()
		b.AssertClaudeJSONKey("theme", "dark")
	})

	// --- Basic round-trip ---

	t.Run("theme_change_propagates", func(t *testing.T) {
		s := harness.NewScenario(t)
		a := s.NewMachine("a").WriteClaudeJSON(`{"theme":"dark"}`)
		a.Sync()
		b := s.NewMachine("b")
		b.Sync()
		b.AssertClaudeJSONKey("theme", "dark")

		a.WriteClaudeJSONKey("theme", "light").Sync()
		b.Sync()
		b.AssertClaudeJSONKey("theme", "light")
	})

	t.Run("new_mcp_server_with_env_secret_roundtrips", func(t *testing.T) {
		s := harness.NewScenario(t)
		a := s.NewMachine("a").WriteClaudeJSON(`{
			"mcpServers": {"gemini": {"command":"gemini-mcp", "env":{"GEMINI_API_KEY":"real-secret"}}}
		}`)
		a.Sync()

		// Repo should carry a placeholder, not the real secret.
		blob, ok := s.BareFile("profiles/default/claude.json")
		if !ok {
			t.Fatal("bare missing claude.json")
		}
		if strings.Contains(string(blob), "real-secret") {
			t.Errorf("plaintext secret leaked into repo")
		}
		if !strings.Contains(string(blob), "<<REDACTED") {
			t.Errorf("expected placeholder, got: %s", blob)
		}

		// The keychain mock is shared — machine B's sync restores the secret.
		b := s.NewMachine("b")
		b.Sync()
		got := b.ClaudeJSONMap()
		env := got["mcpServers"].(map[string]any)["gemini"].(map[string]any)["env"].(map[string]any)
		if env["GEMINI_API_KEY"] != "real-secret" {
			t.Errorf("secret not restored; got %v", env["GEMINI_API_KEY"])
		}
	})

	t.Run("new_agent_propagates", func(t *testing.T) {
		s := harness.NewScenario(t)
		s.NewMachine("a").WriteClaudeFile("agents/research.md", "go deep").Sync()
		b := s.NewMachine("b")
		b.Sync()
		b.AssertClaudeFile("agents/research.md", "go deep")
	})

	t.Run("new_skill_propagates", func(t *testing.T) {
		s := harness.NewScenario(t)
		s.NewMachine("a").WriteClaudeFile("skills/summarize/SKILL.md", "summarize").Sync()
		b := s.NewMachine("b")
		b.Sync()
		b.AssertClaudeFile("skills/summarize/SKILL.md", "summarize")
	})

	t.Run("new_command_propagates", func(t *testing.T) {
		s := harness.NewScenario(t)
		s.NewMachine("a").WriteClaudeFile("commands/foo.md", "Run foo").Sync()
		b := s.NewMachine("b")
		b.Sync()
		b.AssertClaudeFile("commands/foo.md", "Run foo")
	})

	// --- User-local state preservation (the v0.4.0 headline fix) ---

	t.Run("oauthAccount_not_clobbered_on_pull", func(t *testing.T) {
		// Classic bug: A has oauthAccount=X, B has oauthAccount=Y. A
		// pushes theme change. B pulls. Before the fix, B's oauthAccount
		// was wiped. After the fix, B keeps Y while picking up A's theme.
		s := harness.NewScenario(t)
		a := s.NewMachine("a").WriteClaudeJSON(`{
			"theme":"dark",
			"oauthAccount":{"userId":"a-user","email":"a@example.com"}
		}`)
		a.Sync()

		b := s.NewMachine("b").WriteClaudeJSON(`{
			"theme":"dark",
			"oauthAccount":{"userId":"b-user","email":"b@example.com"}
		}`)
		b.Sync() // first sync from b; should not push oauth either

		// A changes theme, pushes.
		a.WriteClaudeJSONKey("theme", "light").Sync()

		// B pulls. oauthAccount must survive.
		b.Sync()
		b.AssertClaudeJSONKey("theme", "light")
		b.AssertClaudeJSONKey("oauthAccount.userId", "b-user")
		b.AssertClaudeJSONKey("oauthAccount.email", "b@example.com")

		// And oauthAccount must never leave the machine (not in repo).
		blob, _ := s.BareFile("profiles/default/claude.json")
		if strings.Contains(string(blob), "a-user") || strings.Contains(string(blob), "b-user") {
			t.Errorf("oauthAccount leaked into repo: %s", blob)
		}
	})

	t.Run("userID_not_clobbered_on_pull", func(t *testing.T) {
		s := harness.NewScenario(t)
		s.NewMachine("a").
			WriteClaudeJSON(`{"theme":"dark","userID":"user-a"}`).
			Sync()
		b := s.NewMachine("b").
			WriteClaudeJSON(`{"theme":"dark","userID":"user-b"}`)
		b.Sync()
		b.AssertClaudeJSONKey("userID", "user-b")
	})

	t.Run("permissions_allow_stays_local", func(t *testing.T) {
		// settings.json has permissions.allow excluded; two machines with
		// different allow-lists must not cross-contaminate after sync.
		s := harness.NewScenario(t)
		a := s.NewMachine("a").WriteClaudeFile("settings.json", `{
			"theme":"dark",
			"permissions":{"allow":["Bash(a1)","Bash(a2)"]}
		}`)
		a.Sync()

		b := s.NewMachine("b").WriteClaudeFile("settings.json", `{
			"theme":"dark",
			"permissions":{"allow":["Bash(b1)"]}
		}`)
		b.Sync()

		// B's local settings.json must still carry b1 only.
		got, _ := b.ReadClaudeFile("settings.json")
		if !strings.Contains(got, "Bash(b1)") {
			t.Errorf("b's permissions lost: %s", got)
		}
		if strings.Contains(got, "Bash(a1)") || strings.Contains(got, "Bash(a2)") {
			t.Errorf("a's permissions leaked to b: %s", got)
		}
		// Repo must not carry either machine's permissions.
		blob, _ := s.BareFile("profiles/default/claude/settings.json")
		if strings.Contains(string(blob), "Bash(a1)") || strings.Contains(string(blob), "Bash(b1)") {
			t.Errorf("permissions leaked into repo: %s", blob)
		}
	})

	t.Run("projects_field_excluded_from_sync", func(t *testing.T) {
		s := harness.NewScenario(t)
		a := s.NewMachine("a").WriteClaudeJSON(`{
			"theme":"dark",
			"projects":{"/Users/alice/p1":{"session":"abc"}}
		}`)
		a.Sync()

		blob, _ := s.BareFile("profiles/default/claude.json")
		if strings.Contains(string(blob), "Users/alice") {
			t.Errorf("projects leaked: %s", blob)
		}

		// B syncs, confirms projects field didn't arrive.
		b := s.NewMachine("b").WriteClaudeJSON(`{"theme":"dark","projects":{"/home/bob/p":{}}}`)
		b.Sync()
		b.AssertClaudeJSONKey("projects", map[string]any{"/home/bob/p": map[string]any{}})
	})

	t.Run("statsig_cache_excluded", func(t *testing.T) {
		s := harness.NewScenario(t)
		a := s.NewMachine("a").WriteClaudeJSON(`{
			"theme":"dark",
			"cachedStatsigGates":{"gate_a":{"val":true}}
		}`)
		a.Sync()
		blob, _ := s.BareFile("profiles/default/claude.json")
		if strings.Contains(string(blob), "gate_a") {
			t.Errorf("statsig leaked: %s", blob)
		}
	})

	// --- Redaction ---

	t.Run("missing_secret_blocks_local_write", func(t *testing.T) {
		s := harness.NewScenario(t)
		// A pushes with a secret in keychain; then we wipe the keychain,
		// simulating a B that doesn't have access to the secret.
		a := s.NewMachine("a").WriteClaudeJSON(`{
			"mcpServers":{"svc":{"env":{"API_KEY":"value"}}}
		}`)
		a.Sync()
		secrets.MockInit() // wipe keychain

		b := s.NewMachine("b")
		res := b.Sync()
		if len(res.MissingSecrets) == 0 {
			t.Errorf("expected MissingSecrets to be populated; got %v", res)
		}
		// B must NOT have written ~/.claude.json with dangling placeholder.
		if _, ok := b.ReadClaudeFile("../../.claude.json"); ok {
			// technically we could read the original without the write;
			// assert no "<<REDACTED" in whatever's there
			raw := b.ClaudeJSONRaw()
			if strings.Contains(string(raw), "<<REDACTED") {
				t.Errorf("placeholder written to local without restore: %s", raw)
			}
		}
	})

	// --- Conflicts ---

	t.Run("text_conflict_take_local_pushes_local_version", func(t *testing.T) {
		s := harness.NewScenario(t)
		s.NewMachine("a").WriteClaudeFile("agents/shared.md", "v1").Sync()

		// Both sides modify shared.md after diverging.
		a := s.NewMachine("a2").WriteClaudeFile("agents/shared.md", "v1")
		a.Sync() // a2 picks up v1 as its base
		b := s.NewMachine("b").WriteClaudeFile("agents/shared.md", "v1")
		b.Sync()

		a.WriteClaudeFile("agents/shared.md", "a version").Sync()
		b.WriteClaudeFile("agents/shared.md", "b version")
		// b now has a merge-conflict pending with a's push.
		res := b.SyncAndResolveAll(harness.TakeLocal)
		_ = res
		b.AssertClaudeFile("agents/shared.md", "b version")

		// a then pulls; picks up b's resolution.
		a.Sync()
		a.AssertClaudeFile("agents/shared.md", "b version")
	})

	// --- Profiles ---

	t.Run("work_extends_default_inherits_agents", func(t *testing.T) {
		s := harness.NewScenario(t, harness.WithProfiles(map[string]config.ProfileSpec{
			"work": {Extends: "default"},
		}))
		s.NewMachine("a").
			WriteClaudeFile("agents/shared.md", "inherited").
			Sync()
		b := s.NewMachine("b").UseProfile("work")
		b.Sync()
		b.AssertClaudeFile("agents/shared.md", "inherited")
	})

	t.Run("work_profile_exclude_blocks_pull", func(t *testing.T) {
		s := harness.NewScenario(t, harness.WithProfiles(map[string]config.ProfileSpec{
			"work": {
				Extends: "default",
				Exclude: &config.ProfileExclude{Paths: []string{"claude/agents/secret.md"}},
			},
		}))
		s.NewMachine("a").WriteClaudeFile("agents/secret.md", "top secret").Sync()
		b := s.NewMachine("b").UseProfile("work")
		b.Sync()
		b.AssertNoClaudeFile("agents/secret.md")
	})

	// --- Stale-exclude GC ---

	t.Run("stale_syncignore_silently_gcs_repo_entry", func(t *testing.T) {
		s := harness.NewScenario(t)
		// Machine A seeds the repo normally.
		a := s.NewMachine("a").WriteClaudeFile("agents/keeper.md", "keep")
		a.Sync()
		// Now stash a legacy file under an IGNORED subtree directly in the
		// repo worktree — simulating pre-v0.3 content now covered by the
		// widened .syncignore.
		legacyLocal := a.RepoPath + "/profiles/default/claude/projects/legacy.md"
		if err := writeStringFile(legacyLocal, "legacy"); err != nil {
			t.Fatal(err)
		}
		// commit+push via another sync (which happens to add nothing; we
		// commit directly here to land the legacy file on origin).
		pushDirect(t, a.RepoPath, "add legacy")
		// Next sync on A should silently DeleteRemote the legacy path.
		a.Sync()
		s.AssertBareNoPath("profiles/default/claude/projects/legacy.md")
	})

	t.Run("claude_md_concurrent_appends_surface_conflict", func(t *testing.T) {
		// Two machines append to CLAUDE.md. The current text merge is
		// hunk-based and both sides "modified" the same tail chunk (the
		// EOF region), so this surfaces as a conflict, not a clean merge.
		// Recording this as the current contract — if we someday ship
		// append-aware merging, flip the expectation to "no conflicts"
		// and this test proves the new behavior.
		s := harness.NewScenario(t)
		s.NewMachine("seed").WriteClaudeFile("CLAUDE.md", "# base\n\nshared line\n").Sync()
		a := s.NewMachine("a")
		a.Sync()
		b := s.NewMachine("b")
		b.Sync()

		a.WriteClaudeFile("CLAUDE.md", "# base\n\nshared line\n\n## from a\nnote-a\n").Sync()
		b.WriteClaudeFile("CLAUDE.md", "# base\n\nshared line\n\n## from b\nnote-b\n")

		// Resolve: take-local on b keeps b's tail; then a pulls b's resolution.
		b.SyncAndResolveAll(harness.TakeLocal)
		got, _ := b.ReadClaudeFile("CLAUDE.md")
		if !strings.Contains(got, "note-b") {
			t.Errorf("take-local on b lost note-b: %s", got)
		}
	})

	t.Run("json_theme_conflict_take_remote", func(t *testing.T) {
		// Both sides change theme; resolve "take remote" on b so a's
		// value wins.
		s := harness.NewScenario(t)
		s.NewMachine("seed").WriteClaudeJSON(`{"theme":"dark"}`).Sync()
		a := s.NewMachine("a")
		a.Sync()
		b := s.NewMachine("b")
		b.Sync()

		a.WriteClaudeJSONKey("theme", "light").Sync()
		b.WriteClaudeJSONKey("theme", "hc-dark")
		b.SyncAndResolveAll(harness.TakeRemote)
		b.AssertClaudeJSONKey("theme", "light")
	})

	t.Run("work_profile_exclude_blocks_push", func(t *testing.T) {
		// Work creates a path locally that its profile excludes; after
		// sync the path must not appear in the repo.
		s := harness.NewScenario(t, harness.WithProfiles(map[string]config.ProfileSpec{
			"work": {
				Exclude: &config.ProfileExclude{Paths: []string{"claude/agents/work-only.md"}},
			},
		}))
		a := s.NewMachine("a").UseProfile("work").
			WriteClaudeFile("agents/work-only.md", "local-only content")
		a.Sync()
		s.AssertBareNoPath("profiles/work/claude/agents/work-only.md")
	})

	t.Run("cross_profile_secret_restore", func(t *testing.T) {
		// work extends default. Default pushes a secret; work pulls and
		// restores from the default-scoped keychain entry.
		s := harness.NewScenario(t, harness.WithProfiles(map[string]config.ProfileSpec{
			"work": {Extends: "default"},
		}))
		s.NewMachine("a").WriteClaudeJSON(`{
			"mcpServers":{"svc":{"env":{"API_KEY":"shared-secret"}}}
		}`).Sync()

		b := s.NewMachine("b").UseProfile("work")
		b.Sync()
		got := b.ClaudeJSONMap()
		env := got["mcpServers"].(map[string]any)["svc"].(map[string]any)["env"].(map[string]any)
		if env["API_KEY"] != "shared-secret" {
			t.Errorf("cross-profile restore failed; got %v", env["API_KEY"])
		}
	})

	// --- Per-machine denials ---

	t.Run("denied_path_stays_off_repo", func(t *testing.T) {
		// Machine a creates a command locally, denies it, then syncs.
		// The command must stay on disk but never reach the repo — no
		// DeleteRemote, no push, just silent skip. Proves DeniedPaths is
		// wired into the sync engine like profile excludes.
		s := harness.NewScenario(t)
		a := s.NewMachine("a").
			WriteClaudeFile("commands/work-only.md", "local-only").
			DenyPath("claude/commands/work-only.md")
		a.Sync()
		s.AssertBareNoPath("profiles/default/claude/commands/work-only.md")
		// The local file should still exist on disk.
		if got, ok := a.ReadClaudeFile("commands/work-only.md"); !ok || got != "local-only" {
			t.Errorf("local file should still exist: ok=%v, got=%q", ok, got)
		}
	})

	t.Run("denied_path_blocks_pull_too", func(t *testing.T) {
		// A pushes agents/shared.md. B denies that path before syncing.
		// B's sync must not create the file locally.
		s := harness.NewScenario(t)
		s.NewMachine("a").WriteClaudeFile("agents/shared.md", "body").Sync()
		b := s.NewMachine("b").DenyPath("claude/agents/shared.md")
		b.Sync()
		b.AssertNoClaudeFile("agents/shared.md")
	})

	// --- Auto behavior ---

	t.Run("clean_sync_produces_no_commit", func(t *testing.T) {
		s := harness.NewScenario(t)
		a := s.NewMachine("a").WriteClaudeFile("agents/foo.md", "body")
		a.Sync()
		before := len(s.BareCommits())
		a.Sync() // nothing changed
		after := len(s.BareCommits())
		if before != after {
			t.Errorf("no-op sync produced a commit: %d → %d", before, after)
		}
	})
}
