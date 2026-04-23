package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/state"
)

// TestUpdateSuccessSchedulesRestart pins the auto-restart contract
// after a successful self-install: the screen flips to the
// restarting step, writes the exec target into ctx.RestartBinaryPath
// so main() can syscall.Exec after the TUI shuts down, and returns
// a non-nil Cmd (the tea.Tick that fires the eventual tea.Quit).
// If any of these three drop, the auto-restart silently reverts to
// the pre-v0.8.3 "press any key to return" behaviour and users have
// to manually relaunch to see the new binary — the bug we're
// guarding against.
func TestUpdateSuccessSchedulesRestart(t *testing.T) {
	ctx := &AppContext{State: &state.State{}}
	m := &updateScreenModel{
		ctx:     ctx,
		current: "v0.8.2",
		latest:  "v0.8.3",
		exePath: "/tmp/fake-ccsync-binary",
		step:    updateStepInstalling,
	}
	updated, cmd := m.Update(updateInstallDoneMsg{err: nil})
	um := updated.(*updateScreenModel)

	if um.step != updateStepRestarting {
		t.Fatalf("step = %v, want updateStepRestarting — success should route through the restart branch, not Done", um.step)
	}
	if ctx.RestartBinaryPath != "/tmp/fake-ccsync-binary" {
		t.Errorf("ctx.RestartBinaryPath = %q, want /tmp/fake-ccsync-binary — main() reads this field after tea.Run returns to pick the exec target",
			ctx.RestartBinaryPath)
	}
	if cmd == nil {
		t.Error("no Cmd returned; the tea.Tick scheduling the restart quit is missing, so the TUI will sit on the success card forever")
	}
}

// TestUpdateFailureDoesNotRestart pins that an install error path
// doesn't accidentally arm the auto-restart — main() would then
// syscall.Exec into whatever exePath we had, possibly a bad write
// from the half-successful install.
func TestUpdateFailureDoesNotRestart(t *testing.T) {
	ctx := &AppContext{State: &state.State{}}
	m := &updateScreenModel{
		ctx:     ctx,
		exePath: "/tmp/fake-ccsync-binary",
		step:    updateStepInstalling,
	}
	updated, _ := m.Update(updateInstallDoneMsg{err: errFake("download failed")})
	um := updated.(*updateScreenModel)

	if um.step != updateStepDone {
		t.Errorf("step = %v, want updateStepDone — a failed install stays on the done-with-error card for the user to acknowledge", um.step)
	}
	if ctx.RestartBinaryPath != "" {
		t.Errorf("ctx.RestartBinaryPath = %q, want empty — main() would otherwise try to exec into a possibly-broken binary", ctx.RestartBinaryPath)
	}
}

// TestUpdateRestartTickQuits pins that restartTickMsg returns a
// tea.Quit command — the signal that hands control back to main()
// for the syscall.Exec. Anything else (nil, popToRoot, etc.) would
// leave the TUI alive and the new binary never takes over.
func TestUpdateRestartTickQuits(t *testing.T) {
	ctx := &AppContext{State: &state.State{}}
	m := &updateScreenModel{ctx: ctx, step: updateStepRestarting}
	_, cmd := m.Update(restartTickMsg{})
	if cmd == nil {
		t.Fatal("restart tick produced no Cmd; expected tea.Quit to shut down the TUI and return to main() for re-exec")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("Cmd executed to nil msg; expected tea.QuitMsg")
	} else if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("Cmd returned %T; expected tea.QuitMsg so main() can run the syscall.Exec after p.Run returns", msg)
	}
}

// errFake is a minimal error type for the failure-path test — avoids
// importing errors.New inline and keeps the test self-contained.
type errFake string

func (e errFake) Error() string { return string(e) }
