package humanize

import "testing"

// TestTildePathTrailingSlashHome pins iter-43 audit fix: TildePath now
// trims trailing separators off `home` before the prefix check. Pre-
// fix, if home came in as "/Users/x/" (possible when $HOME is set
// explicitly with a trailing slash), the prefix test looked for
// "/Users/x//" in abs — never present — and abs was returned
// unabbreviated. The Home-screen dashboard would then render full
// absolute paths all over the UI.
func TestTildePathTrailingSlashHome(t *testing.T) {
	cases := []struct {
		abs, home, want string
	}{
		{"/Users/x/foo", "/Users/x/", "~/foo"},
		{"/Users/x", "/Users/x/", "~"},
		{"/Users/x/foo", "/Users/x", "~/foo"}, // unchanged behavior
		{"/other/path", "/Users/x/", "/other/path"},
	}
	for _, c := range cases {
		if got := TildePath(c.abs, c.home); got != c.want {
			t.Errorf("TildePath(%q, %q) = %q, want %q", c.abs, c.home, got, c.want)
		}
	}
}
