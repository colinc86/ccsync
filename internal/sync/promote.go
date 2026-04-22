package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/colinc86/ccsync/internal/gitx"
)

// PromotePath moves a file in the repo worktree from one profile's
// subtree to another, committing + pushing in a single step. This is
// how "share this with other profiles" works in the UI: a file that
// was pushed to profiles/<active>/<rel> gets moved to
// profiles/<target>/<rel> so every profile that extends <target>
// picks it up on the next sync.
//
// repoRelPath is the path under the active profile's subtree (e.g.
// "claude/agents/foo.md" — no "profiles/<name>/" prefix). from is the
// profile name the file currently sits under; to is the destination
// profile. Both must exist in ccsync.yaml.
//
// Idempotency: if the destination already has identical bytes, we
// still delete the source (the intent is "source should not hold its
// own copy anymore"); the commit may be empty, which gitx.Commit
// tolerates gracefully.
func PromotePath(ctx context.Context, in Inputs, repoRelPath, from, to string) error {
	if from == "" || to == "" {
		return fmt.Errorf("promote: from and to profiles required")
	}
	if from == to {
		return nil
	}

	repo, err := gitx.Open(in.RepoPath)
	if err != nil {
		return fmt.Errorf("promote open: %w", err)
	}
	// Always align with origin before a targeted rewrite so the commit
	// we're about to make is fast-forward-friendly.
	if empty, _ := repo.IsEmpty(); !empty {
		if err := repo.Fetch(ctx, in.Auth); err != nil {
			return fmt.Errorf("promote fetch: %w", err)
		}
		if err := repo.SyncToRemote(); err != nil {
			return fmt.Errorf("promote align: %w", err)
		}
	}

	srcPath := filepath.Join(in.RepoPath, "profiles", from, repoRelPath)
	dstPath := filepath.Join(in.RepoPath, "profiles", to, repoRelPath)

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("promote read %s: %w", srcPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("promote mkdir: %w", err)
	}
	if err := writeFileAtomic(dstPath, data); err != nil {
		return fmt.Errorf("promote write %s: %w", dstPath, err)
	}
	// Remove the source. After this, machines on `from`'s profile will
	// pull `to`'s version via the normal extends-inheritance path.
	if err := os.Remove(srcPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("promote remove %s: %w", srcPath, err)
	}

	hasChanges, err := repo.HasChanges()
	if err != nil {
		return err
	}
	if !hasChanges {
		return nil // idempotent — destination was already identical
	}

	if err := repo.AddAll(); err != nil {
		return err
	}
	rel := strings.TrimPrefix(repoRelPath, "claude/")
	msg := fmt.Sprintf("promote: move %s from %s to %s", rel, from, to)
	if _, err := repo.Commit(msg, in.HostName, in.AuthorEmail); err != nil {
		return err
	}
	return repo.Push(ctx, in.Auth)
}
