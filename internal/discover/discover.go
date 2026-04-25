// Package discover walks the user's Claude Code content tree and
// classifies files according to .syncignore rules. It's the source of
// truth for which files ccsync considers tracked.
//
// Post-v0.9.0 the walk is deliberately narrow: only the six content
// directories (agents, skills, commands, hooks, output-styles, memory)
// plus the single top-level CLAUDE.md file. Everything else under
// ~/.claude/ — settings, caches, telemetry, sessions, projects — is
// invisible to ccsync. JSON-slice content (mcpServers, hook wiring)
// is handled by internal/mcpextract, not by this walk.
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

// Inputs describe where to discover from.
type Inputs struct {
	// ClaudeDir is the absolute path to ~/.claude. Walk descends into
	// the explicit content roots underneath; everything else is
	// invisible.
	ClaudeDir string
}

// ContentDirs is the canonical list of subdirectories under ~/.claude
// that ccsync syncs. Order is the inspector's render order. Adding a
// directory here is one of the steps to add a new content chunk —
// see internal/state.AllContentChunks for the full list.
var ContentDirs = []string{
	"agents",
	"skills",
	"commands",
	"hooks",
	"output-styles",
	"memory",
}

// TopLevelFile is a single file under ~/.claude that we sync as
// content (not as a settings file). Right now there's just one —
// CLAUDE.md, the user's global Claude Code instructions.
const TopLevelFile = "CLAUDE.md"

// Walk discovers files under the given inputs using m for classification.
// A nil m treats all entries as tracked.
func Walk(in Inputs, m *ignore.Matcher) (*Result, error) {
	res := &Result{}
	if in.ClaudeDir == "" {
		return res, nil
	}

	// Resolve the root once so symlink-escape checks can compare
	// against a canonical base.
	resolvedRoot, err := filepath.EvalSymlinks(in.ClaudeDir)
	if err != nil {
		resolvedRoot = in.ClaudeDir
	}

	// Top-level CLAUDE.md (single file, not a directory).
	topPath := filepath.Join(in.ClaudeDir, TopLevelFile)
	if info, err := os.Lstat(topPath); err == nil && !info.IsDir() {
		entry := Entry{
			AbsPath: topPath,
			RelPath: "claude/" + TopLevelFile,
			Size:    info.Size(),
		}
		// Apply ignore rules even to the top-level file — the user
		// might genuinely want CLAUDE.md ignored, and consistency
		// with the directory walk matters.
		if m != nil && m.Matches(TopLevelFile) {
			entry.Ignored = true
			res.Ignored = append(res.Ignored, entry)
		} else if symlinkOK(topPath, info, resolvedRoot) {
			res.Tracked = append(res.Tracked, entry)
		}
	}

	// Explicit content directories. A missing directory is fine —
	// not every machine has every chunk populated.
	for _, dir := range ContentDirs {
		root := filepath.Join(in.ClaudeDir, dir)
		if err := walkContentDir(root, dir, resolvedRoot, m, res); err != nil {
			return nil, err
		}
	}

	sort.Slice(res.Tracked, func(i, j int) bool { return res.Tracked[i].RelPath < res.Tracked[j].RelPath })
	sort.Slice(res.Ignored, func(i, j int) bool { return res.Ignored[i].RelPath < res.Ignored[j].RelPath })
	return res, nil
}

func walkContentDir(root, contentName, resolvedClaudeRoot string, m *ignore.Matcher, res *Result) error {
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
		// Match against the path relative to ~/.claude (not to the
		// content dir) so .syncignore patterns like "memory/notes.md"
		// or "*.bak" both work as expected.
		matchPath := contentName + "/" + rel

		if d.IsDir() {
			if m != nil && m.Matches(matchPath+"/") {
				return filepath.SkipDir
			}
			return nil
		}

		// Symlink-escape guard: drop links resolving outside ~/.claude
		// to keep discovery hermetic.
		if d.Type()&fs.ModeSymlink != 0 {
			info, _ := d.Info()
			if !symlinkOK(path, info, resolvedClaudeRoot) {
				return nil
			}
		}

		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		e := Entry{
			AbsPath: path,
			RelPath: "claude/" + matchPath,
			Size:    info.Size(),
		}
		if m != nil && m.Matches(matchPath) {
			e.Ignored = true
			res.Ignored = append(res.Ignored, e)
			return nil
		}
		res.Tracked = append(res.Tracked, e)
		return nil
	})
}

// symlinkOK reports whether the given path is safe to track. Regular
// files always pass; symlinks must resolve to a target inside the
// claude root. EvalSymlinks errors (broken or dangling links) fail
// safe — we don't surface them as tracked since sync.Run would
// crash on the read anyway.
func symlinkOK(path string, info os.FileInfo, resolvedClaudeRoot string) bool {
	if info == nil {
		return false
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return true
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return isUnder(target, resolvedClaudeRoot)
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
