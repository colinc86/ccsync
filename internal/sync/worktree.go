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

// findPlaceholdersInJSON scans raw JSON bytes for redaction placeholder strings
// and returns their JSON paths. This is a best-effort surface scan — the
// authoritative walk is in jsonfilter.Restore.
func findPlaceholdersInJSON(data []byte) []string {
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	var paths []string
	walkPlaceholders(parsed, "", &paths)
	return paths
}

func walkPlaceholders(node any, prefix string, out *[]string) {
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
		if strings.HasPrefix(n, "<<REDACTED:ccsync:") && strings.HasSuffix(n, ">>") {
			*out = append(*out, prefix)
		}
	}
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
