package tui

import (
	"fmt"
	"strings"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/theme"
)

// statusBar renders a single-line bottom status line. Always shows active
// profile + whether a repo is configured; optional extras (host class,
// exclude count, hint) filled in automatically from ctx.
func statusBar(ctx *AppContext) string {
	if ctx == nil {
		return ""
	}
	profile := ctx.State.ActiveProfile
	if profile == "" {
		profile = "(none)"
	}

	parts := []string{"profile: " + theme.Primary.Render(profile)}

	if ctx.State.HostClass != "" {
		parts = append(parts, "host-class: "+theme.Secondary.Render(ctx.State.HostClass))
	}

	// Show profile exclude count to make host-class filtering visible.
	if resolved, err := config.EffectiveProfile(ctx.Config, profile); err == nil {
		if n := len(resolved.PathExcludes); n > 0 {
			parts = append(parts, fmt.Sprintf("excludes: %s", theme.Warn.Render(fmt.Sprintf("%d", n))))
		}
	}

	if ctx.State.SyncRepoURL == "" {
		parts = append(parts, theme.Warn.Render("no repo"))
	}
	// The freshness badge used to live here too; as of v0.3 it's rendered
	// in the top header by AppModel.View so there's a single source of
	// truth for "are we in sync" at a glance.

	return theme.Hint.Render(strings.Join(parts, " • "))
}
