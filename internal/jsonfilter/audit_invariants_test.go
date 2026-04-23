package jsonfilter

import (
	"strings"
	"testing"
)

// TestCompileRejectsEmptySegments pins iter-43 audit: Compile
// rejects dangling-dot patterns ("$.", "$..") with clear errors.
// These would be trivial typos in hand-edited ccsync.yaml rules;
// reject early rather than produce a pattern that matches in
// surprising ways. This test is a pinned invariant so a future
// refactor can't accidentally loosen the parser.
func TestCompileRejectsEmptySegments(t *testing.T) {
	cases := []string{"$.", "$..", "$.foo.", "$..."}
	for _, pat := range cases {
		_, err := Compile(pat)
		if err == nil {
			t.Errorf("Compile(%q): expected error for empty segment", pat)
			continue
		}
		if !strings.Contains(err.Error(), "expected name") && !strings.Contains(err.Error(), "unexpected") {
			t.Errorf("Compile(%q): error msg %q should mention expected-name or unexpected-char", pat, err)
		}
	}
	// Control: "$" alone is valid (root-match pattern).
	if _, err := Compile("$"); err != nil {
		t.Errorf("Compile($) should succeed: %v", err)
	}
}
