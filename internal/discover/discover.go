// Package discover walks the user's Claude Code config tree and classifies
// files according to .syncignore rules. It's the source of truth for which
// files ccsync considers tracked.
package discover

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/colinc86/ccsync/internal/ignore"
)

// Entry describes one discovered file.
type Entry struct {
	AbsPath string // absolute path on disk
	RelPath string // slash-separated path rooted at the sync-repo tree
	Size    int64
	Ignored bool
}

// Result groups discovered entries.
type Result struct {
	Tracked []Entry
	Ignored []Entry
}

// Inputs describe where to discover from. Empty fields are skipped.
type Inputs struct {
	ClaudeDir  string // absolute path to ~/.claude
	ClaudeJSON string // absolute path to ~/.claude.json
}

// Walk discovers files under the given inputs using m for classification.
// A nil m treats all entries as tracked.
func Walk(in Inputs, m *ignore.Matcher) (*Result, error) {
	res := &Result{}

	if in.ClaudeJSON != "" {
		if info, err := os.Stat(in.ClaudeJSON); err == nil && !info.IsDir() {
			res.Tracked = append(res.Tracked, Entry{
				AbsPath: in.ClaudeJSON,
				RelPath: "claude.json",
				Size:    info.Size(),
			})
		}
	}

	if in.ClaudeDir != "" {
		if err := walkClaudeDir(in.ClaudeDir, m, res); err != nil {
			return nil, err
		}
	}

	sort.Slice(res.Tracked, func(i, j int) bool { return res.Tracked[i].RelPath < res.Tracked[j].RelPath })
	sort.Slice(res.Ignored, func(i, j int) bool { return res.Ignored[i].RelPath < res.Ignored[j].RelPath })
	return res, nil
}

func walkClaudeDir(root string, m *ignore.Matcher, res *Result) error {
	// Resolve the root once so symlink-escape checks can compare
	// against a canonical base. If the user's ~/.claude is itself
	// a symlink (uncommon but valid), we want "inside the tree" to
	// mean "inside the real target," not "inside the symlink path."
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = root
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if m != nil && m.Matches(rel+"/") {
				return filepath.SkipDir
			}
			return nil
		}

		// Symlink-escape guard: if the entry is a symlink whose target
		// resolves outside ~/.claude, silently drop it. sync.Run reads
		// via os.ReadFile which FOLLOWS links, so without this filter a
		// stray symlink could leak /etc/* or another user's home into
		// the sync repo. Broken links (EvalSymlinks errors) are dropped
		// the same way; sync would fail on read anyway, and surfacing a
		// broken link as tracked would just produce a confusing sync
		// error the user can't action.
		if d.Type()&fs.ModeSymlink != 0 {
			target, lerr := filepath.EvalSymlinks(path)
			if lerr != nil {
				return nil
			}
			if !isUnder(target, resolvedRoot) {
				return nil
			}
		}

		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		e := Entry{
			AbsPath: path,
			RelPath: "claude/" + rel,
			Size:    info.Size(),
		}
		if m != nil && m.Matches(rel) {
			e.Ignored = true
			res.Ignored = append(res.Ignored, e)
			return nil
		}
		res.Tracked = append(res.Tracked, e)
		return nil
	})
}

// isUnder reports whether target (already symlink-resolved) lives
// inside base (also resolved). Equality counts as "under" so the root
// itself isn't rejected if something happens to point straight at it.
func isUnder(target, base string) bool {
	if target == base {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(target, base+sep)
}
