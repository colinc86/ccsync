package tui

import (
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/updater"
)

// updateCheckDoneMsg carries the result of a background update check.
type updateCheckDoneMsg struct {
	latest string
	err    error
	at     time.Time
}

// autoInstallDoneMsg fires after an auto-install attempt. Silent to the
// user (per the setting's contract) — AppModel just clears the pending
// flag on success.
type autoInstallDoneMsg struct {
	err error
}

// periodicUpdateCheckTickMsg fires every 24h to kick off another check.
// Separate cadence from the sync plan refresh because they answer
// different questions and we don't want to slam GitHub on every tick.
type periodicUpdateCheckTickMsg struct{}

// updateCheckInterval is the cadence for background update checks. Not
// user-configurable: daily is a sensible default that neither spams nor
// lets stale versions rot for weeks.
const updateCheckInterval = 24 * time.Hour

// checkForUpdateCmd hits GitHub for the latest tag and posts the result.
// Returns nil (no-op) if a check is already in flight recently — we avoid
// double-checking on startup if the timer and init both fire.
func checkForUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		tag, err := updater.LatestTag()
		return updateCheckDoneMsg{latest: tag, err: err, at: time.Now()}
	}
}

// schedulePeriodicUpdateCheck returns a one-shot tick that fires after
// updateCheckInterval. Self-rescheduled on every fire by AppModel.
func schedulePeriodicUpdateCheck() tea.Cmd {
	return tea.Tick(updateCheckInterval, func(time.Time) tea.Msg {
		return periodicUpdateCheckTickMsg{}
	})
}

// autoInstallIfNeeded returns a command that silently installs the
// latest version when (a) update mode is "auto", (b) a newer version is
// available, (c) the current binary isn't Homebrew-managed, and (d) we
// aren't already mid-install. Returns nil in every other case so
// tea.Batch filters it out. The in-flight latch flip happens here so the
// caller doesn't have to remember.
func autoInstallIfNeeded(ctx *AppContext) tea.Cmd {
	if ctx == nil || ctx.State == nil {
		return nil
	}
	if ctx.UpdateInstalling {
		return nil
	}
	if ctx.State.UpdateMode != "auto" {
		return nil
	}
	if !ctx.UpdateAvailable || ctx.LatestVersion == "" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if updater.IsHomebrew(exe) {
		return nil // brew manages this install; don't stomp on it
	}
	ctx.UpdateInstalling = true
	tag := ctx.LatestVersion
	return func() tea.Msg {
		err := updater.InstallRelease(tag, exe)
		return autoInstallDoneMsg{err: err}
	}
}
