package humanize

import (
	"os"
	"path/filepath"
	"strings"
)

// TildePath abbreviates an absolute path under `home` to "~/…". Returns
// abs unchanged when home is empty or abs doesn't live under home.
// Display-only — the TUI renders tidier paths ("~/.claude/agents/foo")
// without the sync engine ever seeing anything but absolute paths.
//
// Exact match (abs == home) returns "~"; anything else becomes
// "~" + the path tail (with the leading separator preserved). On
// Windows the separator is "\", which is fine — the abbreviation is
// visual, not parsed downstream.
func TildePath(abs, home string) string {
	if home == "" || abs == "" {
		return abs
	}
	sep := string(filepath.Separator)
	// Normalize trailing separator on home — os.UserHomeDir can
	// return "/Users/x/" when $HOME is explicitly set with a
	// trailing slash. Without this trim, home+"/" below matches
	// "/Users/x//" which never occurs in abs, so the prefix check
	// silently fails and abbreviation never happens.
	home = strings.TrimRight(home, sep)
	if abs == home {
		return "~"
	}
	if strings.HasPrefix(abs, home+sep) {
		return "~" + abs[len(home):]
	}
	return abs
}

// UserTildePath is TildePath with home resolved via os.UserHomeDir().
// Resolution failures fall back to returning abs unchanged, so a broken
// env never turns into a broken UI. Prefer this in TUI code; reserve
// TildePath for tests that want to pin the home value.
func UserTildePath(abs string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return abs
	}
	return TildePath(abs, home)
}
