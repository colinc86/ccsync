package tui

import (
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/bootstrap"
	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
)

// TestBootstrapWizardSuccessTransitionsToProfilePicker guards the
// bootstrap wizard's post-success handoff. A previous regression had
// it push newSyncPreview directly, skipping the profile picker — so
// machine #2+ setups silently joined "default" and pushed over
// whatever was there.
//
// The wizard returns a tea.Sequence(popToRoot, switchTo(picker)) on
// stepDone + enter. We walk the resulting Cmd chain with reflection
// (tea.Sequence's internal msg type is unexported, but it's a slice
// of Cmds) and assert that the switchTo target is a
// *profilePickerModel. If anyone swaps the destination back to
// syncPreview — or anything else — this test fails loudly.
func TestBootstrapWizardSuccessTransitionsToProfilePicker(t *testing.T) {
	ctx := &AppContext{
		State: &state.State{
			LastSyncedSHA: map[string]string{},
		},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		StateDir: t.TempDir(),
		RepoPath: t.TempDir(),
	}
	w := newBootstrapWizard(ctx)
	// Manually drive the wizard to its success state. Simulates
	// "the bootstrap completed and we rendered the 'bootstrapped ✓'
	// screen waiting for a keypress."
	w.step = stepDone
	w.done = &state.State{SyncRepoURL: "git@example.com:x/y.git", Auth: bootstrap.Inputs{}.Auth}

	// User presses enter on the done screen.
	_, cmd := w.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("stepDone + enter should produce a transition Cmd")
	}

	// Walk the sequence. tea.Sequence returns a Cmd whose msg is an
	// unexported []Cmd-ish. We use reflection to get at it.
	seqMsg := cmd()
	subCmds := extractSequenceCmds(t, seqMsg)
	if len(subCmds) < 2 {
		t.Fatalf("expected at least 2 sub-commands (pop + switchTo), got %d", len(subCmds))
	}

	// The LAST command in the sequence should be the switchTo(picker).
	// Pop comes first, picker second.
	lastMsg := subCmds[len(subCmds)-1]()
	sw, ok := lastMsg.(switchScreenMsg)
	if !ok {
		t.Fatalf("last sequence cmd should produce switchScreenMsg; got %T", lastMsg)
	}
	if _, isPicker := sw.s.(*profilePickerModel); !isPicker {
		t.Errorf("bootstrap success should push profilePickerModel; got %T", sw.s)
	}
}

// TestBootstrapGatesAutoSyncUntilProfileChoice pins the fix for the
// v0.8.4 user report: on a fresh install bootstrapping into an
// existing repo, auto-mode fired a background sync under the
// hardcoded "default" profile BEFORE the user was prompted to pick
// or create their own profile. The user's local ~/.claude content
// landed as a commit under default, and when the user then created
// their own profile + tweaked things, they got a tangled state
// where their files lived partly under default (stray commit from
// the auto-sync) and partly under the new profile (from the post-
// picker sync).
//
// The invariant we're pinning: completing the bootstrap wizard sets
// ctx.PendingProfileChoice, and maybeLaunchAutoSync declines to
// fire while that flag is true. The profile picker's finalize
// clears the flag, after which auto-sync works normally.
func TestBootstrapGatesAutoSyncUntilProfileChoice(t *testing.T) {
	ctx := &AppContext{
		State: &state.State{
			SyncRepoURL:   "git@example.com:x/y.git",
			ActiveProfile: "default",
			LastSyncedSHA: map[string]string{},
			// Zero-valued SyncMode resolves to auto — the default
			// for fresh installs and the exact shape the user hit.
		},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		StateDir: t.TempDir(),
		RepoPath: t.TempDir(),
	}
	w := newBootstrapWizard(ctx)
	// Complete the bootstrap step (success path).
	if _, _ = w.Update(bootstrapDoneMsg{st: ctx.State}); ctx.State == nil {
		t.Fatal("setup regressed: bootstrap msg lost state")
	}

	if !ctx.PendingProfileChoice {
		t.Fatal("bootstrap success should set ctx.PendingProfileChoice — the auto-sync gate relies on it to defer the first real sync until the user has picked/created a profile")
	}

	// Simulate the plan refresh that bootstrap or its follow-up
	// kicks off. maybeLaunchAutoSync is the function that would
	// otherwise fire a background sync under "default" right now.
	plan := sync.Plan{Actions: []sync.FileAction{{Path: "profiles/default/claude/CLAUDE.md", Action: manifest.ActionAddRemote}}}
	ctx.Plan = &plan
	ctx.PlanErr = nil
	if cmd := maybeLaunchAutoSync(ctx); cmd != nil {
		t.Fatal("maybeLaunchAutoSync returned a Cmd while PendingProfileChoice was set — the pre-picker auto-sync is exactly the bug we're guarding against")
	}

	// Now clear the flag the way the profile picker's finalize
	// does, and confirm auto-sync resumes.
	ctx.PendingProfileChoice = false
	if cmd := maybeLaunchAutoSync(ctx); cmd == nil {
		t.Error("after profile choice, maybeLaunchAutoSync should resume firing; got nil")
	}
}

// extractSequenceCmds pulls the []Cmd out of a tea.Sequence's returned
// message via reflection. bubbletea defines sequenceMsg as a named
// []Cmd; reflect handles that uniformly.
func extractSequenceCmds(t *testing.T, msg tea.Msg) []tea.Cmd {
	t.Helper()
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		t.Fatalf("expected sequence msg to be a slice; got %s (%T)", v.Kind(), msg)
	}
	out := make([]tea.Cmd, v.Len())
	for i := 0; i < v.Len(); i++ {
		c, ok := v.Index(i).Interface().(tea.Cmd)
		if !ok {
			t.Fatalf("sequence element %d is not a tea.Cmd; got %T", i, v.Index(i).Interface())
		}
		out[i] = c
	}
	return out
}
