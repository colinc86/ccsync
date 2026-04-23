package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/theme"
)

// SyncSummary is the structured status we surface in the TUI: how many
// paths need to be pushed up, how many pulled down, and how many are
// currently blocked by conflicts. Derived from the cached dry-run plan on
// AppContext; zero-valued when no plan has been computed yet.
type SyncSummary struct {
	Outbound  int // local changes waiting to push
	Inbound   int // remote changes waiting to pull
	Conflicts int // plan.Conflicts — blocks both directions
	Unknown   bool
	Fetching  bool
	LastFetch time.Time
	FetchErr  error
}

// Clean reports whether everything is in sync (no pending pushes, pulls, or
// conflicts). Unknown is NOT clean — we just don't know yet.
func (s SyncSummary) Clean() bool {
	if s.Unknown {
		return false
	}
	return s.Outbound == 0 && s.Inbound == 0 && s.Conflicts == 0
}

// Summary returns the current sync status for the active profile derived
// from the cached plan. Cheap — no I/O.
func (c *AppContext) Summary() SyncSummary {
	if c == nil || c.State == nil || c.State.SyncRepoURL == "" {
		return SyncSummary{Unknown: true}
	}
	s := SyncSummary{
		Fetching:  c.Fetching,
		LastFetch: c.PlanTime,
		FetchErr:  c.PlanErr,
	}
	if c.Plan == nil {
		s.Unknown = true
		return s
	}
	for _, a := range c.Plan.Actions {
		if a.ExcludedByProfile || a.ExcludedByDeny {
			continue
		}
		switch a.Action {
		case manifest.ActionAddRemote, manifest.ActionPush, manifest.ActionDeleteRemote:
			s.Outbound++
		case manifest.ActionAddLocal, manifest.ActionPull, manifest.ActionDeleteLocal:
			s.Inbound++
		case manifest.ActionMerge:
			s.Outbound++
			s.Inbound++
		}
	}
	s.Conflicts = len(c.Plan.Conflicts)
	return s
}

// SummaryBadge returns a compact themed status string suitable for the
// status bar (narrow) or Home dashboard line (a bit more spacious).
// compact=true hides "in sync" and "checking" text, showing just counts
// or a dot glyph — useful for the always-visible status bar.
func SummaryBadge(s SyncSummary, compact bool) string {
	if s.Fetching {
		if compact {
			return theme.Hint.Render("◌ checking")
		}
		return theme.Hint.Render("◌ checking remote…")
	}
	if s.FetchErr != nil {
		if compact {
			return theme.Warn.Render("! fetch failed")
		}
		return theme.Warn.Render("! fetch failed: " + s.FetchErr.Error())
	}
	if s.Unknown {
		return theme.Hint.Render("status unknown — open sync preview to check")
	}
	if s.Clean() {
		return theme.Good.Render("✓ in sync")
	}
	// Build "↑ N push · ↓ M pull · ! K conflict"
	var parts []string
	if s.Outbound > 0 {
		parts = append(parts, theme.Warn.Render(fmt.Sprintf("↑ %d push", s.Outbound)))
	}
	if s.Inbound > 0 {
		parts = append(parts, theme.Warn.Render(fmt.Sprintf("↓ %d pull", s.Inbound)))
	}
	if s.Conflicts > 0 {
		parts = append(parts, theme.Bad.Render(fmt.Sprintf("! %d conflict", s.Conflicts)))
	}
	return strings.Join(parts, " · ")
}
