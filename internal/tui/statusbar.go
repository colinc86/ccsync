package tui

import (
	"fmt"
	"strings"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/theme"
)

// statusBar renders a single-line bottom line of secondary context:
// host class, exclude counts, missing-repo warnings. The active
// profile + freshness badge live in the app-shell header now, so the
// status bar carries only the information the header doesn't —
// local-machine config that's invisible but consequential at sync
// time. Returns empty when there's nothing to report, so the app
// shell doesn't reserve a blank line for noise.
func statusBar(ctx *AppContext) string {
	if ctx == nil {
		return ""
	}
	profile := ctx.State.ActiveProfile
	if profile == "" {
		profile = "default"
	}

	var parts []string
	if ctx.State.HostClass != "" {
		parts = append(parts, "host-class: "+theme.Secondary.Render(ctx.State.HostClass))
	}
	if resolved, err := config.EffectiveProfile(ctx.Config, profile); err == nil {
		if n := len(resolved.PathExcludes); n > 0 {
			parts = append(parts, fmt.Sprintf("excludes: %s", theme.Warn.Render(fmt.Sprintf("%d", n))))
		}
	}
	if n := len(ctx.State.DeniedPaths); n > 0 {
		parts = append(parts, fmt.Sprintf("denied: %s", theme.Warn.Render(fmt.Sprintf("%d", n))))
	}
	if ctx.State.SyncRepoURL == "" {
		parts = append(parts, theme.Warn.Render("no repo configured"))
	}
	if len(parts) == 0 {
		return ""
	}
	return theme.Hint.Render(strings.Join(parts, " • "))
}
