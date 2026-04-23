// Package harness is a test fixture for ccsync that simulates a multi-
// machine sync scenario entirely in-process: a single bare git repo,
// multiple isolated "machines" each with their own $HOME and
// ~/.ccsync state, a shared in-memory keyring. Tests written against
// this harness exercise real sync.Run, real go-git, real jsonfilter —
// the only thing fake is the filesystem layout and the secrets backend.
//
// The point: every cross-machine bug we've hit in v0.3.x should be
// reproducible as a deterministic test case here, so the fix-then-ship
// cycle doesn't depend on the user being patient enough to hit it
// again on a real second machine.
package harness

import (
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/secrets"
)

// Scenario owns the tmp root, the bare repo every machine talks to, and
// the shared Config. All file paths under a scenario live under t.TempDir
// so cleanup is automatic.
type Scenario struct {
	t       *testing.T
	Root    string
	BareDir string
	Config  *config.Config
}

// ScenarioOption customises a Scenario at construction time. Used for
// setting up additional profiles, host classes, etc. before any machine
// is created.
type ScenarioOption func(*Scenario)

// WithProfiles overrides / extends the default profile map. The passed
// map is merged into the loaded config, so "default" remains unless the
// caller overrides it explicitly.
func WithProfiles(profiles map[string]config.ProfileSpec) ScenarioOption {
	return func(s *Scenario) {
		if s.Config.Profiles == nil {
			s.Config.Profiles = map[string]config.ProfileSpec{}
		}
		for name, p := range profiles {
			s.Config.Profiles[name] = p
		}
	}
}

// WithConfig lets tests mutate the loaded default config directly —
// useful for adding one-off JSON rules, syncignore patches, etc.
func WithConfig(mutate func(*config.Config)) ScenarioOption {
	return func(s *Scenario) { mutate(s.Config) }
}

// NewScenario initializes a fresh sandbox: a temp dir, an empty bare
// git repo at {tmp}/bare.git, the default config, an in-memory
// keychain. Each sub-test of the caller should get its own Scenario.
func NewScenario(t *testing.T, opts ...ScenarioOption) *Scenario {
	t.Helper()
	secrets.MockInit()

	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	br, err := gogit.PlainInit(bare, true)
	if err != nil {
		t.Fatalf("bare init: %v", err)
	}
	// Point the bare's HEAD at refs/heads/main so a later Clone
	// resolves cleanly after our first push — go-git's PlainInit
	// defaults to master, but ccsync pushes to main (gitx.DefaultBranch),
	// and a HEAD→master pointer to a nonexistent ref would leave
	// clone callers with "reference not found."
	headRef := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(gitx.DefaultBranch))
	if err := br.Storer.SetReference(headRef); err != nil {
		t.Fatalf("set bare HEAD: %v", err)
	}

	cfg, err := config.LoadDefault()
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}

	s := &Scenario{t: t, Root: root, BareDir: bare, Config: cfg}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// T returns the *testing.T the scenario was created with — lets assertion
// helpers on Machine fail the correct test without threading it through
// every call.
func (s *Scenario) T() *testing.T { return s.t }

// BareHead returns the current origin/master SHA of the bare repo. ""
// when there are no commits yet (empty repo on first invocation).
func (s *Scenario) BareHead() string {
	s.t.Helper()
	r, err := gogit.PlainOpen(s.BareDir)
	if err != nil {
		s.t.Fatalf("open bare: %v", err)
	}
	ref, err := r.Reference("refs/heads/"+gitx.DefaultBranch, true)
	if err != nil {
		return ""
	}
	return ref.Hash().String()
}

// BareFile reads a path from the bare repo's HEAD tree. Returns ok=false
// when the path isn't present. Used to assert what was / wasn't pushed.
func (s *Scenario) BareFile(path string) ([]byte, bool) {
	s.t.Helper()
	r, err := gogit.PlainOpen(s.BareDir)
	if err != nil {
		s.t.Fatalf("open bare: %v", err)
	}
	ref, err := r.Reference("refs/heads/"+gitx.DefaultBranch, true)
	if err != nil {
		return nil, false
	}
	c, err := r.CommitObject(ref.Hash())
	if err != nil {
		return nil, false
	}
	f, err := c.File(path)
	if err != nil {
		return nil, false
	}
	b, err := f.Contents()
	if err != nil {
		return nil, false
	}
	return []byte(b), true
}

// BareCommits returns the commit log on master, newest first, each
// element is a one-liner "<short-sha> <subject>". Used by tests that
// want to assert no no-op commit was made, or that the expected number
// of syncs produced the expected number of commits.
func (s *Scenario) BareCommits() []string {
	s.t.Helper()
	r, err := gogit.PlainOpen(s.BareDir)
	if err != nil {
		s.t.Fatalf("open bare: %v", err)
	}
	ref, err := r.Reference("refs/heads/"+gitx.DefaultBranch, true)
	if err != nil {
		return nil
	}
	iter, err := r.Log(&gogit.LogOptions{From: ref.Hash()})
	if err != nil {
		return nil
	}
	var out []string
	_ = iter.ForEach(func(c *object.Commit) error {
		subject := c.Message
		if len(subject) > 60 {
			subject = subject[:60]
		}
		short := c.Hash.String()
		if len(short) > 7 {
			short = short[:7]
		}
		out = append(out, short+" "+subject)
		return nil
	})
	return out
}
