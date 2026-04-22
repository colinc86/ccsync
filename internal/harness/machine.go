package harness

import (
	"context"
	"path/filepath"

	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
)

// Machine is one simulated node in a scenario. It has its own fake home
// dir (so ~/.claude and ~/.claude.json live inside the test temp root),
// its own ~/.ccsync state dir, and its own clone of the bare repo at
// {root}/machines/{name}/repo. Multiple machines in one scenario share
// the bare repo and the in-memory keyring, which is exactly the
// configuration of a real user running ccsync on several hosts against
// one sync remote.
type Machine struct {
	scenario *Scenario
	Name     string

	// Profile controls which profile this machine syncs under. Starts as
	// "default"; change via UseProfile before the first Sync.
	Profile string

	// Home is this machine's fake $HOME. Real files created under Home
	// are what sync.Run discovers.
	Home string
	// ClaudeDir is $HOME/.claude.
	ClaudeDir string
	// ClaudeJSON is $HOME/.claude.json.
	ClaudeJSON string
	// StateDir is $HOME/.ccsync.
	StateDir string
	// RepoPath is this machine's local clone of the bare repo.
	RepoPath string

	hostUUID string
}

// NewMachine sets up a new machine with initialized dirs + a clone of
// the bare repo. Subsequent operations (seed, sync, assert) all run
// against this isolated state. Calling NewMachine a second time with
// the same name is not guarded — callers are expected to pass
// descriptive names ("work", "home", "laptop") and not repeat.
func (s *Scenario) NewMachine(name string) *Machine {
	s.t.Helper()

	home := filepath.Join(s.Root, "machines", name, "home")
	claudeDir := filepath.Join(home, ".claude")
	claudeJSON := filepath.Join(home, ".claude.json")
	stateDir := filepath.Join(home, ".ccsync")
	repoPath := filepath.Join(s.Root, "machines", name, "repo")

	if err := mkdirs(claudeDir, stateDir); err != nil {
		s.t.Fatalf("machine %s dirs: %v", name, err)
	}

	// Bare may still be empty on the very first machine. gitx.Init
	// clones from bare if a HEAD exists, else it initialises a worktree
	// attached to bare as origin — that's the "first push from fresh"
	// path the very first Run() on this machine will execute.
	if s.BareHead() != "" {
		if _, err := gitx.Clone(context.Background(), s.BareDir, repoPath, nil); err != nil {
			s.t.Fatalf("machine %s clone: %v", name, err)
		}
	} else {
		if _, err := gitx.Init(repoPath, s.BareDir); err != nil {
			s.t.Fatalf("machine %s init: %v", name, err)
		}
	}

	return &Machine{
		scenario:   s,
		Name:       name,
		Profile:    "default",
		Home:       home,
		ClaudeDir:  claudeDir,
		ClaudeJSON: claudeJSON,
		StateDir:   stateDir,
		RepoPath:   repoPath,
		hostUUID:   "host-" + name,
	}
}

// UseProfile switches this machine to the named profile for subsequent
// syncs. Fluent — returns the machine.
func (m *Machine) UseProfile(name string) *Machine {
	m.Profile = name
	return m
}

// DenyPath adds a repo-relative path (under the active profile's prefix
// — e.g. "claude/commands/work-only.md") to this machine's denylist.
// Persists to state.json so the next Sync honors it.
func (m *Machine) DenyPath(repoRelPath string) *Machine {
	m.scenario.t.Helper()
	st, err := state.Load(m.StateDir)
	if err != nil {
		m.scenario.t.Fatalf("load state: %v", err)
	}
	st.DenyPath(repoRelPath)
	if err := state.Save(m.StateDir, st); err != nil {
		m.scenario.t.Fatalf("save state: %v", err)
	}
	return m
}

// DenyMCPServer adds an mcpServers key to this machine's denylist.
// Persists to state.json.
func (m *Machine) DenyMCPServer(name string) *Machine {
	m.scenario.t.Helper()
	st, err := state.Load(m.StateDir)
	if err != nil {
		m.scenario.t.Fatalf("load state: %v", err)
	}
	st.DenyMCPServer(name)
	if err := state.Save(m.StateDir, st); err != nil {
		m.scenario.t.Fatalf("save state: %v", err)
	}
	return m
}

// SetPolicy mutates this machine's (category, direction) policy and
// saves state. Fluent.
func (m *Machine) SetPolicy(category string, dir state.Direction, policy string) *Machine {
	m.scenario.t.Helper()
	st, err := state.Load(m.StateDir)
	if err != nil {
		m.scenario.t.Fatalf("load state: %v", err)
	}
	st.SetPolicy(category, dir, policy)
	if err := state.Save(m.StateDir, st); err != nil {
		m.scenario.t.Fatalf("save state: %v", err)
	}
	return m
}

// Sync runs a full real sync.Run and returns the result. Any error
// fails the scenario's test — scenarios that want to assert error state
// should use SyncExpectErr.
func (m *Machine) Sync() sync.Result {
	m.scenario.t.Helper()
	res, err := sync.RunWithRetry(context.Background(), m.inputs(false), nil)
	if err != nil {
		m.scenario.t.Fatalf("machine %s sync: %v", m.Name, err)
	}
	return res
}

// SyncExpectErr runs a full sync and returns (result, error); does NOT
// fail the test on a non-nil error. Use when a scenario is specifically
// asserting that an error is produced.
func (m *Machine) SyncExpectErr() (sync.Result, error) {
	m.scenario.t.Helper()
	return sync.RunWithRetry(context.Background(), m.inputs(false), nil)
}

// DryRun computes the plan without applying it. Returns the Plan so
// tests can inspect action counts, conflict shapes, etc.
func (m *Machine) DryRun() sync.Plan {
	m.scenario.t.Helper()
	in := m.inputs(false)
	in.DryRun = true
	res, err := sync.Run(context.Background(), in, nil)
	if err != nil {
		m.scenario.t.Fatalf("machine %s dry-run: %v", m.Name, err)
	}
	return res.Plan
}

// Promote runs sync.PromotePath on this machine, moving a file in the
// repo worktree from one profile's subtree to another and pushing the
// result. repoRelPath is under the claude/ tree (e.g.
// "claude/agents/foo.md").
func (m *Machine) Promote(repoRelPath, from, to string) {
	m.scenario.t.Helper()
	if err := sync.PromotePath(context.Background(), m.inputs(false), repoRelPath, from, to); err != nil {
		m.scenario.t.Fatalf("machine %s promote: %v", m.Name, err)
	}
}

// SyncAndResolveAll runs a sync; if conflicts are produced, resolves
// them all to the given side (local or remote) and re-applies. Returns
// the final result. Tests that want per-path resolutions should use
// sync.ApplyResolutions directly on the plan.
func (m *Machine) SyncAndResolveAll(choice ResolutionChoice) sync.Result {
	m.scenario.t.Helper()
	res, err := sync.RunWithRetry(context.Background(), m.inputs(false), nil)
	if err != nil {
		m.scenario.t.Fatalf("machine %s sync: %v", m.Name, err)
	}
	if len(res.Plan.Conflicts) == 0 {
		return res
	}
	resolutions := map[string][]byte{}
	for _, fc := range res.Plan.Conflicts {
		switch choice {
		case TakeLocal:
			resolutions[fc.Path] = fc.LocalData
		case TakeRemote:
			resolutions[fc.Path] = fc.RemoteData
		}
	}
	resolved, err := sync.ApplyResolutions(context.Background(), m.inputs(false), resolutions)
	if err != nil {
		m.scenario.t.Fatalf("machine %s resolve: %v", m.Name, err)
	}
	return resolved
}

// ResolutionChoice names the bulk choice in SyncAndResolveAll. Exported
// so callers spell their intent.
type ResolutionChoice int

const (
	TakeLocal ResolutionChoice = iota
	TakeRemote
)

// inputs builds a sync.Inputs targeting this machine's fake dirs, the
// scenario's bare repo, and its profile. Event channel is always nil
// here — tests that want events should build their own Inputs and call
// sync.Run directly.
func (m *Machine) inputs(dryRun bool) sync.Inputs {
	return sync.Inputs{
		Config:      m.scenario.Config,
		Profile:     m.Profile,
		ClaudeDir:   m.ClaudeDir,
		ClaudeJSON:  m.ClaudeJSON,
		RepoPath:    m.RepoPath,
		StateDir:    m.StateDir,
		HostUUID:    m.hostUUID,
		HostName:    m.Name,
		AuthorEmail: m.Name + "@ccsync.test",
		DryRun:      dryRun,
		Auth:        transport.AuthMethod(nil),
	}
}

func mkdirs(paths ...string) error {
	for _, p := range paths {
		if err := mkdirAll(p); err != nil {
			return err
		}
	}
	return nil
}
