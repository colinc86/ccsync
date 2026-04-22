package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/state"
)

// newTestPickerCtx builds a minimal AppContext good enough for the
// picker's constructor + Init + View. Uses t.TempDir for StateDir so
// any state.Save calls from the test don't collide with the real
// ~/.ccsync.
//
// withContent controls whether the repo worktree has pre-existing
// content under profiles/<active>/claude/ — the signal the picker
// uses to distinguish "machine #1 fresh repo" (no content, should
// auto-advance) from "machine #2 joining" (has content, must show
// picker).
func newTestPickerCtx(t *testing.T, profiles []string, activeProfile string, withContent bool) *AppContext {
	t.Helper()
	cfg := &config.Config{Profiles: map[string]config.ProfileSpec{}}
	for _, p := range profiles {
		cfg.Profiles[p] = config.ProfileSpec{Description: p + " profile"}
	}
	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if withContent {
		active := activeProfile
		if active == "" && len(profiles) > 0 {
			active = profiles[0]
		}
		if active == "" {
			active = "default"
		}
		// Simulate content from another machine's prior syncs.
		subtree := filepath.Join(repoPath, "profiles", active, "claude")
		if err := os.MkdirAll(subtree, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subtree, "CLAUDE.md"), []byte("existing"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &AppContext{
		State: &state.State{
			ActiveProfile: activeProfile,
			LastSyncedSHA: map[string]string{},
		},
		Config:   cfg,
		StateDir: t.TempDir(),
		RepoPath: repoPath,
	}
}

// TestProfilePickerShowsPickerWhenContentExists covers the
// machine #2+ case: a repo with pre-existing profile content means
// the user is joining something that was set up elsewhere, so they
// need the picker to decide between joining as-is or creating a new
// profile. The v0.6.0/v0.6.1 regression silently skipped this exact
// case. This test fails loudly if that short-circuit comes back.
func TestProfilePickerShowsPickerWhenContentExists(t *testing.T) {
	cases := []struct {
		name          string
		profiles      []string
		activeProfile string
	}{
		{"single default with content; no active yet",
			[]string{"default"}, ""},
		{"single default with content; active already default (post-bootstrap)",
			[]string{"default"}, "default"},
		{"multiple profiles; no active",
			[]string{"default", "home"}, ""},
		{"multiple profiles; active is one of them",
			[]string{"default", "home"}, "home"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestPickerCtx(t, tc.profiles, tc.activeProfile, true /*withContent*/)
			m := newProfilePickerScreen(ctx)

			if cmd := m.Init(); cmd != nil {
				t.Fatalf("picker Init returned a non-nil Cmd when repo has existing content; user may never see the picker (v0.6.0/v0.6.1 regression shape)")
			}
			view := m.View()
			if !strings.Contains(view, "create a new profile") {
				t.Errorf("picker view missing create-new-profile affordance; user can't make a second profile.\nview:\n%s", view)
			}
			if !strings.Contains(strings.ToLower(view), "pick") && !strings.Contains(view, "create") {
				t.Errorf("picker view doesn't present a choice to the user; got:\n%s", view)
			}
		})
	}
}

// TestProfilePickerAutoAdvancesOnFreshlyBootstrappedRepo covers the
// inverse case: user just created a brand-new repo with `ccsync
// bootstrap`; ccsync.yaml has just "default" and no content lives
// under profiles/default/claude/ yet. Showing the picker here would
// frame a "choice" the user didn't make — the profile was created by
// their own bootstrap seconds ago. We auto-advance instead.
func TestProfilePickerAutoAdvancesOnFreshlyBootstrappedRepo(t *testing.T) {
	ctx := newTestPickerCtx(t, []string{"default"}, "default", false /*no content*/)
	m := newProfilePickerScreen(ctx)
	if !m.autoJoin {
		t.Error("fresh-bootstrap repo should auto-join; user shouldn't be asked to pick from a list of one they just created")
	}
	// Init must return a non-nil Cmd carrying the auto-join signal.
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("auto-join mode should return a Cmd from Init so Update can finalize")
	}
	msg := cmd()
	if _, ok := msg.(autoJoinMsg); !ok {
		t.Errorf("fresh-bootstrap Init should emit autoJoinMsg; got %T", msg)
	}
}

// TestProfilePickerEnterOnExistingActivates drives the happy-path:
// user presses enter on the highlighted row, state.ActiveProfile
// updates, OnboardingComplete flips true, and the transition command
// fires.
func TestProfilePickerEnterOnExistingActivates(t *testing.T) {
	ctx := newTestPickerCtx(t, []string{"default", "home"}, "", true)
	m := newProfilePickerScreen(ctx)
	// Cursor defaults to 0 (first name in sorted order = "default").
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should produce a persistence command")
	}
	// Execute the persistence command.
	msg := cmd()
	newModel2, _ := newModel.(*profilePickerModel).Update(msg)
	pm := newModel2.(*profilePickerModel)
	if pm.err != nil {
		t.Fatalf("unexpected persistence error: %v", pm.err)
	}
	if ctx.State.ActiveProfile != "default" {
		t.Errorf("ActiveProfile = %q, want default", ctx.State.ActiveProfile)
	}
	if !ctx.State.OnboardingComplete {
		t.Error("OnboardingComplete should flip true after a successful pick")
	}
}

// TestProfilePickerCreateNewProfile drives the 'n' path: press n,
// type a name, press enter. Verifies a new profile is created in the
// config with extends set to the first existing profile, that it's
// activated, and OnboardingComplete flips.
func TestProfilePickerCreateNewProfile(t *testing.T) {
	ctx := newTestPickerCtx(t, []string{"default"}, "default", true)
	// Seed the repo path with a ccsync.yaml so profile.Create can save.
	if err := writeCCSyncYamlForTest(ctx); err != nil {
		t.Fatal(err)
	}

	m := newProfilePickerScreen(ctx)
	// Enter the create sub-view.
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	_ = cmd // textinput.Blink — not needed here
	m = newModel.(*profilePickerModel)
	if !m.creating {
		t.Fatal("pressing n should switch to creating mode")
	}
	// Type "work" into the textinput by driving runes through.
	for _, r := range "work" {
		newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = newModel.(*profilePickerModel)
	}
	// Press enter to submit.
	newModel, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newModel.(*profilePickerModel)
	if cmd == nil {
		t.Fatal("submitting new-profile name should produce a persistence command")
	}
	// Execute the persistence command.
	msg := cmd()
	newModel, _ = m.Update(msg)
	m = newModel.(*profilePickerModel)
	if m.err != nil {
		t.Fatalf("create profile failed: %v", m.err)
	}

	// Verify: a "work" profile now exists in the config, extending default.
	spec, ok := ctx.Config.Profiles["work"]
	if !ok {
		t.Fatal("work profile wasn't created in config")
	}
	if spec.Extends != "default" {
		t.Errorf("work profile Extends = %q, want default", spec.Extends)
	}
	if ctx.State.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want work", ctx.State.ActiveProfile)
	}
	if !ctx.State.OnboardingComplete {
		t.Error("OnboardingComplete should flip after a successful create")
	}
}

// writeCCSyncYamlForTest seeds the ctx's RepoPath with an
// approximate ccsync.yaml so profile.Create's SaveWithBackup can
// succeed. Minimal — just enough that SaveWithBackup doesn't error
// on a missing parent dir.
func writeCCSyncYamlForTest(ctx *AppContext) error {
	if err := os.MkdirAll(ctx.RepoPath, 0o755); err != nil {
		return err
	}
	return ctx.Config.SaveWithBackup(ctx.ConfigPath())
}
