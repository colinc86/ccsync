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
