package ignore

import "testing"

func TestMatcher(t *testing.T) {
	rules := `
# comment line
projects/
*.log
!important.log
session-*/
`
	m := New(rules)

	cases := []struct {
		path string
		want bool
	}{
		{"projects/foo.txt", true},
		{"projects/", true},
		{"agents/foo.md", false},
		{"debug.log", true},
		{"important.log", false},
		{"session-abc/file", true},
	}
	for _, tc := range cases {
		if got := m.Matches(tc.path); got != tc.want {
			t.Errorf("Matches(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestMatcherNil(t *testing.T) {
	var m *Matcher
	if m.Matches("anything") {
		t.Fatal("nil matcher should match nothing")
	}
}

func TestMatcherEmpty(t *testing.T) {
	m := New("")
	if m.Matches("anything") {
		t.Fatal("empty matcher should match nothing")
	}
}
