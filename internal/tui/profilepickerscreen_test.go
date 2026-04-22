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
func newTestPickerCtx(t *testing.T, profiles []string, activeProfile string) *AppContext {
	t.Helper()
	cfg := &config.Config{Profiles: map[string]config.ProfileSpec{}}
	for _, p := range profiles {
		cfg.Profiles[p] = config.ProfileSpec{Description: p + " profile"}
	}
	return &AppContext{
		State: &state.State{
			ActiveProfile: activeProfile,
			LastSyncedSHA: map[string]string{},
		},
		Config:   cfg,
		StateDir: t.TempDir(),
		RepoPath: filepath.Join(t.TempDir(), "repo"),
	}
}

// TestProfilePickerNeverAutoAdvances is the regression test for the
// v0.6.0/v0.6.1 bug where the picker short-circuited past the user
// when there was only a "default" profile AND ActiveProfile was set
// — exactly the shape a cloned repo + freshly-bootstrapped machine
// presents. The picker silently joined default, stranding the user
// on the wrong profile. This test fails if the short-circuit comes
// back: Init() must return nil (no auto-finish command) AND View()
// must render the picker UI.
func TestProfilePickerNeverAutoAdvances(t *testing.T) {
	cases := []struct {
		name          string
		profiles      []string
		activeProfile string
	}{
		{"single default; no active profile yet",
			[]string{"default"}, ""},
		{"single default; active already set to default (post-bootstrap)",
			[]string{"default"}, "default"},
		{"multiple profiles; no active",
			[]string{"default", "home"}, ""},
		{"multiple profiles; active is one of them",
			[]string{"default", "home"}, "home"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestPickerCtx(t, tc.profiles, tc.activeProfile)
			m := newProfilePickerScreen(ctx)

			// Init must return nil. Any non-nil Cmd here is a code
			// smell — the picker's only job at Init time is to render
			// a choice. The v0.6.0/v0.6.1 auto-advance bug was
			// exactly this: Init returned a Cmd that produced an
			// autoFinishMsg, skipping the user entirely.
			if cmd := m.Init(); cmd != nil {
				t.Fatalf("picker Init returned a non-nil Cmd; user may never see the picker (this is the v0.6.0/v0.6.1 auto-advance regression)")
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

// TestProfilePickerEnterOnExistingActivates drives the happy-path:
// user presses enter on the highlighted row, state.ActiveProfile
// updates, OnboardingComplete flips true, and the transition command
// fires.
func TestProfilePickerEnterOnExistingActivates(t *testing.T) {
	ctx := newTestPickerCtx(t, []string{"default", "home"}, "")
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
	ctx := newTestPickerCtx(t, []string{"default"}, "default")
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
