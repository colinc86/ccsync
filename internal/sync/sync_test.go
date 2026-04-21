package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/secrets"
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

func TestCrossMachinePushPullRoundTrip(t *testing.T) {
	secrets.MockInit()

	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	if _, err := gogit.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}

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
	if _, err := gogit.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}
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
	if _, err := gogit.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}
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
	if _, err := gogit.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}

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
	if _, err := gogit.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}
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
	secrets.MockInit()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	if _, err := gogit.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}
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

func TestDryRunReturnsPlanWithoutWrites(t *testing.T) {
	secrets.MockInit()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	if _, err := gogit.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}
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
