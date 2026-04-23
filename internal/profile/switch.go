package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/discover"
	"github.com/colinc86/ccsync/internal/ignore"
	"github.com/colinc86/ccsync/internal/snapshot"
	"github.com/colinc86/ccsync/internal/state"
)

// SwitchAndSwap changes the active profile to target AND brings the
// on-disk tracked tree into sync with target's effective repo content.
//
// Flow:
//  1. Resolve target's extends chain and compute its effective repo
//     path set (union of ancestors, projected into target's namespace).
//  2. Walk the user's live ~/.claude + ~/.claude.json to find tracked
//     files (honouring .syncignore at the repo root).
//  3. Snapshot every tracked abs path under stateDir/backups so the
//     whole operation is reversible via profile.RestoreBackup.
//  4. Delete from disk any tracked file whose repo-projection does NOT
//     exist in target's effective tree — those belonged to the old
//     profile and must not be re-attributed to target on the next
//     sync (pre-v0.7 bug: sync saw them as local-only adds and pushed
//     them to profiles/<target>/, contaminating the target).
//  5. Update state.ActiveProfile and save state.
//
// We deliberately do NOT materialize target's files here. Sync does
// that, and doing it in two places would force us to replicate the
// redaction / keyring / preserve-local-excludes logic that already
// lives in sync.Run. The call site is expected to run a sync right
// after SwitchAndSwap returns.
//
// Switching to the already-active profile is a no-op (empty Meta).
// Target must exist in cfg.
func SwitchAndSwap(
	cfg *config.Config,
	repoPath string,
	st *state.State,
	stateDir string,
	target string,
	claudeDir, claudeJSON string,
) (snapshot.Meta, error) {
	if target == "" {
		return snapshot.Meta{}, errors.New("target profile required")
	}
	if target == st.ActiveProfile {
		return snapshot.Meta{}, nil
	}
	if _, ok := cfg.Profiles[target]; !ok {
		return snapshot.Meta{}, fmt.Errorf("no such profile: %q", target)
	}

	// 1. Target's effective repo-relative path set (stripped of the
	//    profiles/<target>/ prefix so we can match against local
	//    RelPath directly — discover.Walk's RelPath is
	//    "claude/agents/…" or "claude.json" with no profile prefix).
	resolved, err := config.EffectiveProfile(cfg, target)
	if err != nil {
		return snapshot.Meta{}, fmt.Errorf("resolve target: %w", err)
	}
	targetPaths, err := effectiveRelPaths(repoPath, resolved.Chain)
	if err != nil {
		return snapshot.Meta{}, fmt.Errorf("read target tree: %w", err)
	}

	// 2. Walk the user's local tracked files.
	matcher, err := loadSyncignore(repoPath, cfg.DefaultSyncignore)
	if err != nil {
		return snapshot.Meta{}, fmt.Errorf("load syncignore: %w", err)
	}
	disc, err := discover.Walk(discover.Inputs{
		ClaudeDir:  claudeDir,
		ClaudeJSON: claudeJSON,
	}, matcher)
	if err != nil {
		return snapshot.Meta{}, fmt.Errorf("walk local: %w", err)
	}
	absPaths := make([]string, 0, len(disc.Tracked))
	for _, e := range disc.Tracked {
		absPaths = append(absPaths, e.AbsPath)
	}

	// 3. Snapshot everything tracked — covers both files we're about
	//    to remove and files we're keeping, so the user can hit
	//    `ccsync profile restore` and fully reverse the switch.
	backupRoot := filepath.Join(stateDir, "backups")
	op := "profile-" + sanitize(st.ActiveProfile) + "-to-" + sanitize(target)
	meta, err := snapshot.Take(backupRoot, op, st.ActiveProfile, absPaths)
	if err != nil {
		return snapshot.Meta{}, fmt.Errorf("snapshot: %w", err)
	}

	// 4. Remove files that don't belong to target. Silently skip
	//    already-missing files (stat race, user deleted between the
	//    walk and here) — the snapshot already has them if they
	//    mattered, and re-erroring would abort a half-complete swap.
	for _, e := range disc.Tracked {
		if targetPaths[e.RelPath] {
			continue
		}
		if err := os.Remove(e.AbsPath); err != nil && !os.IsNotExist(err) {
			return meta, fmt.Errorf("remove %s: %w", e.AbsPath, err)
		}
	}

	// 5. Reset target's last-synced anchor so the next sync uses
	//    first-sync-take-remote semantics. Without this, the sync
	//    engine sees "local missing, base has content, remote matches
	//    base" and classifies the switch-induced disk clear as a
	//    user-intent delete → ActionDeleteRemote → target's content
	//    disappears on the next push. First-sync semantics turn the
	//    same signal into ActionAddLocal (pull) which is exactly
	//    what a profile swap should do.
	if st.LastSyncedSHA == nil {
		st.LastSyncedSHA = map[string]string{}
	}
	st.LastSyncedSHA[target] = ""

	// 6. Flip the active profile. Any failure here leaves the disk
	//    already swapped but state.ActiveProfile pointing at the old
	//    profile — inconsistent, but recoverable via the snapshot.
	st.ActiveProfile = target
	if err := state.Save(stateDir, st); err != nil {
		return meta, fmt.Errorf("save state after swap: %w", err)
	}
	return meta, nil
}

// effectiveRelPaths returns the set of profile-relative paths that target
// sees, given an ordered extends chain (target first, then ancestors).
// Each entry is like "claude/agents/foo.md" or "claude.json" — the
// "profiles/<name>/" prefix is stripped. Inherited files get projected
// into the leaf namespace, so the returned set is exactly what should
// appear on disk (minus the filesystem prefix) when target is active.
func effectiveRelPaths(repoPath string, chain []string) (map[string]bool, error) {
	out := map[string]bool{}
	// Walk ancestors in parent-first order so a target override of an
	// inherited file wins (last write wins in out). Order doesn't change
	// which paths are in the set, but keeps the semantics mirrored with
	// sync.Run's chain walk.
	for i := len(chain) - 1; i >= 0; i-- {
		prefix := "profiles/" + chain[i] + "/"
		root := filepath.Join(repoPath, prefix)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// loadSyncignore assembles the ignore.Matcher used by discover.Walk
// during a profile swap: repo-root .syncignore if present, otherwise
// the defaults from config. Matches what sync.Run does, so the
// definition of "tracked" is identical at switch time and sync time
// — a file that sync wouldn't push can't be something the swap
// deletes.
func loadSyncignore(repoPath, defaults string) (*ignore.Matcher, error) {
	body := defaults
	if data, err := os.ReadFile(filepath.Join(repoPath, ".syncignore")); err == nil {
		body = string(data)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return ignore.New(body), nil
}
