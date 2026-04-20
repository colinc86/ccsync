// Package jsonfilter applies include/exclude/redact rules to JSON documents.
// Pattern syntax: JSONPath-lite — supports `$`, `.key`, `.*`, `..key`, `..*`.
package jsonfilter

import (
	"errors"
	"fmt"
	"strings"
)

type segment struct {
	Recursive bool   // true for ".."
	Wildcard  bool   // true for "*"
	Key       string // literal key (empty if wildcard)
}

// Pattern is a compiled JSONPath-lite expression.
type Pattern struct {
	segs []segment
	raw  string
}

// Compile parses a pattern like "$.foo.*.bar" or "$..key".
func Compile(pattern string) (*Pattern, error) {
	if !strings.HasPrefix(pattern, "$") {
		return nil, fmt.Errorf("pattern must start with $: %q", pattern)
	}
	rest := pattern[1:]
	var segs []segment
	for len(rest) > 0 {
		switch {
		case strings.HasPrefix(rest, ".."):
			rest = rest[2:]
			if strings.HasPrefix(rest, "*") {
				segs = append(segs, segment{Recursive: true, Wildcard: true})
				rest = rest[1:]
			} else {
				name, after := readName(rest)
				if name == "" {
					return nil, errors.New("expected name after '..'")
				}
				segs = append(segs, segment{Recursive: true, Key: name})
				rest = after
			}
		case strings.HasPrefix(rest, "."):
			rest = rest[1:]
			if strings.HasPrefix(rest, "*") {
				segs = append(segs, segment{Wildcard: true})
				rest = rest[1:]
			} else {
				name, after := readName(rest)
				if name == "" {
					return nil, errors.New("expected name after '.'")
				}
				segs = append(segs, segment{Key: name})
				rest = after
			}
		default:
			return nil, fmt.Errorf("unexpected char %q in pattern %q", rest[:1], pattern)
		}
	}
	return &Pattern{segs: segs, raw: pattern}, nil
}

// String returns the source pattern.
func (p *Pattern) String() string { return p.raw }

// Match returns every concrete dot-path in doc that matches the pattern.
// Returned paths use sjson-style indexing ("foo.bar", "arr.0").
func (p *Pattern) Match(doc interface{}) []string {
	var out []string
	p.walk(doc, "", 0, &out)
	return out
}

func (p *Pattern) walk(node interface{}, pathSoFar string, idx int, out *[]string) {
	if idx == len(p.segs) {
		*out = append(*out, pathSoFar)
		return
	}
	seg := p.segs[idx]

	switch n := node.(type) {
	case map[string]interface{}:
		for k, v := range n {
			childPath := k
			if pathSoFar != "" {
				childPath = pathSoFar + "." + k
			}
			if seg.Wildcard || seg.Key == k {
				p.walk(v, childPath, idx+1, out)
			}
			if seg.Recursive {
				p.walk(v, childPath, idx, out)
			}
		}
	case []interface{}:
		for i, v := range n {
			childPath := fmt.Sprintf("%s.%d", pathSoFar, i)
			if seg.Wildcard {
				p.walk(v, childPath, idx+1, out)
			}
			if seg.Recursive {
				p.walk(v, childPath, idx, out)
			}
		}
	}
}

// MatchPath reports whether a concrete dot-path (e.g.
// "mcpServers.gemini.env.KEY" — note: no leading "$.") would be matched by
// this pattern. Useful when you have a path string but no document to walk.
func (p *Pattern) MatchPath(path string) bool {
	var parts []string
	if path != "" {
		parts = strings.Split(path, ".")
	}
	return matchSegs(p.segs, 0, parts, 0)
}

func matchSegs(segs []segment, si int, parts []string, pi int) bool {
	if si == len(segs) {
		return pi == len(parts)
	}
	seg := segs[si]
	if seg.Recursive {
		// Try matching "zero parts consumed by this segment" first, then grow.
		for k := pi; k <= len(parts); k++ {
			if k < len(parts) && (seg.Wildcard || seg.Key == parts[k]) {
				if matchSegs(segs, si+1, parts, k+1) {
					return true
				}
			}
		}
		return false
	}
	if pi >= len(parts) {
		return false
	}
	if !(seg.Wildcard || seg.Key == parts[pi]) {
		return false
	}
	return matchSegs(segs, si+1, parts, pi+1)
}

func readName(s string) (name, rest string) {
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '.' || c == '*' {
			break
		}
		i++
	}
	return s[:i], s[i:]
}
