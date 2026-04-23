package profileinspect

import (
	"bytes"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// extractMarkdownMeta pulls a title + description out of a markdown
// file's content. Precedence (first hit wins):
//  1. YAML frontmatter (`---\n...\n---\n` at top of file) with
//     `name` + `description` keys — the convention Claude Code
//     skills / commands / agents use.
//  2. First H1 (`# Something`) for title; first non-heading paragraph
//     for description.
//  3. Filename stem as title; empty description.
//
// fallbackName is the path (or dir, for skills) used when no H1 is
// present — the filename stem becomes a reasonable last-resort title
// (e.g., "ccsync-verify" for `.claude/commands/ccsync-verify.md`).
//
// All returned strings are trimmed. Descriptions are single-line;
// multi-line paragraphs collapse to one line with `· ` between
// sentences and truncate to 160 runes so list rendering stays tight.
func extractMarkdownMeta(data []byte, fallbackName string) (title, description string) {
	rest, fm := parseFrontmatter(data)
	if fm != nil {
		if v := firstString(fm, "name", "title"); v != "" {
			title = v
		}
		if v := firstString(fm, "description", "desc", "summary"); v != "" {
			description = v
		}
	}
	if title == "" {
		if h1 := findH1(rest); h1 != "" {
			title = h1
		} else {
			title = stemOf(fallbackName)
		}
	}
	if description == "" {
		description = firstParagraph(rest)
	}
	return cleanOneLine(title, 80), cleanOneLine(description, 160)
}

// parseFrontmatter returns (body-without-frontmatter, parsed-map)
// when data begins with a `---\n` / `\n---\n` frontmatter block.
// Missing or malformed frontmatter returns (data, nil) so the caller
// falls back to H1/filename. Uses yaml.v3's best-effort unmarshal
// into a generic map — we only care about a couple of string keys.
func parseFrontmatter(data []byte) (rest []byte, fm map[string]any) {
	// Tolerate CRLF line endings by normalising only the prefix check.
	// The marker lines in frontmatter are cheap to match either way.
	open := []byte("---\n")
	if !bytes.HasPrefix(data, open) {
		// Try the Windows line-ending variant.
		if bytes.HasPrefix(data, []byte("---\r\n")) {
			open = []byte("---\r\n")
		} else {
			return data, nil
		}
	}
	body := data[len(open):]
	closeMarker := []byte("\n---\n")
	altClose := []byte("\n---\r\n")
	idx := bytes.Index(body, closeMarker)
	sep := len(closeMarker)
	if idx < 0 {
		idx = bytes.Index(body, altClose)
		sep = len(altClose)
		if idx < 0 {
			return data, nil
		}
	}
	block := body[:idx]
	rest = body[idx+sep:]
	m := map[string]any{}
	if err := yaml.Unmarshal(block, &m); err != nil {
		return data, nil
	}
	return rest, m
}

// firstString returns the first present string value across the
// given keys. YAML decodes into map[string]any with strings as
// strings and numbers/bools coerced through fmt — but we only use
// string values, so other shapes fall through to empty.
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// findH1 returns the text of the first `# Heading` line in body, or
// "" if none. Skips the `# ` prefix; rest-of-line is trimmed.
// Intentionally only matches ATX-style headings (`# `) and not
// Setext (`===` underline) — Claude Code content uses ATX
// exclusively.
func findH1(body []byte) string {
	for _, line := range bytes.Split(body, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("# ")) {
			continue
		}
		// Strip trailing whitespace + `#` decorators ("# Foo #"
		// shape, which pandoc users sometimes produce).
		heading := bytes.TrimSpace(bytes.TrimRight(trimmed[2:], "# "))
		if len(heading) > 0 {
			return string(heading)
		}
	}
	return ""
}

// firstParagraph returns the first non-empty block of prose in body,
// flattened to one line. Skips headings, skips frontmatter (the
// caller already stripped it), stops at the next blank line. Used
// as the subtitle when no `description:` frontmatter exists.
func firstParagraph(body []byte) string {
	lines := bytes.Split(body, []byte("\n"))
	var buf []string
	started := false
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			if started {
				break
			}
			continue
		}
		// Skip headings + list markers + code fences. Prose wins.
		if bytes.HasPrefix(trimmed, []byte("#")) {
			continue
		}
		if bytes.HasPrefix(trimmed, []byte("```")) {
			continue
		}
		started = true
		buf = append(buf, string(trimmed))
	}
	return strings.Join(buf, " ")
}

// stemOf returns the filename without extension. "foo.md" → "foo";
// "skills/my-skill/SKILL.md" → "my-skill" (the directory name when
// the file is named SKILL.md, matching how Claude Code addresses
// skills).
func stemOf(path string) string {
	base := filepath.Base(path)
	if strings.EqualFold(base, "SKILL.md") {
		dir := filepath.Base(filepath.Dir(path))
		if dir != "" && dir != "." {
			return dir
		}
	}
	if i := strings.LastIndex(base, "."); i > 0 {
		return base[:i]
	}
	return base
}

// cleanOneLine flattens whitespace to a single space and truncates
// to n runes (inclusive of a trailing "…" when cut). Empty input
// returns empty.
func cleanOneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
