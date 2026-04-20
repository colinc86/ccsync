package tui

import (
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/theme"
)

// FreshnessKind classifies how in-sync this machine is with the repo as
// far as ccsync can tell cheaply (without fetching or hashing every file).
type FreshnessKind int

const (
	FreshnessUnknown   FreshnessKind = iota // no repo yet / can't tell
	FreshnessNever                          // bootstrapped but never synced
	FreshnessUpToDate                       // local HEAD == last-synced SHA
	FreshnessUnsynced                       // repo HEAD has moved since last sync
)

// Freshness returns a cheap sync-freshness signal for the active profile.
// Does NOT fetch from the remote, so "up to date" means "nothing new in the
// local clone since we last synced". To detect remote drift, the user runs
// a sync.
func Freshness(ctx *AppContext) FreshnessKind {
	if ctx == nil || ctx.State == nil || ctx.State.SyncRepoURL == "" {
		return FreshnessUnknown
	}
	profile := ctx.State.ActiveProfile
	if profile == "" {
		profile = "default"
	}
	last := ctx.State.LastSyncedSHA[profile]
	if last == "" {
		return FreshnessNever
	}
	repo, err := gitx.Open(ctx.RepoPath)
	if err != nil {
		return FreshnessUnknown
	}
	head, err := repo.HeadSHA()
	if err != nil || head == "" {
		return FreshnessUnknown
	}
	if head == last {
		return FreshnessUpToDate
	}
	return FreshnessUnsynced
}

// FreshnessBadge returns a compact themed string suitable for inline
// rendering in the Home dashboard or the status bar.
func FreshnessBadge(k FreshnessKind) string {
	switch k {
	case FreshnessUpToDate:
		return theme.Good.Render("✓ up to date")
	case FreshnessUnsynced:
		return theme.Warn.Render("⟲ unsynced commits — run sync")
	case FreshnessNever:
		return theme.Hint.Render("never synced on this machine")
	}
	return ""
}
