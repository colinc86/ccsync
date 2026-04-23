// Package humanize formats numbers and words for user-facing text.
// Small utility kept out of theme (which is for styling) and tui
// (which shouldn't be imported by headless subcommands). Used by
// both the TUI and headless entry points so "3 file(s)" style
// grammar warts don't leak anywhere.
package humanize

import (
	"fmt"
	"strings"
)

// Plural returns singular when n == 1 and singular+"s" otherwise.
// English irregulars aren't handled — ccsync's nouns are all regular
// (file, conflict, snapshot, secret, path, rule, suggestion, key, dir,
// profile, change, commit). If that stops being true, pass the correct
// plural form via PluralForm instead.
func Plural(n int, singular string) string {
	if n == 1 {
		return singular
	}
	return singular + "s"
}

// PluralForm is Plural with an explicit plural form, for irregulars.
// E.g. PluralForm(n, "leaf", "leaves"). Prefer Plural for the common
// case.
func PluralForm(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// Count returns "<n> <singular-or-plural>", the most common shape used
// across the TUI and headless output. E.g. Count(1, "conflict") ==
// "1 conflict"; Count(3, "file") == "3 files".
func Count(n int, singular string) string {
	return fmt.Sprintf("%d %s", n, Plural(n, singular))
}

// Truncate shortens s to at most max display columns, appending `…`
// when it clips. Measured in runes, not bytes, so multi-byte paths
// (unicode folder names) clip at the right grapheme boundary. max < 1
// just returns `…`; if s already fits, it's returned unchanged.
func Truncate(s string, max int) string {
	if max < 1 {
		return "…"
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// Join builds a comma-and-"and"-separated list from items. Empty slice
// → "". One item → item. Two items → "a and b". More → "a, b, and c".
// Used for "N files, N conflicts, and N secrets" style status lines.
func Join(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}
