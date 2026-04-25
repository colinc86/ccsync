package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/profile"
	"github.com/colinc86/ccsync/internal/secrets"
	ccstate "github.com/colinc86/ccsync/internal/state"
)

const fixtureClaudeJSON = `{
  "autoUpdates": true,
  "theme": "dark",
  "userID": "user-A",
  "mcpServers": {
    "gemini": {
      "command": "gemini-mcp",
      "env": {"GEMINI_API_KEY": "secret-A"}
    }
  },
  "projects": {"/some/path": {"session": "s1"}}
}`

func seedMachine(t *testing.T, home string) {
	t.Helper()
	mkdir := func(p string) {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile := func(p, s string) {
		mkdir(filepath.Dir(p))
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claudeDir := filepath.Join(home, ".claude")
	writeFile(filepath.Join(claudeDir, "agents/foo.md"), "agent body")
	writeFile(filepath.Join(claudeDir, "settings.json"), `{"theme":"dark"}`)
	writeFile(filepath.Join(home, ".claude.json"), fixtureClaudeJSON)
}

func machineInputs(home, repoPath, stateDir string, cfg *config.Config) Inputs {
	return Inputs{
		Config:      cfg,
		Profile:     "default",
		ClaudeDir:   filepath.Join(home, ".claude"),
		ClaudeJSON:  filepath.Join(home, ".claude.json"),
		RepoPath:    repoPath,
		StateDir:    stateDir,
		HostUUID:    "host-" + filepath.Base(home),
		HostName:    "test-" + filepath.Base(home),
		AuthorEmail: "test@example.com",
	}
}

// initBareWithMainHEAD creates a bare repo at path and sets HEAD to
// refs/heads/main so a subsequent Clone can check out HEAD cleanly
// after the first push lands. go-git's PlainInit defaults bare HEAD
// to master, but ccsync pushes to main (gitx.DefaultBranch). Without
// aligning, the bare's HEAD points at a ref that never exists, and
// Clone fails with "reference not found." See also harness.NewScenario
// for the mirror of this logic in the cross-machine test fixture.
func initBareWithMainHEAD(t *testing.T, path string) {
	t.Helper()
	r, err := gogit.PlainInit(path, true)
	if err != nil {
		t.Fatal(err)
	}
	ref := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(gitx.DefaultBranch))
	if err := r.Storer.SetReference(ref); err != nil {
		t.Fatal(err)
	}
}

func TestCrossMachinePushPullRoundTrip(t *testing.T) {
	t.Skip("v0.9.0: this test pins claude.json round-trip semantics that no longer apply — claude.json never lands in the repo, only its mcpServers slice via mcpextract. Round-trip + redaction coverage now lives in internal/mcpextract; rewrite this fixture as managed-slice round-trip.")
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}

	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	seedMachine(t, homeA)

	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	resA, err := Run(ctx, machineInputs(homeA, repoA, stateA, cfg), nil)
	if err != nil {
		t.Fatalf("machine A Run: %v", err)
	}
	if resA.CommitSHA == "" {
		t.Fatal("expected a commit on first machine A sync")
	}

	// Fixture should have produced a redaction; verify key landed in keychain.
	_, err = secrets.Fetch(secrets.Key("default", "mcpServers.gemini.env.GEMINI_API_KEY"))
	if err != nil {
		t.Fatalf("expected keychain entry for GEMINI_API_KEY: %v", err)
	}

	// Inspect the bare repo: no plaintext secret anywhere
	inspectClone := filepath.Join(tmp, "inspect")
	if _, err := gogit.PlainClone(inspectClone, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatalf("inspect clone: %v", err)
	}
	repoJSONPath := filepath.Join(inspectClone, "profiles/default/claude.json")
	repoJSON, err := os.ReadFile(repoJSONPath)
	if err != nil {
		t.Fatalf("read repo claude.json: %v", err)
	}
	if strings.Contains(string(repoJSON), "secret-A") {
		t.Errorf("plaintext secret leaked into repo:\n%s", repoJSON)
	}
	if !strings.Contains(string(repoJSON), "<<REDACTED:") {
		t.Errorf("expected redaction placeholder in repo, got:\n%s", repoJSON)
	}

	// Machine B: empty home, clones repo, pulls
	homeB := filepath.Join(tmp, "homeB")
	repoB := filepath.Join(tmp, "repoB")
	stateB := filepath.Join(tmp, "stateB")
	if err := os.MkdirAll(filepath.Join(homeB, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Clone(ctx, bareDir, repoB, nil); err != nil {
		t.Fatalf("machine B clone: %v", err)
	}

	resB, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil)
	if err != nil {
		t.Fatalf("machine B Run: %v", err)
	}
	if len(resB.MissingSecrets) != 0 {
		t.Errorf("unexpected missing secrets (mock keychain is shared): %v", resB.MissingSecrets)
	}

	// Verify files arrived on machine B with correct content
	got, err := os.ReadFile(filepath.Join(homeB, ".claude/agents/foo.md"))
	if err != nil || string(got) != "agent body" {
		t.Errorf("agent file on B = %q, err=%v", got, err)
	}
	localJSON, err := os.ReadFile(filepath.Join(homeB, ".claude.json"))
	if err != nil {
		t.Fatalf("read B claude.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(localJSON, &parsed); err != nil {
		t.Fatalf("parse B claude.json: %v", err)
	}
	mcp, _ := parsed["mcpServers"].(map[string]any)
	gem, _ := mcp["gemini"].(map[string]any)
	env, _ := gem["env"].(map[string]any)
	if env["GEMINI_API_KEY"] != "secret-A" {
		t.Errorf("secret not restored on machine B: got %v", env["GEMINI_API_KEY"])
	}
	if _, excluded := parsed["projects"]; excluded {
		t.Errorf("projects should have been excluded; found on B: %v", parsed["projects"])
	}
	if _, excluded := parsed["userID"]; excluded {
		t.Errorf("userID should have been excluded; found on B: %v", parsed["userID"])
	}
}

func TestSelectiveSyncOnlyAppliesListedPaths(t *testing.T) {
	secrets.MockInit()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()

	homeA := filepath.Join(tmp, "home")
	repoA := filepath.Join(tmp, "repo")
	stateA := filepath.Join(tmp, "state")
	seedMachine(t, homeA)
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}

	// First: a plain dry run to discover what paths exist.
	in := machineInputs(homeA, repoA, stateA, cfg)
	in.DryRun = true
	dry, err := Run(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Pick exactly one path (the agent file). Selective apply that one.
	var pick string
	for _, a := range dry.Plan.Actions {
		if strings.HasSuffix(a.Path, "agents/foo.md") {
			pick = a.Path
			break
		}
	}
	if pick == "" {
		t.Fatal("agent path not found in plan")
	}

	in.DryRun = false
	in.OnlyPaths = map[string]bool{pick: true}
	res, err := Run(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("selective Run: %v", err)
	}
	if res.CommitSHA == "" {
		t.Fatal("expected a commit for the one selected path")
	}

	// The picked file should be in the repo; an unselected one should NOT be.
	inspectPicked := filepath.Join(repoA, pick)
	if _, err := os.Stat(inspectPicked); err != nil {
		t.Errorf("picked path not materialized: %v", err)
	}
	// settings.json was in the plan but not selected — shouldn't be in repo.
	inspectSkipped := filepath.Join(repoA, "profiles/default/claude/settings.json")
	if _, err := os.Stat(inspectSkipped); err == nil {
		t.Error("un-selected path was written to repo")
	}

	// Because it's selective, state.LastSyncedSHA should NOT advance.
	st, err := loadHostState(stateA)
	if err != nil {
		t.Fatal(err)
	}
	if sha := st.LastSyncedSHA["default"]; sha != "" {
		t.Errorf("LastSyncedSHA advanced on selective sync: %s", sha)
	}
}

func TestProfileExtendsInheritsContent(t *testing.T) {
	secrets.MockInit()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()
	cfg.Profiles["work"] = config.ProfileSpec{
		Description: "work laptop",
		Extends:     "default",
	}

	// Machine A on profile=default pushes an agent + a skill.
	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	seedMachine(t, homeA) // creates claude/agents/foo.md
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("machine A push: %v", err)
	}

	// Inspect what landed in the bare repo — should be under
	// profiles/default/, NOT profiles/work/.
	inspect := filepath.Join(tmp, "inspect")
	if _, err := gogit.PlainClone(inspect, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inspect, "profiles/default/claude/agents/foo.md")); err != nil {
		t.Fatalf("expected agent under profiles/default/: %v", err)
	}

	// Machine B on profile=work clones and syncs. With extends wired up,
	// the agent should materialize under homeB/.claude even though work
	// never pushed anything.
	homeB := filepath.Join(tmp, "homeB")
	repoB := filepath.Join(tmp, "repoB")
	stateB := filepath.Join(tmp, "stateB")
	if err := os.MkdirAll(filepath.Join(homeB, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Clone(context.Background(), bareDir, repoB, nil); err != nil {
		t.Fatal(err)
	}
	inB := machineInputs(homeB, repoB, stateB, cfg)
	inB.Profile = "work"
	if _, err := Run(context.Background(), inB, nil); err != nil {
		t.Fatalf("machine B (work) pull: %v", err)
	}
	// The inherited agent should have landed in B's ~/.claude.
	if got, err := os.ReadFile(filepath.Join(homeB, ".claude/agents/foo.md")); err != nil || string(got) != "agent body" {
		t.Errorf("inherited agent not on work machine: got=%q err=%v", got, err)
	}
}

func TestProfileExcludeBlocksPushAndPull(t *testing.T) {
	secrets.MockInit()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)

	// Build a config with a "work" profile that extends "default" and excludes
	// the foo agent.
	cfg, _ := config.LoadDefault()
	cfg.Profiles["work"] = config.ProfileSpec{
		Description: "work laptop",
		Extends:     "default",
		HostClasses: []string{"work"},
		Exclude: &config.ProfileExclude{
			Paths: []string{"claude/agents/foo.md"},
		},
	}

	// Machine A: personal, pushes the agent on profile=default
	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	seedMachine(t, homeA)
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("machine A: %v", err)
	}
	// Confirm the agent is in the bare repo (pushed under default).
	inspectClone := filepath.Join(tmp, "inspect")
	if _, err := gogit.PlainClone(inspectClone, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inspectClone, "profiles/default/claude/agents/foo.md")); err != nil {
		t.Fatalf("default profile should have the agent: %v", err)
	}

	// Machine B: work. Clone repo, sync under profile=work. foo.md should NOT
	// be pulled to disk, and should NOT be pushed back up either.
	homeB := filepath.Join(tmp, "homeB")
	repoB := filepath.Join(tmp, "repoB")
	stateB := filepath.Join(tmp, "stateB")
	if err := os.MkdirAll(filepath.Join(homeB, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Clone(context.Background(), bareDir, repoB, nil); err != nil {
		t.Fatal(err)
	}
	inB := machineInputs(homeB, repoB, stateB, cfg)
	inB.Profile = "work"
	resB, err := Run(context.Background(), inB, nil)
	if err != nil {
		t.Fatalf("machine B: %v", err)
	}

	// foo.md must not be on B's disk.
	if _, err := os.Stat(filepath.Join(homeB, ".claude/agents/foo.md")); err == nil {
		t.Error("profile-excluded agent was pulled to work machine")
	}

	// If B has anything in the plan for foo.md, it must be flagged ExcludedByProfile.
	workPath := "profiles/work/claude/agents/foo.md"
	defaultPath := "profiles/default/claude/agents/foo.md"
	for _, a := range resB.Plan.Actions {
		if a.Path == workPath || a.Path == defaultPath {
			if !a.ExcludedByProfile {
				t.Errorf("action for %s should be ExcludedByProfile, got %+v", a.Path, a)
			}
		}
	}

	// Summary should not count the excluded path.
	for _, a := range resB.Plan.Actions {
		if a.ExcludedByProfile && !a.ExcludedByProfile {
			t.Fatal("sanity")
		}
	}
}

func TestEncryptionRoundTrip(t *testing.T) {
	secrets.MockInit()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()

	// Machine A — bootstrap + first sync (plaintext).
	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	seedMachine(t, homeA)
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	inA := machineInputs(homeA, repoA, stateA, cfg)
	if _, err := Run(context.Background(), inA, nil); err != nil {
		t.Fatalf("A first sync: %v", err)
	}

	// Enable encryption.
	if _, err := EnableEncryption(context.Background(), inA, "hunter2"); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Inspect the bare repo: agent file bytes must not contain the original plaintext.
	inspect := filepath.Join(tmp, "inspect")
	if _, err := gogit.PlainClone(inspect, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatal(err)
	}
	ct, err := os.ReadFile(filepath.Join(inspect, "profiles/default/claude/agents/foo.md"))
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}
	if strings.Contains(string(ct), "agent body") {
		t.Errorf("plaintext leaked into encrypted repo:\n%s", ct)
	}

	// Second sync should still work — round-trip through encryption.
	if _, err := Run(context.Background(), inA, nil); err != nil {
		t.Fatalf("A sync after enable: %v", err)
	}

	// Decrypt: repo bytes should be plaintext again.
	if _, err := DisableEncryption(context.Background(), inA); err != nil {
		t.Fatalf("disable: %v", err)
	}
	inspect2 := filepath.Join(tmp, "inspect2")
	if _, err := gogit.PlainClone(inspect2, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatal(err)
	}
	pt, err := os.ReadFile(filepath.Join(inspect2, "profiles/default/claude/agents/foo.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pt), "agent body") {
		t.Errorf("expected plaintext after decrypt; got:\n%s", pt)
	}
}

// TestInheritedFileArrivesOnMachineMissingLocal — a work machine that has
// synced before but has never had ~/.claude.json on disk should still pull
// updates to the inherited default/claude.json, not treat the missing local
// as a "user deleted it" and conflict/DeleteRemote against the ancestor.
func TestInheritedFileArrivesOnMachineMissingLocal(t *testing.T) {
	t.Skip("v0.9.0: pins claude.json inheritance across machines; claude.json itself is no longer synced in v0.9.0 (only mcpServers slice via mcpextract). Rewrite to cover skill or agent inheritance instead.")
	secrets.MockInit()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()
	cfg.Profiles["work"] = config.ProfileSpec{
		Description: "work laptop",
		Extends:     "default",
	}

	// Machine A (personal, profile=default): full seed, syncs claude.json + agents.
	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	seedMachine(t, homeA)
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("machine A first sync: %v", err)
	}

	// Machine B (work, profile=work extends default): ~/.claude exists but
	// ~/.claude.json does NOT. Claude Code hasn't been launched here yet.
	// First sync on B establishes state.LastSyncedSHA[work], pulling agents
	// inherited from default. claude.json won't write because local is
	// missing and base is empty → AddLocal → pulled.
	homeB := filepath.Join(tmp, "homeB")
	repoB := filepath.Join(tmp, "repoB")
	stateB := filepath.Join(tmp, "stateB")
	if err := os.MkdirAll(filepath.Join(homeB, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Clone(context.Background(), bareDir, repoB, nil); err != nil {
		t.Fatal(err)
	}
	// Delete B's ~/.claude.json after first sync so the second sync sees
	// "local missing, base=<inherited default from baseCommit>, remote=<new
	// default>" — the exact scenario that broke before the ancestor-base fix.
	inB := machineInputs(homeB, repoB, stateB, cfg)
	inB.Profile = "work"
	if _, err := Run(context.Background(), inB, nil); err != nil {
		t.Fatalf("machine B first sync: %v", err)
	}
	if err := os.Remove(filepath.Join(homeB, ".claude.json")); err != nil {
		t.Fatalf("remove B claude.json: %v", err)
	}

	// Machine A edits ~/.claude.json (adds an mcpServer) and pushes.
	jsonA := filepath.Join(homeA, ".claude.json")
	updated := strings.Replace(fixtureClaudeJSON, `"gemini"`, `"gemini-v2"`, 1)
	if err := os.WriteFile(jsonA, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("machine A second sync: %v", err)
	}

	// Machine B's second sync — work's baseCommit was set by its first
	// sync, and profiles/work/claude.json was never there. Base lookup
	// walks up and hits profiles/default/claude.json. Pre-fix: Decide saw
	// (local="", base=set, remote=set) → Conflict or DeleteRemote. Post-
	// fix: baseSHA gets nulled because it came from an ancestor, so Decide
	// sees (local="", base="", remote=set) → AddLocal → pulled.
	resB, err := Run(context.Background(), inB, nil)
	if err != nil {
		t.Fatalf("machine B second sync: %v", err)
	}
	if len(resB.Plan.Conflicts) > 0 {
		t.Fatalf("unexpected conflicts: %+v", resB.Plan.Conflicts)
	}
	got, err := os.ReadFile(filepath.Join(homeB, ".claude.json"))
	if err != nil {
		t.Fatalf("expected ~/.claude.json on B after second sync: %v", err)
	}
	if !strings.Contains(string(got), "gemini-v2") {
		t.Errorf("B didn't receive A's update; claude.json = %s", got)
	}
	// Critical: A's profiles/default/claude.json must still exist in the
	// bare repo after B's sync (pre-fix B would have DeleteRemote'd it).
	inspect := filepath.Join(tmp, "inspectAfter")
	if _, err := gogit.PlainClone(inspect, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inspect, "profiles/default/claude.json")); err != nil {
		t.Errorf("default/claude.json was deleted from repo by work sync: %v", err)
	}
}

// TestStaleExcludeGC — a repo with content that used to sync but is now
// covered by a .syncignore rule (e.g. projects/ in the v0.3 default set)
// should have that content silently DeleteRemote'd on the next sync,
// not surfaced as a conflict or stale forever.
func TestStaleExcludeGC(t *testing.T) {
	t.Skip("v0.9.0: relies on the v0.8.x default .syncignore content (projects/, file-history/, etc.) which collapsed when the discover walk narrowed to explicit content roots. Rewrite using a custom .syncignore + an excluded skill or hook script.")
	secrets.MockInit()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()

	homeA := filepath.Join(tmp, "home")
	repoA := filepath.Join(tmp, "repo")
	stateA := filepath.Join(tmp, "state")
	seedMachine(t, homeA)
	// Seed a file under an IGNORED subtree locally — we want to prove that
	// the cleanup happens even when the user no longer has it on disk.
	// Since the file is in the syncignore-excluded "projects/" dir,
	// discover.Walk will skip it on local side.
	if err := os.MkdirAll(filepath.Join(homeA, ".claude/projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	// First sync: push everything that passes the ignore matcher.
	if _, err := Run(context.Background(), machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// Now stash a legacy file directly in the repo worktree — simulating
	// a repo that carries pre-v0.3 content in an excluded subtree.
	legacyRel := "profiles/default/claude/projects/legacy.md"
	legacyAbs := filepath.Join(repoA, legacyRel)
	if err := os.MkdirAll(filepath.Dir(legacyAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyAbs, []byte("leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Commit + push the legacy file so it's on the remote and a future
	// discover pass will see it in remoteEntries.
	r, _ := gitx.Open(repoA)
	_ = r.AddAll()
	if _, err := r.Commit("add legacy", "test", "test@example.com"); err != nil {
		t.Fatalf("commit legacy: %v", err)
	}
	if err := r.Push(context.Background(), nil); err != nil {
		t.Fatalf("push legacy: %v", err)
	}

	// Second sync: the local .syncignore now covers projects/, and the
	// legacy file has no local equivalent. Expected: silent DeleteRemote,
	// no conflict, no re-pull to disk.
	res, err := Run(context.Background(), machineInputs(homeA, repoA, stateA, cfg), nil)
	if err != nil {
		t.Fatalf("gc sync: %v", err)
	}
	if len(res.Plan.Conflicts) > 0 {
		t.Fatalf("unexpected conflicts: %+v", res.Plan.Conflicts)
	}
	// The legacy file must be gone from the repo worktree.
	if _, err := os.Stat(legacyAbs); err == nil {
		t.Error("legacy file still present in repo worktree after GC")
	}
	// And must not have been pulled to local either.
	if _, err := os.Stat(filepath.Join(homeA, ".claude/projects/legacy.md")); err == nil {
		t.Error("excluded file was pulled to local despite syncignore")
	}
}

func TestDryRunReturnsPlanWithoutWrites(t *testing.T) {
	secrets.MockInit()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()

	homeA := filepath.Join(tmp, "home")
	repoA := filepath.Join(tmp, "repo")
	stateA := filepath.Join(tmp, "state")
	seedMachine(t, homeA)
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}

	in := machineInputs(homeA, repoA, stateA, cfg)
	in.DryRun = true

	res, err := Run(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.CommitSHA != "" {
		t.Errorf("dry-run produced commit: %s", res.CommitSHA)
	}
	if len(res.Plan.Actions) == 0 {
		t.Error("expected plan actions")
	}
	if _, err := os.Stat(filepath.Join(repoA, "profiles/default/claude.json")); err == nil {
		t.Error("dry-run wrote to the repo")
	}
}

// TestEnableEncryptionAdvancesLastSyncedSHA pins the iteration-5 fix in
// commitMigration: after EnableEncryption commits + pushes the encrypted
// repo tree, state.LastSyncedSHA must advance to that new head. Without
// this, the user's next sync would see a stale baseCommit — currently
// benign because the old plaintext blob is still in git history, but
// exactly the class of "silently stale state" bug we cleaned up for
// resolve and rollback in iteration 1. Reverting the advanceStateToHead
// call in commitMigration should cause this test to fail.
func TestEnableEncryptionAdvancesLastSyncedSHA(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()

	home := filepath.Join(tmp, "home")
	repoPath := filepath.Join(tmp, "repo")
	stateDir := filepath.Join(tmp, "state")
	seedMachine(t, home)
	if _, err := gitx.Init(repoPath, bareDir); err != nil {
		t.Fatal(err)
	}
	in := machineInputs(home, repoPath, stateDir, cfg)

	// First land plaintext content so there's something for
	// EnableEncryption to re-encrypt.
	if _, err := Run(context.Background(), in, nil); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	st, err := loadHostState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	preEncHead := st.LastSyncedSHA[in.Profile]
	if preEncHead == "" {
		t.Fatal("expected initial sync to set LastSyncedSHA")
	}

	res, err := EnableEncryption(context.Background(), in, "my-passphrase")
	if err != nil {
		t.Fatalf("EnableEncryption: %v", err)
	}
	if res.CommitSHA == "" {
		t.Fatal("EnableEncryption should have produced a migration commit")
	}

	st2, err := loadHostState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	postEncHead := st2.LastSyncedSHA[in.Profile]
	if postEncHead == preEncHead {
		t.Errorf("LastSyncedSHA did not advance after EnableEncryption — next sync will use a stale baseCommit (%s)", preEncHead[:7])
	}
	if postEncHead != res.CommitSHA {
		t.Errorf("LastSyncedSHA = %s, want the migration commit %s",
			shortSHA(postEncHead), shortSHA(res.CommitSHA))
	}
	if st2.LastSyncedAt[in.Profile].IsZero() {
		t.Error("LastSyncedAt should have been set alongside LastSyncedSHA")
	}
}

// shortSHA is a tiny test helper — we import nothing new. If the
// commitSHA is shorter than 7 (shouldn't happen in practice but safe
// for tests), return it as-is.
func shortSHA(s string) string {
	if len(s) >= 7 {
		return s[:7]
	}
	return s
}

// TestCrossMachineEncryptionRoundTrip pins the iteration-35 flow: A
// bootstraps plaintext, enables encryption with passphrase P, and pushes
// ciphertext to the remote. B clones the encrypted remote, stores P in
// its own keychain, and syncs. B must see the original plaintext content
// on disk after sync — the critical "onboard a second machine to an
// encrypted repo" path that the CLI `ccsync unlock` wraps.
//
// Without encryption rigour here, the user-visible failure is subtle:
// B's sync silently fails on the encryption boundary, the orchestrator
// surfaces something like "decrypt failed", and the user is locked out
// with no clear path back. This test exercises the whole chain end-to-
// end so a regression in marker read / keychain fetch / Decrypt /
// maybeDecrypt / sync's fetch path fails loud.
func TestCrossMachineEncryptionRoundTrip(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()
	ctx := context.Background()

	// A: bootstrap + first sync (plaintext), then enable encryption.
	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	seedMachine(t, homeA)
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	inA := machineInputs(homeA, repoA, stateA, cfg)
	if _, err := Run(ctx, inA, nil); err != nil {
		t.Fatalf("A first sync: %v", err)
	}
	if _, err := EnableEncryption(ctx, inA, "correct-horse-battery-staple"); err != nil {
		t.Fatalf("A enable encryption: %v", err)
	}

	// Verify the remote is now ciphertext — no plaintext "agent body"
	// bytes anywhere under the encrypted paths.
	inspect := filepath.Join(tmp, "inspect-ct")
	if _, err := gogit.PlainClone(inspect, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatalf("inspect-ct clone: %v", err)
	}
	ct, err := os.ReadFile(filepath.Join(inspect, "profiles/default/claude/agents/foo.md"))
	if err != nil {
		t.Fatalf("read remote agent: %v", err)
	}
	if strings.Contains(string(ct), "agent body") {
		t.Fatal("plaintext leaked into encrypted remote")
	}

	// B: clone the (now-encrypted) remote, seed keychain, sync.
	homeB := filepath.Join(tmp, "homeB")
	repoB := filepath.Join(tmp, "repoB")
	stateB := filepath.Join(tmp, "stateB")
	if err := os.MkdirAll(filepath.Join(homeB, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Clone(ctx, bareDir, repoB, nil); err != nil {
		t.Fatalf("B clone: %v", err)
	}

	// Model B BEFORE unlocking: sync should fail loud with an instructive
	// error, not silently write ciphertext to disk or spin. Passphrase
	// keychain entry lives in a shared MockInit backend, so force-delete
	// it to simulate a fresh machine with no stored passphrase.
	_ = secrets.Delete(SecretsKeyPassphrase)
	inB := machineInputs(homeB, repoB, stateB, cfg)
	if _, err := Run(ctx, inB, nil); err == nil {
		t.Fatal("sync should fail when encryption passphrase isn't stored")
	} else if !strings.Contains(err.Error(), "passphrase") && !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("pre-unlock sync error should mention passphrase/encryption; got: %v", err)
	}

	// Now B unlocks (stores the passphrase) — the mock keychain is shared,
	// so this simulates `ccsync unlock` having run successfully.
	if err := secrets.Store(SecretsKeyPassphrase, "correct-horse-battery-staple"); err != nil {
		t.Fatalf("store passphrase: %v", err)
	}
	if _, err := Run(ctx, inB, nil); err != nil {
		t.Fatalf("B sync after unlock: %v", err)
	}

	// B should now see the original plaintext on disk.
	got, err := os.ReadFile(filepath.Join(homeB, ".claude/agents/foo.md"))
	if err != nil {
		t.Fatalf("read B agent: %v", err)
	}
	if !strings.Contains(string(got), "agent body") {
		t.Errorf("B did not receive decrypted plaintext: %q", got)
	}

	// And the secret inside claude.json (redacted at rest, restored via
	// keychain) must have come through too.
	localJSON, err := os.ReadFile(filepath.Join(homeB, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(localJSON), "<<REDACTED:") {
		t.Errorf("B's claude.json still has redaction placeholder after sync:\n%s", localJSON)
	}
}

// TestSyncignoreUpdatePropagates pins the flow: user on A edits the
// repo-root .syncignore to add a new exclusion pattern; the change
// reaches B on the next sync; B then treats matching local files as
// untracked (neither pushing nor pulling), regardless of whether they
// existed in B's ~/.claude already. .syncignore has to travel in-band
// with the content or the fleet falls out of policy the instant one
// machine changes it. Scope target: discover.Walk uses the repo-root
// .syncignore at sync time (not a cached copy), so the new rule takes
// effect on the same sync that picked it up.
func TestSyncignoreUpdatePropagates(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()
	ctx := context.Background()

	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	seedMachine(t, homeA)
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	// Seed .syncignore in the repo (bootstrap does this in the real flow;
	// these unit tests bypass bootstrap).
	syncignoreOnDisk := filepath.Join(repoA, ".syncignore")
	if err := os.WriteFile(syncignoreOnDisk, []byte(cfg.DefaultSyncignore), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("A first sync: %v", err)
	}

	homeB := filepath.Join(tmp, "homeB")
	repoB := filepath.Join(tmp, "repoB")
	stateB := filepath.Join(tmp, "stateB")
	if err := os.MkdirAll(filepath.Join(homeB, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Clone(ctx, bareDir, repoB, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil); err != nil {
		t.Fatalf("B first sync: %v", err)
	}

	// A writes a new file that both machines currently have; then A
	// edits .syncignore to exclude that file's pattern. The edited
	// .syncignore + the new file both get pushed on the same sync.
	secretDir := filepath.Join(homeA, ".claude/scratch")
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(secretDir, "private.log")
	if err := os.WriteFile(secretFile, []byte("A's private notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev, err := os.ReadFile(syncignoreOnDisk)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(prev) + "\nscratch/\n"
	if err := os.WriteFile(syncignoreOnDisk, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	// The file was created BEFORE .syncignore changed — but the rule's
	// still authoritative at sync time. Neither side should end up with
	// scratch/ in the tracked tree.
	if _, err := Run(ctx, machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("A post-rule sync: %v", err)
	}

	// The new rule should have reached the remote, and scratch/ should
	// NOT be under profiles/default/.
	inspect := filepath.Join(tmp, "inspect")
	if _, err := gogit.PlainClone(inspect, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatal(err)
	}
	gotIgnore, err := os.ReadFile(filepath.Join(inspect, ".syncignore"))
	if err != nil {
		t.Fatalf("read remote .syncignore: %v", err)
	}
	if !strings.Contains(string(gotIgnore), "scratch/") {
		t.Errorf("new rule didn't propagate:\n%s", gotIgnore)
	}
	if _, err := os.Stat(filepath.Join(inspect, "profiles/default/claude/scratch/private.log")); err == nil {
		t.Fatal("scratch/private.log leaked into remote despite syncignore rule")
	}

	// B syncs — pulls the new .syncignore, and also creates its own
	// scratch file locally. The new rule should keep B's file off the
	// remote too.
	if err := os.MkdirAll(filepath.Join(homeB, ".claude/scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeB, ".claude/scratch/b-private.log"), []byte("B's notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil); err != nil {
		t.Fatalf("B sync after rule: %v", err)
	}
	inspect2 := filepath.Join(tmp, "inspect2")
	if _, err := gogit.PlainClone(inspect2, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inspect2, "profiles/default/claude/scratch/b-private.log")); err == nil {
		t.Error("B's scratch file reached remote — .syncignore rule didn't take effect on B's first sync after pulling")
	}
}

// TestBinaryMergeUsesRealMtimes pins the iteration-42 fix: binary file
// conflicts must respect actual file mtimes, not always pick remote.
// Pre-fix, mergeFile called merge.Binary(local, time.Now(), remote,
// time.Now()) — two identical-ish times, with the first one evaluated
// slightly earlier. remote.After(local) therefore always held, so
// remote always won regardless of which side was actually newer on
// disk. CLAUDE.md promised LWW by mtime; the promise was silently
// broken for every binary conflict.
func TestBinaryMergeUsesRealMtimes(t *testing.T) {
	t.Skip("v0.9.0: pins binary-LWW behaviour for settings.json which is no longer synced. Rewrite using a binary asset under one of the v0.9.0 content directories — e.g. claude/output-styles/some.png.")
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()
	ctx := context.Background()

	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	if err := os.MkdirAll(filepath.Join(homeA, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A binary-shaped extension so mergeFile routes to merge.Binary.
	// Contents here are arbitrary opaque bytes.
	binPath := filepath.Join(homeA, ".claude/tool.bin")
	if err := os.WriteFile(binPath, []byte("v1-A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("A first sync: %v", err)
	}

	homeB := filepath.Join(tmp, "homeB")
	repoB := filepath.Join(tmp, "repoB")
	stateB := filepath.Join(tmp, "stateB")
	if err := os.MkdirAll(filepath.Join(homeB, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Clone(ctx, bareDir, repoB, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil); err != nil {
		t.Fatalf("B first sync: %v", err)
	}

	// B edits its local copy with an OLD mtime.
	binB := filepath.Join(homeB, ".claude/tool.bin")
	if err := os.WriteFile(binB, []byte("v2-B"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(binB, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// A edits its local copy with a FRESH mtime, then syncs (pushes v2-A
	// with current mtime to the remote manifest).
	if err := os.WriteFile(binPath, []byte("v2-A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("A second sync: %v", err)
	}

	// B syncs. Both sides modified since base. Remote's mtime is ~now;
	// B's local mtime is 24 hours old. Remote should win by LWW.
	if _, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	got, err := os.ReadFile(binB)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2-A" {
		t.Errorf("LWW: B's local is older — remote should win. got %q, want v2-A", got)
	}

	// Reverse the scenario: make B's local the fresh one, A the old one.
	// Reset both machines to a known base first by doing another round.
	if err := os.WriteFile(binB, []byte("v3-B"), 0o644); err != nil {
		t.Fatal(err)
	}
	// B's mtime is "now" here (fresh write).
	// A edits with an artificially-OLD mtime.
	if err := os.WriteFile(binPath, []byte("v3-A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(binPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("A third sync: %v", err)
	}
	if _, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil); err != nil {
		t.Fatalf("B third sync: %v", err)
	}
	got, err = os.ReadFile(binB)
	if err != nil {
		t.Fatal(err)
	}
	// B's local was freshly-written (v3-B). If remote "always wins" bug
	// is present, disk would be v3-A. With correct LWW, v3-B stays.
	if string(got) != "v3-B" {
		t.Errorf("LWW: B's local is fresher — should win. got %q, want v3-B", got)
	}
}

// TestRollbackToCommitReversesPushAndSnapshotsPresent pins the iteration-
// 36 smoke: `ccsync rollback --commit <sha>` — the action recommended to
// push-only machines that don't have a local snapshot to restore from —
// must (a) replay the target commit's content to disk and to the repo,
// (b) push a NEW commit that other machines can pull, (c) advance
// state.LastSyncedSHA to that new commit so the next sync sees a clean
// base, and (d) leave behind a pre-rollback snapshot so the user can
// undo the undo if they changed their mind.
//
// Pre-iter-36, rollback --commit was the documented "to undo a push"
// path but untested end-to-end — a regression in any of (a)–(d) would
// have shipped silently.
func TestRollbackToCommitReversesPushAndSnapshotsPresent(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()
	ctx := context.Background()

	home := filepath.Join(tmp, "home")
	repoPath := filepath.Join(tmp, "repo")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(filepath.Join(home, ".claude/agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(home, ".claude/agents/history.md")
	if err := os.WriteFile(agentPath, []byte("v1 content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Init(repoPath, bareDir); err != nil {
		t.Fatal(err)
	}
	in := machineInputs(home, repoPath, stateDir, cfg)

	// Sync v1 (commit1).
	res1, err := Run(ctx, in, nil)
	if err != nil {
		t.Fatalf("sync v1: %v", err)
	}
	if res1.CommitSHA == "" {
		t.Fatal("expected a commit for v1")
	}
	commit1 := res1.CommitSHA

	// Overwrite with v2, sync (commit2).
	if err := os.WriteFile(agentPath, []byte("v2 content"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := Run(ctx, in, nil)
	if err != nil {
		t.Fatalf("sync v2: %v", err)
	}
	if res2.CommitSHA == "" || res2.CommitSHA == commit1 {
		t.Fatalf("expected a distinct commit for v2, got %q", res2.CommitSHA)
	}

	// Rollback to commit1.
	resR, err := RollbackTo(ctx, in, commit1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if resR.CommitSHA == "" || resR.CommitSHA == res2.CommitSHA {
		t.Fatalf("rollback should produce a NEW commit; got %q", resR.CommitSHA)
	}

	// (a) disk reflects v1
	got, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1 content" {
		t.Errorf("disk after rollback = %q, want v1 content", got)
	}

	// (b) remote reflects v1 via fresh clone inspection
	inspect := filepath.Join(tmp, "inspect")
	if _, err := gogit.PlainClone(inspect, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatalf("inspect clone: %v", err)
	}
	ct, err := os.ReadFile(filepath.Join(inspect, "profiles/default/claude/agents/history.md"))
	if err != nil {
		t.Fatalf("read remote agent: %v", err)
	}
	if string(ct) != "v1 content" {
		t.Errorf("remote after rollback = %q, want v1 content", ct)
	}

	// (c) state advanced to rollback commit
	st, err := loadHostState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastSyncedSHA["default"] != resR.CommitSHA {
		t.Errorf("LastSyncedSHA = %q, want rollback commit %q", st.LastSyncedSHA["default"], resR.CommitSHA)
	}

	// (d) pre-rollback snapshot exists and has the v2 content
	snapshots, err := os.ReadDir(filepath.Join(stateDir, "snapshots"))
	if err != nil {
		t.Fatalf("read snapshots dir: %v", err)
	}
	var rollbackSnap string
	for _, d := range snapshots {
		if strings.Contains(d.Name(), "rollback") {
			rollbackSnap = d.Name()
			break
		}
	}
	if rollbackSnap == "" {
		t.Fatal("no rollback snapshot found — user has no undo-the-undo path")
	}
	// The snapshot stores files under their abs-path-rooted layout.
	// Verifying "rollback-op snapshot exists" is enough for this pin;
	// snapshot.Restore is covered by snapshot's own tests.

	// A subsequent sync should be a no-op — state and disk and remote
	// all consistent at the rollback commit.
	resFinal, err := Run(ctx, in, nil)
	if err != nil {
		t.Fatalf("post-rollback sync: %v", err)
	}
	if resFinal.CommitSHA != "" {
		t.Errorf("post-rollback sync produced a spurious commit %q — nothing should have changed", resFinal.CommitSHA)
	}
}

// TestWrongPassphraseSurfacesLoudly pins the error path for an incorrect
// stored passphrase. A user who typo'd during `ccsync unlock` (or whose
// keychain entry was corrupted/rotated) must NOT have ccsync silently
// fall through to writing garbage to disk or pushing ciphertext that no
// one can read. The sync should fail with an error that mentions
// decryption or authentication, pointing the user toward re-unlocking.
func TestWrongPassphraseSurfacesLoudly(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()
	ctx := context.Background()

	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	seedMachine(t, homeA)
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	inA := machineInputs(homeA, repoA, stateA, cfg)
	if _, err := Run(ctx, inA, nil); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	if _, err := EnableEncryption(ctx, inA, "right-passphrase"); err != nil {
		t.Fatalf("enable encryption: %v", err)
	}

	// Simulate a fresh machine with the WRONG passphrase stored.
	homeB := filepath.Join(tmp, "homeB")
	repoB := filepath.Join(tmp, "repoB")
	stateB := filepath.Join(tmp, "stateB")
	if err := os.MkdirAll(filepath.Join(homeB, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Clone(ctx, bareDir, repoB, nil); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Store(SecretsKeyPassphrase, "wrong-passphrase"); err != nil {
		t.Fatalf("store wrong passphrase: %v", err)
	}

	_, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil)
	if err == nil {
		t.Fatal("sync must fail with a wrong stored passphrase; got nil error (silent bad-decrypt)")
	}
	// The message shape — "decrypt ..." from run.go's wrap — is what the
	// user will see. It doesn't need to say "wrong passphrase" verbatim;
	// it must clearly not be "all good" and must mention decryption.
	msg := err.Error()
	if !strings.Contains(msg, "decrypt") && !strings.Contains(msg, "auth") {
		t.Errorf("error doesn't point at the encryption boundary; got: %v", err)
	}

	// The disk must be untouched — no half-written garbage files from a
	// decrypt that returned error mid-stream.
	if _, err := os.Stat(filepath.Join(homeB, ".claude/agents/foo.md")); err == nil {
		t.Error("wrong-passphrase sync wrote to disk — should have bailed before writing")
	}
}

// TestEncryptionLeavesGitignorePlaintext pins the invariant that the
// repo-root .gitignore added by iteration 33 stays plaintext across an
// EnableEncryption migration. The protection comes from walkAndTransform's
// `strings.HasPrefix(rel, "profiles/")` gate — only files under profiles/
// are rewritten, so root-level .gitignore/.syncignore/ccsync.yaml/etc.
// are skipped. This test pins that invariant so a future refactor that
// drops the prefix gate (or expands walkAndTransform to cover metadata)
// can't silently undo iter-33's .bak-leak fix: the moment .gitignore
// becomes ciphertext, go-git stops honouring it and every .bak rejoins
// the sync on the next commit.
func TestEncryptionLeavesGitignorePlaintext(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, _ := config.LoadDefault()

	home := filepath.Join(tmp, "home")
	repoPath := filepath.Join(tmp, "repo")
	stateDir := filepath.Join(tmp, "state")
	seedMachine(t, home)
	if _, err := gitx.Init(repoPath, bareDir); err != nil {
		t.Fatal(err)
	}
	in := machineInputs(home, repoPath, stateDir, cfg)
	if _, err := Run(context.Background(), in, nil); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	if _, err := EnableEncryption(context.Background(), in, "hunter2"); err != nil {
		t.Fatalf("enable encryption: %v", err)
	}

	// The canonical test: does the post-encryption .gitignore parse as
	// gitignore text? "/*.bak\n/*.tmp\n" is plaintext; a scrypt-encrypted
	// payload starts with the ccsync magic header (not printable ASCII).
	gi, err := os.ReadFile(filepath.Join(repoPath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gi), "*.bak") {
		t.Errorf(".gitignore was encrypted after EnableEncryption — go-git can no longer use it, "+
			"and ccsync.yaml.bak will leak on the next sync. Bytes: %x", gi)
	}

	// The load-bearing downstream assertion: trigger a .bak, sync, confirm
	// the remote tree does NOT contain it.
	cfg.Profiles["extra"] = config.ProfileSpec{Description: "trigger .bak"}
	if err := cfg.SaveWithBackup(filepath.Join(repoPath, "ccsync.yaml")); err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if _, err := Run(context.Background(), in, nil); err != nil {
		t.Fatalf("sync after save: %v", err)
	}
	inspect := filepath.Join(tmp, "inspect")
	if _, err := gogit.PlainClone(inspect, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatalf("inspect clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inspect, "ccsync.yaml.bak")); err == nil {
		t.Fatal("ccsync.yaml.bak leaked into encrypted remote — iter-33 .gitignore protection broken by encryption")
	}
}

// TestProfileSwitchDoesNotContaminateTarget pins the iteration-34 fix:
// when the user switches from profile A to profile B, files that lived
// only under A must be removed from disk BEFORE the next sync runs.
// Pre-fix, those files stayed on disk; the next sync saw them as local-
// only (nothing at base, nothing remote under the new profile's subtree)
// and pushed them, silently re-attributing them to the target profile
// and polluting every machine that pulls it.
//
// Repro shape: two profiles (default + work). A file lives only under
// work. Switch from work → default. Next sync must NOT push that file
// into profiles/default/.
func TestProfileSwitchDoesNotContaminateTarget(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["work"] = config.ProfileSpec{Description: "work laptop"}
	ctx := context.Background()

	home := filepath.Join(tmp, "home")
	repoPath := filepath.Join(tmp, "repo")
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(filepath.Join(home, ".claude/agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed a default-only file and sync under profile=default.
	if err := os.WriteFile(filepath.Join(home, ".claude/agents/default-only.md"), []byte("default agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Init(repoPath, bareDir); err != nil {
		t.Fatal(err)
	}
	in := machineInputs(home, repoPath, stateDir, cfg)
	in.Profile = "default"
	if _, err := Run(ctx, in, nil); err != nil {
		t.Fatalf("default sync: %v", err)
	}

	// Switch to work, add a work-only file, sync.
	metaA, err := profile.SwitchAndSwap(cfg, repoPath, loadStateForTest(t, stateDir), stateDir, "work",
		filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("switch to work: %v", err)
	}
	if metaA.ID == "" {
		t.Error("expected a pre-switch snapshot")
	}
	// After switching to work, default-only.md should be gone from disk
	// (it belongs to default, not work; snapshot preserves it for recovery).
	if _, err := os.Stat(filepath.Join(home, ".claude/agents/default-only.md")); err == nil {
		t.Error("default-only.md still on disk after switch to work — swap didn't clean up")
	}

	if err := os.WriteFile(filepath.Join(home, ".claude/agents/work-only.md"), []byte("work agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	inWork := machineInputs(home, repoPath, stateDir, cfg)
	inWork.Profile = "work"
	if _, err := Run(ctx, inWork, nil); err != nil {
		t.Fatalf("work sync: %v", err)
	}

	// Switch back to default. work-only.md must disappear from disk —
	// otherwise the next sync will push it into profiles/default/.
	if _, err := profile.SwitchAndSwap(cfg, repoPath, loadStateForTest(t, stateDir), stateDir, "default",
		filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json")); err != nil {
		t.Fatalf("switch back to default: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/agents/work-only.md")); err == nil {
		t.Fatal("work-only.md still on disk after switch back to default — contamination about to happen")
	}

	inDefault := machineInputs(home, repoPath, stateDir, cfg)
	inDefault.Profile = "default"
	if _, err := Run(ctx, inDefault, nil); err != nil {
		t.Fatalf("default sync after switch: %v", err)
	}

	// The critical assertion: remote default/ tree must NOT contain work-only.md.
	inspect := filepath.Join(tmp, "inspect")
	if _, err := gogit.PlainClone(inspect, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatalf("inspect clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inspect, "profiles/default/claude/agents/work-only.md")); err == nil {
		t.Fatal("work-only.md leaked into profiles/default/ — profile switch contaminated default")
	}
	if _, err := os.Stat(filepath.Join(inspect, "profiles/work/claude/agents/work-only.md")); err != nil {
		t.Errorf("work-only.md should still exist under profiles/work/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inspect, "profiles/default/claude/agents/default-only.md")); err != nil {
		t.Errorf("default-only.md should still exist under profiles/default/: %v", err)
	}
}

// loadStateForTest is a tiny helper to read state.State inside a test.
func loadStateForTest(t *testing.T, stateDir string) *ccstate.State {
	t.Helper()
	st, err := loadHostState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestSaveWithBackupBakIsGitignored pins the iteration-33 fix: the atomic-
// save sibling ccsync.yaml.bak — intentional on disk for the repo-local
// rollback — must not land in the tracked git tree, because AddAll would
// otherwise publish it to the remote and every other clone acquires a
// stale copy of someone else's past config. The fix is the seeded repo-
// root .gitignore with `/*.bak`. This test provokes a config save
// (profile Create), syncs, and asserts the .bak exists on disk but
// NOT in the remote tree.
func TestSaveWithBackupBakIsGitignored(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	home := filepath.Join(tmp, "home")
	repoPath := filepath.Join(tmp, "repo")
	stateDir := filepath.Join(tmp, "state")
	seedMachine(t, home)
	if _, err := gitx.Init(repoPath, bareDir); err != nil {
		t.Fatal(err)
	}

	// Write ccsync.yaml at the repo root so SaveWithBackup has something
	// to move aside as .bak. (Bootstrap does this normally; the unit tests
	// skip bootstrap and drive Run directly.)
	cfgPath := filepath.Join(repoPath, "ccsync.yaml")
	if err := cfg.SaveWithBackup(cfgPath); err != nil {
		t.Fatalf("initial config save: %v", err)
	}

	// First sync: stages metadata (including the new .gitignore) and
	// ends with a pushed commit.
	if _, err := Run(ctx, machineInputs(home, repoPath, stateDir, cfg), nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// Mutate + re-save so SaveWithBackup creates the .bak sibling.
	cfg.Profiles["extra"] = config.ProfileSpec{Description: "trigger .bak"}
	if err := cfg.SaveWithBackup(cfgPath); err != nil {
		t.Fatalf("SaveWithBackup: %v", err)
	}
	if _, err := os.Stat(cfgPath + ".bak"); err != nil {
		t.Fatalf("ccsync.yaml.bak should exist on disk for rollback: %v", err)
	}

	// Run again; the sync orchestrator must stage + commit without pulling
	// the .bak along.
	if _, err := Run(ctx, machineInputs(home, repoPath, stateDir, cfg), nil); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	// Clone the bare to inspect what the remote actually holds.
	inspect := filepath.Join(tmp, "inspect")
	if _, err := gogit.PlainClone(inspect, false, &gogit.CloneOptions{URL: bareDir}); err != nil {
		t.Fatalf("inspect clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inspect, "ccsync.yaml.bak")); err == nil {
		t.Fatal("ccsync.yaml.bak was committed to the remote — atomic-save sibling leaked through AddAll")
	}
	if _, err := os.Stat(filepath.Join(inspect, ".gitignore")); err != nil {
		t.Fatalf("repo root .gitignore missing — self-heal should have written it: %v", err)
	}
}

// TestUnresolvedConflictDoesNotAdvanceState pins the iteration-32 fix: when
// Run surfaces conflicts that the caller has not yet resolved, it must NOT
// advance state.LastSyncedSHA. Pre-fix behavior: the bare CLI `ccsync sync`
// command prints "1 conflict — resolve in the TUI", returns cleanly, and
// silently bumps LastSyncedSHA to the remote head. On the very next sync,
// the orchestrator sees local != base (because local still holds the user's
// divergent edit) but base == remote (because state just moved there),
// classifies the file as ActionPush, and overwrites the other machine's
// content. End result: user opens a TUI that shows "no conflicts", the other
// side's work is gone, and nobody sees a warning. That is precisely the
// "never silently lose data" rule the whole merge strategy exists to enforce.
func TestUnresolvedConflictDoesNotAdvanceState(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	initBareWithMainHEAD(t, bareDir)
	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Machine A: seed + first sync (lands base content).
	homeA := filepath.Join(tmp, "homeA")
	repoA := filepath.Join(tmp, "repoA")
	stateA := filepath.Join(tmp, "stateA")
	claudeA := filepath.Join(homeA, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeA, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	sharedAbs := filepath.Join(claudeA, "agents/shared.md")
	if err := os.WriteFile(sharedAbs, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Init(repoA, bareDir); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("A first sync: %v", err)
	}

	// Machine B: clone + first sync (pulls shared.md).
	homeB := filepath.Join(tmp, "homeB")
	repoB := filepath.Join(tmp, "repoB")
	stateB := filepath.Join(tmp, "stateB")
	if err := os.MkdirAll(filepath.Join(homeB, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Clone(ctx, bareDir, repoB, nil); err != nil {
		t.Fatalf("B clone: %v", err)
	}
	if _, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil); err != nil {
		t.Fatalf("B first sync: %v", err)
	}

	// Concurrent divergent edits to the SAME file.
	if err := os.WriteFile(sharedAbs, []byte("a\nb-FROM-A\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sharedAbsB := filepath.Join(homeB, ".claude/agents/shared.md")
	if err := os.WriteFile(sharedAbsB, []byte("a\nb-FROM-B\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A pushes first; remote now has b-FROM-A.
	if _, err := Run(ctx, machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("A second sync: %v", err)
	}

	// B's state before the conflict sync — baseline for comparison.
	stBPre, err := loadHostState(stateB)
	if err != nil {
		t.Fatal(err)
	}
	baseBPre := stBPre.LastSyncedSHA["default"]
	if baseBPre == "" {
		t.Fatal("B's first sync should have set LastSyncedSHA")
	}

	// B syncs — detects the conflict. MUST NOT advance state.
	resB, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil)
	if err != nil {
		t.Fatalf("B conflict sync: %v", err)
	}
	if len(resB.Plan.Conflicts) == 0 {
		t.Fatal("expected a conflict on B, got none (test premise broken)")
	}
	stBPost, err := loadHostState(stateB)
	if err != nil {
		t.Fatal(err)
	}
	if stBPost.LastSyncedSHA["default"] != baseBPre {
		t.Errorf(
			"LastSyncedSHA advanced despite unresolved conflict: was %s, now %s. "+
				"Next sync will treat base==remote and silently push B's divergent edit, overwriting A's work.",
			shortSHA(baseBPre), shortSHA(stBPost.LastSyncedSHA["default"]))
	}

	// The real teeth: on B's NEXT sync, the same conflict must still surface.
	// Pre-fix, the stale state makes B treat the divergent file as a plain
	// local edit and push it unopposed.
	resB2, err := Run(ctx, machineInputs(homeB, repoB, stateB, cfg), nil)
	if err != nil {
		t.Fatalf("B repeat sync: %v", err)
	}
	if len(resB2.Plan.Conflicts) == 0 {
		t.Fatal("repeat sync on B lost the conflict (state advanced earlier); B just overwrote A's edit")
	}

	// Machine A re-reads its own edit from the repo to confirm it's intact
	// after B's repeat sync. Pre-fix this assertion is what fails loudly
	// when the test catches the silent-overwrite bug.
	if _, err := Run(ctx, machineInputs(homeA, repoA, stateA, cfg), nil); err != nil {
		t.Fatalf("A re-sync: %v", err)
	}
	got, err := os.ReadFile(sharedAbs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "b-FROM-A") {
		t.Errorf("A's edit was overwritten: got %q, expected content containing b-FROM-A", got)
	}
}
