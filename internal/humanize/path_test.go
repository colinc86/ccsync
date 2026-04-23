package humanize

import (
	"path/filepath"
	"testing"
)

func TestTildePath(t *testing.T) {
	home := filepath.Join("/Users", "colin")
	cases := []struct {
		name string
		abs  string
		home string
		want string
	}{
		{"exact home", home, home, "~"},
		{"file under home", filepath.Join(home, ".claude"), home, filepath.FromSlash("~/.claude")},
		{"deep under home", filepath.Join(home, ".claude", "agents", "foo.md"), home, filepath.FromSlash("~/.claude/agents/foo.md")},
		{"unrelated path", "/etc/passwd", home, "/etc/passwd"},
		{"path is prefix but not dir", "/Users/colinother", home, "/Users/colinother"},
		{"empty home", "/tmp/x", "", "/tmp/x"},
		{"empty abs", "", home, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TildePath(c.abs, c.home)
			if got != c.want {
				t.Errorf("TildePath(%q, %q) = %q, want %q", c.abs, c.home, got, c.want)
			}
		})
	}
}

// TestUserTildePathFallback exercises the UserHomeDir failure path.
// On unix we can force it to fail by unsetting HOME, which makes
// os.UserHomeDir return an error. UserTildePath should then return
// its input unchanged rather than panicking or returning "".
func TestUserTildePathFallback(t *testing.T) {
	t.Setenv("HOME", "")
	// Some platforms also read PWD as a fallback; clear both so
	// os.UserHomeDir is forced to the error path on macOS/Linux.
	got := UserTildePath("/some/path")
	if got != "/some/path" {
		t.Errorf("UserTildePath with empty HOME = %q, want unchanged", got)
	}
}
