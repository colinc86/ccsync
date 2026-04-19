package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/jsonfilter"
	"github.com/colinc86/ccsync/internal/snapshot"
)

// RollbackTo materializes the profile tree at targetCommitSHA into both the
// repo worktree and the local ~/.claude filesystem, removing any files that
// existed in HEAD but not in target. The result is a NEW forward commit (no
// history rewrite) — the state machine stays linear and safe to push.
//
// Pre-rollback safety: we take a local snapshot of every file we're about
// to touch, so the user can always undo a rollback the same way they undo
// any sync.
func RollbackTo(ctx context.Context, in Inputs, targetCommitSHA string) (Result, error) {
	repo, err := gitx.Open(in.RepoPath)
	if err != nil {
		return Result{}, fmt.Errorf("open repo: %w", err)
	}

	profilePrefix := "profiles/" + in.Profile + "/"

	// Collect paths we care about at the target commit
	targetFiles, err := repo.FilesAtCommit(targetCommitSHA)
	if err != nil {
		return Result{}, fmt.Errorf("list target files: %w", err)
	}
	targetProfileFiles := map[string]bool{}
	for _, p := range targetFiles {
		if strings.HasPrefix(p, profilePrefix) {
			targetProfileFiles[p] = true
		}
	}

	// Collect paths currently in HEAD for the same profile
	headSHA, err := repo.HeadSHA()
	if err != nil {
		return Result{}, err
	}
	headFiles, _ := repo.FilesAtCommit(headSHA)
	headProfileFiles := map[string]bool{}
	for _, p := range headFiles {
		if strings.HasPrefix(p, profilePrefix) {
			headProfileFiles[p] = true
		}
	}

	jsonRules := resolveJSONRules(in.Config, in.ClaudeDir, in.ClaudeJSON)

	// Layer 1: pre-rollback snapshot of everything we're about to touch locally
	allTouched := map[string]bool{}
	for p := range targetProfileFiles {
		allTouched[p] = true
	}
	for p := range headProfileFiles {
		allTouched[p] = true
	}

	var absTouched []string
	for p := range allTouched {
		if abs := repoPathToLocal(p, in.Profile, in.ClaudeDir, in.ClaudeJSON); abs != "" {
			absTouched = append(absTouched, abs)
		}
	}
	snapRoot := filepath.Join(in.StateDir, "snapshots")
	meta, _ := snapshot.Take(snapRoot, "rollback", in.Profile, absTouched)

	// Write target content to both sides
	var missing []string
	for p := range targetProfileFiles {
		data, ok, err := repo.BlobAtCommit(targetCommitSHA, p)
		if err != nil {
			return Result{}, fmt.Errorf("read blob %s at %s: %w", p, targetCommitSHA[:7], err)
		}
		if !ok {
			continue
		}

		// Repo side
		repoAbs := filepath.Join(in.RepoPath, p)
		if err := writeFileAtomic(repoAbs, data); err != nil {
			return Result{}, err
		}

		// Local side (with redaction restore if JSON)
		localAbs := repoPathToLocal(p, in.Profile, in.ClaudeDir, in.ClaudeJSON)
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
				return Result{}, err
			}
			if len(restored.Missing) > 0 {
				missing = append(missing, restored.Missing...)
				continue // refuse to write a file with dangling placeholders
			}
			out = restored.Data
		}
		if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
			return Result{}, err
		}
		if err := writeFileAtomic(localAbs, out); err != nil {
			return Result{}, err
		}
	}

	// Delete anything in HEAD but not in target (both sides)
	for p := range headProfileFiles {
		if targetProfileFiles[p] {
			continue
		}
		_ = os.Remove(filepath.Join(in.RepoPath, p))
		if localAbs := repoPathToLocal(p, in.Profile, in.ClaudeDir, in.ClaudeJSON); localAbs != "" {
			_ = os.Remove(localAbs)
		}
	}

	// Regenerate repo README so humans see current profile set.
	_ = writeRepoREADME(in.RepoPath, listProfilesFromRepo(in.RepoPath), nil, in.HostName)

	// Commit + push as a new forward commit (no history rewrite).
	if err := repo.AddAll(); err != nil {
		return Result{}, err
	}
	hasChanges, err := repo.HasChanges()
	if err != nil {
		return Result{}, err
	}
	if !hasChanges {
		return Result{SnapshotID: meta.ID, MissingSecrets: missing}, nil
	}
	short := targetCommitSHA
	if len(short) > 7 {
		short = short[:7]
	}
	msg := fmt.Sprintf("rollback(%s): %s to %s", in.Profile, in.HostName, short)
	commitSHA, err := repo.Commit(msg, in.HostName, in.AuthorEmail)
	if err != nil {
		return Result{}, err
	}
	if err := repo.Push(ctx, in.Auth); err != nil {
		return Result{}, err
	}

	// Update last-synced pointer to the new commit (not the rollback target).
	if st, err := loadHostState(in.StateDir); err == nil {
		if newHead, herr := repo.HeadSHA(); herr == nil && newHead != "" {
			st.LastSyncedSHA[in.Profile] = newHead
			_ = saveHostState(in.StateDir, st)
		}
	}

	return Result{
		CommitSHA:      commitSHA,
		SnapshotID:     meta.ID,
		MissingSecrets: missing,
	}, nil
}
