package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/sync"
)

// planRefreshStartedMsg is emitted just before a background refresh runs;
// AppModel.Update flips ctx.Fetching = true so the status bar shows the
// checking-remote hint immediately, not after the fetch finishes.
type planRefreshStartedMsg struct{}

// planRefreshDoneMsg carries a freshly-computed plan back to AppModel.
type planRefreshDoneMsg struct {
	plan *sync.Plan
	err  error
	at   time.Time
}

// periodicRefreshTickMsg fires on the user's configured FetchInterval.
// AppModel kicks off another refresh and re-schedules the next tick. The
// embedded gen matches AppContext.TickGen at schedule time — stale ticks
// (from a previous interval the user has since changed) are dropped.
type periodicRefreshTickMsg struct{ gen int }

// refreshPlanCmd runs a dry-run against the active profile in the
// background and posts the resulting plan via planRefreshDoneMsg. Skipped
// silently when the repo isn't bootstrapped yet.
func refreshPlanCmd(ctx *AppContext) tea.Cmd {
	if ctx == nil || ctx.State == nil || ctx.State.SyncRepoURL == "" {
		return nil
	}
	return tea.Batch(
		func() tea.Msg { return planRefreshStartedMsg{} },
		func() tea.Msg {
			in, err := buildSyncInputs(ctx, true)
			if err != nil {
				return planRefreshDoneMsg{err: err, at: time.Now()}
			}
			res, err := sync.Run(context.Background(), in, nil)
			if err != nil {
				return planRefreshDoneMsg{err: err, at: time.Now()}
			}
			p := res.Plan
			return planRefreshDoneMsg{plan: &p, at: time.Now()}
		},
	)
}

// schedulePeriodicRefresh returns a tea.Tick that fires once after the
// configured FetchInterval and delivers periodicRefreshTickMsg stamped with
// the current generation. Returns nil when the user has opted out.
func schedulePeriodicRefresh(ctx *AppContext) tea.Cmd {
	if ctx == nil || ctx.State == nil {
		return nil
	}
	d := ctx.State.FetchIntervalDuration()
	if d <= 0 {
		return nil
	}
	gen := ctx.TickGen
	return tea.Tick(d, func(time.Time) tea.Msg { return periodicRefreshTickMsg{gen: gen} })
}
