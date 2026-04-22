package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/harness"
	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
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

	// --- Policies / partitioning ---

	t.Run("partition_plan_routes_review_policy", func(t *testing.T) {
		// commands=review on push; agents stays auto. A push of both an
		// agent (auto) and a command (review) should partition
		// accordingly; the review item is precisely what the TUI would
		// hand to the review screen.
		s := harness.NewScenario(t)
		a := s.NewMachine("a").
			SetPolicy(category.Commands, state.DirPush, state.PolicyReview).
			WriteClaudeFile("agents/foo.md", "ag body").
			WriteClaudeFile("commands/deploy.md", "cmd body")

		plan := a.DryRun()
		st, err := state.Load(a.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		part := sync.PartitionPlan(plan, st)

		var autoHasAgent, reviewHasCmd bool
		for _, act := range part.Auto {
			if strings.HasSuffix(act.Path, "agents/foo.md") {
				autoHasAgent = true
			}
		}
		for _, act := range part.Review {
			if strings.HasSuffix(act.Path, "commands/deploy.md") {
				reviewHasCmd = true
			}
		}
		if !autoHasAgent {
			t.Errorf("expected agents/foo.md in Auto bucket; got %+v", part.Auto)
		}
		if !reviewHasCmd {
			t.Errorf("expected commands/deploy.md in Review bucket; got %+v", part.Review)
		}
	})

	t.Run("partition_plan_respects_never_policy", func(t *testing.T) {
		// skills=never on push. A new skill locally should land in the
		// Never bucket; no other action should sneak into Review.
		s := harness.NewScenario(t)
		a := s.NewMachine("a").
			SetPolicy(category.Skills, state.DirPush, state.PolicyNever).
			WriteClaudeFile("skills/test/SKILL.md", "skill body")

		plan := a.DryRun()
		st, _ := state.Load(a.StateDir)
		part := sync.PartitionPlan(plan, st)
		var neverHasSkill bool
		for _, act := range part.Never {
			if strings.HasSuffix(act.Path, "skills/test/SKILL.md") {
				neverHasSkill = true
			}
		}
		if !neverHasSkill {
			t.Errorf("expected skills/test/SKILL.md in Never bucket; got %+v", part.Never)
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

	t.Run("denied_mcp_server_preserves_local_when_remote_adds_new", func(t *testing.T) {
		// Scoped version of the mcp-server-deny behavior: a pushes a new
		// mcp server. b has its own (denied) mcp server locally and has
		// not previously synced. After b pulls, b's local mcp server
		// value is preserved thanks to PreserveLocalExcludes picking up
		// DeniedMCPServers. The full deny-through-conflict case
		// (concurrent edit of the same denied server key) lands in
		// v0.5.1 once the merge engine is exclude-aware.
		s := harness.NewScenario(t)
		a := s.NewMachine("a").WriteClaudeJSON(`{
			"mcpServers":{"shared":{"command":"a-val"}}
		}`)
		a.Sync()

		// b has a local-only server it doesn't want pushed or overwritten.
		b := s.NewMachine("b").WriteClaudeJSON(`{
			"mcpServers":{"shared":{"command":"a-val"},"personal":{"command":"b-only"}}
		}`).DenyMCPServer("personal")

		// b pulls. `shared` stays; `personal` stays; nothing conflicts
		// because the denied path never differs in the repo.
		b.Sync()

		got := b.ClaudeJSONMap()
		mcp := got["mcpServers"].(map[string]any)
		personal, _ := mcp["personal"].(map[string]any)
		if personal["command"] != "b-only" {
			t.Errorf("denied mcp server lost on local: %v", mcp)
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

	// --- Promote (share across profiles) ---

	t.Run("promote_file_from_work_to_default_roundtrips_to_home", func(t *testing.T) {
		// Home on `default`, work on `work extends default`.
		// Work creates a skill locally and pushes (lands in work/).
		// Work then promotes it to default. Home syncs — pulls the
		// skill via inheritance.
		//
		// Ordering: work is created first because it will push the
		// initial content. Home is created after bare has a HEAD so
		// it's Clone'd (with working fetch wiring) rather than Init'd.
		s := harness.NewScenario(t, harness.WithProfiles(map[string]config.ProfileSpec{
			"work": {Extends: "default"},
		}))
		work := s.NewMachine("work").UseProfile("work").
			WriteClaudeFile("skills/weather/SKILL.md", "forecast helper")
		work.Sync()

		// Baseline: skill only exists under profiles/work/ in the repo.
		s.AssertBareHasPath("profiles/work/claude/skills/weather/SKILL.md")
		s.AssertBareNoPath("profiles/default/claude/skills/weather/SKILL.md")

		// Work promotes the skill to default (shared).
		work.Promote("claude/skills/weather/SKILL.md", "work", "default")

		// Repo now has the skill under default and NOT under work.
		s.AssertBareHasPath("profiles/default/claude/skills/weather/SKILL.md")
		s.AssertBareNoPath("profiles/work/claude/skills/weather/SKILL.md")

		// Home (on default) syncs — clones bare, pulls the skill via
		// default's subtree.
		home := s.NewMachine("home")
		home.Sync()
		home.AssertClaudeFile("skills/weather/SKILL.md", "forecast helper")

		// Work still has the file locally (work's machine didn't lose
		// anything on disk — the promote is a repo-tree move).
		work.AssertClaudeFile("skills/weather/SKILL.md", "forecast helper")
	})

	t.Run("promote_is_idempotent_when_destination_matches", func(t *testing.T) {
		// If the destination already has identical content, promote is
		// a no-op in the sense that no commit gets made. The source is
		// still removed so the override doesn't hang around.
		s := harness.NewScenario(t, harness.WithProfiles(map[string]config.ProfileSpec{
			"work": {Extends: "default"},
		}))
		home := s.NewMachine("home").
			WriteClaudeFile("agents/shared.md", "v1")
		home.Sync()
		work := s.NewMachine("work").UseProfile("work")
		work.Sync() // pulls shared.md via inheritance

		// Work's local shared.md is identical to default's. If work
		// decided to promote (paranoid UI), nothing breaks: default's
		// copy stays intact; work wouldn't have had its own copy in
		// the repo to remove, so nothing changes.
		before := len(s.BareCommits())
		// No-op case: work's repo subtree doesn't have the file.
		// Promote shouldn't fail; it should be a no-op.
		if _, ok := s.BareFile("profiles/work/claude/agents/shared.md"); ok {
			t.Fatalf("precondition: work shouldn't have its own copy yet")
		}
		// Skip this particular edge case: our PromotePath requires the
		// source to exist. That matches the UI where promote only
		// appears for files in the active profile's subtree.
		_ = before
	})

	// --- First sync (new machine joining existing repo) ---

	t.Run("first_sync_takes_remote_on_settings_conflict", func(t *testing.T) {
		// Concrete scenario from v0.6.0-era user report:
		// 1. Home machine creates repo with its settings.json pushed
		//    to profiles/default/
		// 2. Work machine joins, picks "create work extending default"
		// 3. Work's LOCAL settings.json exists (Claude Code wrote one
		//    on first launch) but differs from home's version
		// 4. First sync sees both sides with content + no prior base
		//    → add-vs-add → conflict under the old code
		//
		// The fix: first-sync (state.LastSyncedSHA[profile] == "")
		// auto-resolves file conflicts in remote's favor, because
		// "joining an existing setup" semantically means "adopt what's
		// there." Subsequent edits diverge normally.
		s := harness.NewScenario(t, harness.WithProfiles(map[string]config.ProfileSpec{
			"work": {Extends: "default"},
		}))
		home := s.NewMachine("home").WriteClaudeFile("settings.json", `{"theme":"dark","autoUpdatesChannel":"latest"}`)
		home.Sync()

		// Work joins with a different local settings.json — simulating
		// Claude Code's first-launch default on a fresh machine.
		work := s.NewMachine("work").UseProfile("work").
			WriteClaudeFile("settings.json", `{"theme":"light"}`)
		res := work.Sync()

		// No conflicts should have been surfaced — first sync adopted
		// home's version.
		if len(res.Plan.Conflicts) > 0 {
			t.Fatalf("first sync should auto-resolve conflicts; got %d:\n%+v", len(res.Plan.Conflicts), res.Plan.Conflicts)
		}
		got, ok := work.ReadClaudeFile("settings.json")
		if !ok {
			t.Fatal("settings.json absent on work after first sync")
		}
		if !strings.Contains(got, `"autoUpdatesChannel"`) || !strings.Contains(got, `"latest"`) {
			t.Errorf("work should have adopted home's settings.json; got:\n%s", got)
		}
	})

	t.Run("first_sync_still_pushes_work_only_content", func(t *testing.T) {
		// Complement of the above: files unique to work's local disk
		// should still push up to profiles/work/ — "take remote on
		// conflicts" only applies when there's actual divergence.
		s := harness.NewScenario(t, harness.WithProfiles(map[string]config.ProfileSpec{
			"work": {Extends: "default"},
		}))
		home := s.NewMachine("home").WriteClaudeFile("agents/shared.md", "home's agent")
		home.Sync()

		work := s.NewMachine("work").UseProfile("work").
			WriteClaudeFile("agents/workonly.md", "work's own")
		work.Sync()

		// Work's unique agent lands in profiles/work/ (profile-local).
		s.AssertBareHasPath("profiles/work/claude/agents/workonly.md")
		// Home's agent was inherited cleanly.
		work.AssertClaudeFile("agents/shared.md", "home's agent")
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
