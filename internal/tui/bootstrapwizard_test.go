package tui

import (
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/bootstrap"
	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/state"
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
