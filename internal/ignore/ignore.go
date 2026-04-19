// Package ignore wraps the gitignore matcher used for .syncignore files.
package ignore

import (
	"strings"

	ign "github.com/sabhiram/go-gitignore"
)

type Matcher struct {
	gi *ign.GitIgnore
}

// New compiles gitignore-syntax rules (one per line) into a Matcher.
// A nil or empty rules string yields a matcher that ignores nothing.
func New(rules string) *Matcher {
	lines := strings.Split(rules, "\n")
	return &Matcher{gi: ign.CompileIgnoreLines(lines...)}
}

// Matches reports whether the given slash-separated path is ignored.
// Directories should be passed with a trailing "/" so dir-only patterns match.
func (m *Matcher) Matches(path string) bool {
	if m == nil || m.gi == nil {
		return false
	}
	return m.gi.MatchesPath(path)
}
