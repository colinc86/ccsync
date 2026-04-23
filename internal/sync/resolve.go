package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/humanize"
	"github.com/colinc86/ccsync/internal/jsonfilter"
)

// ApplyResolutions writes user-chosen bytes to both the local ~/.claude
// filesystem and the repo worktree, then commits + pushes. This is how the
// TUI finishes a conflict resolution: for each conflicted path, pass in the
// final bytes (local, remote, or edited) that should become the new state.
//
// resolutions keys are repo-relative paths (e.g. "profiles/default/claude/...").
// Values are the post-resolution bytes (what to store in the repo).
// For JSON paths, the repo-side value should still contain redaction
// placeholders; we call jsonfilter.Restore before writing to local disk.
func ApplyResolutions(ctx context.Context, in Inputs, resolutions map[string][]byte) (Result, error) {
	if len(resolutions) == 0 {
		return Result{}, nil
	}

	repo, err := gitx.Open(in.RepoPath)
	if err != nil {
		return Result{}, err
	}

	jsonRules := resolveJSONRules(in.Config, in.ClaudeDir, in.ClaudeJSON)
	var missing []string

	for path, data := range resolutions {
		// Write repo side.
		repoAbs := filepath.Join(in.RepoPath, path)
		if err := writeFileAtomic(repoAbs, data); err != nil {
			return Result{}, fmt.Errorf("write repo %s: %w", path, err)
		}

		// Write local side, restoring redacted values from keychain if JSON.
		localAbs := repoPathToLocal(path, in.Profile, in.ClaudeDir, in.ClaudeJSON)
		if localAbs == "" {
			continue
		}
		out := data
		if _, isJSON := jsonRules[localAbs]; isJSON {
			values, err := loadKeyringForJSON(in.Profile, data)
			if err != nil {
				return Result{}, err
			}
			restored, err := jsonfilter.Restore(data, values)
			if err != nil {
				return Result{}, fmt.Errorf("restore %s: %w", path, err)
			}
			if len(restored.Missing) > 0 {
				missing = append(missing, restored.Missing...)
				continue // refuse to write local with dangling placeholders
			}
			out = restored.Data
		}
		if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
			return Result{}, err
		}
		if err := writeFileAtomic(localAbs, out); err != nil {
			return Result{}, fmt.Errorf("write local %s: %w", localAbs, err)
		}
	}

	if err := repo.AddAll(); err != nil {
		return Result{}, err
	}
	hasChanges, err := repo.HasChanges()
	if err != nil {
		return Result{}, err
	}
	if !hasChanges {
		return Result{MissingSecrets: missing}, nil
	}

	msg := fmt.Sprintf("resolve(%s): %s %s", in.Profile, in.HostName, humanize.Count(len(resolutions), "conflict"))
	commitSHA, err := repo.Commit(msg, in.HostName, in.AuthorEmail)
	if err != nil {
		return Result{}, err
	}
	if err := repo.Push(ctx, in.Auth); err != nil {
		return Result{}, err
	}

	// Advance LastSyncedSHA to the new commit so the next sync's
	// three-way merge uses a fresh base. Failures here are non-fatal
	// (the push already landed; next sync self-heals via SyncToRemote)
	// but must surface so the user isn't silently left with stale state.
	if err := advanceStateToHead(in, repo, commitSHA, "resolve"); err != nil {
		return Result{CommitSHA: commitSHA, MissingSecrets: missing}, err
	}

	return Result{CommitSHA: commitSHA, MissingSecrets: missing}, nil
}
