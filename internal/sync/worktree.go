package sync

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// readProfileTreeFromWorktree returns every file under the given repo prefix
// as (repo-relative path → bytes). Safe when the prefix doesn't exist yet.
func readProfileTreeFromWorktree(repoPath, prefix string) (map[string][]byte, error) {
	out := map[string][]byte{}
	root := filepath.Join(repoPath, prefix)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return out, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(repoPath, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out, err
}

// placeholderRef is a single redaction placeholder we found in a JSON
// document: its location (JSON path within the doc) and the profile name
// embedded in the placeholder string — needed to look up the original
// value in the keychain, since secrets are scoped by the profile that
// first redacted them.
type placeholderRef struct {
	Path    string // JSON path within the document, sjson syntax
	Profile string // profile name encoded in the placeholder
}

// findPlaceholdersInJSON scans raw JSON bytes for redaction placeholder strings
// and returns their locations along with the profile each was stamped by.
// This is a best-effort surface scan — the authoritative walk is in
// jsonfilter.Restore.
func findPlaceholdersInJSON(data []byte) []placeholderRef {
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	var refs []placeholderRef
	walkPlaceholders(parsed, "", &refs)
	return refs
}

func walkPlaceholders(node any, prefix string, out *[]placeholderRef) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			walkPlaceholders(v, p, out)
		}
	case []any:
		for i, v := range n {
			walkPlaceholders(v, prefixIndex(prefix, i), out)
		}
	case string:
		if prof, ok := parsePlaceholderProfile(n); ok {
			*out = append(*out, placeholderRef{Path: prefix, Profile: prof})
		}
	}
}

// parsePlaceholderProfile extracts the profile from a redaction placeholder
// string, returning ok=false for anything that doesn't match the expected
// format. The format is: <<REDACTED:ccsync:<profile>:<path>>>.
func parsePlaceholderProfile(s string) (string, bool) {
	const prefix = "<<REDACTED:ccsync:"
	const suffix = ">>"
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, suffix) {
		return "", false
	}
	body := s[len(prefix) : len(s)-len(suffix)]
	prof, _, ok := strings.Cut(body, ":")
	if !ok {
		return "", false
	}
	return prof, true
}

func prefixIndex(prefix string, i int) string {
	if prefix == "" {
		return intStr(i)
	}
	return prefix + "." + intStr(i)
}

func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	n := i
	if n < 0 {
		digits = append(digits, '-')
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(digits) + string(buf[pos:])
}
