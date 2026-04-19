package sync

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/colinc86/ccsync/internal/state"
)

// writeRepoREADME regenerates README.md at the repo root. It's a human-facing
// snapshot of what the repo contains; never read by ccsync itself.
func writeRepoREADME(repoPath string, profiles []string, st *state.State, hostName string) error {
	sort.Strings(profiles)

	var sb strings.Builder
	sb.WriteString("# ccsync sync repo\n\n")
	sb.WriteString("This repo is managed by [ccsync](https://github.com/colinc86/ccsync). It stores\n")
	sb.WriteString("one or more snapshots of a user's Claude Code configuration, redacted so\n")
	sb.WriteString("secrets (API keys, OAuth tokens) live only in each machine's OS keychain.\n\n")

	sb.WriteString("## Profiles\n\n")
	if len(profiles) == 0 {
		sb.WriteString("_(no profiles yet — run `ccsync sync` to populate)_\n\n")
	}
	for _, p := range profiles {
		sb.WriteString("- `" + p + "`\n")
	}

	sb.WriteString("\n## Last sync\n\n")
	fmt.Fprintf(&sb, "- **host:** %s\n", hostName)
	activeProfile := "(unknown)"
	if st != nil {
		activeProfile = st.ActiveProfile
	}
	fmt.Fprintf(&sb, "- **active profile:** %s\n", activeProfile)
	fmt.Fprintf(&sb, "- **time:** %s UTC\n", time.Now().UTC().Format(time.RFC3339))

	sb.WriteString("\n## What's safe to edit by hand\n\n")
	sb.WriteString("- `.syncignore` — gitignore-syntax rules for what ccsync sends up\n")
	sb.WriteString("- `ccsync.yaml` — JSON include/exclude/redact rules per file\n\n")
	sb.WriteString("Everything under `profiles/<name>/` is auto-generated. If you hand-edit a\n")
	sb.WriteString("profile file, the next ccsync run will surface that as a three-way conflict\n")
	sb.WriteString("— no silent clobber.\n")

	return writeFileAtomic(filepath.Join(repoPath, "README.md"), []byte(sb.String()))
}
