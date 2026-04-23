package sync

import (
	"fmt"
	"time"

	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/state"
)

// loadHostState and saveHostState are local aliases over internal/state so the
// orchestrator doesn't import the shared package in two places.
func loadHostState(stateDir string) (*state.State, error) { return state.Load(stateDir) }
func saveHostState(stateDir string, s *state.State) error { return state.Save(stateDir, s) }

// advanceStateToHead updates LastSyncedSHA for the active profile to the
// repo's current HEAD after a successful push. All failures are wrapped
// with a "<op> pushed to <short>, but …" prefix so the user sees both
// what succeeded and what didn't — the push already landed, so this is
// a non-fatal "next sync will self-heal via SyncToRemote" situation, but
// silent swallow (the pre-v0.6.5 behavior) left the user with stale
// state that could surface as spurious conflicts on the very next run.
//
// op is the verb that just succeeded ("resolve", "rollback", …); it
// shows up in the error message so the user knows which path failed.
func advanceStateToHead(in Inputs, repo *gitx.Repo, commitSHA, op string) error {
	short := commitSHA
	if len(short) > 7 {
		short = short[:7]
	}
	st, err := loadHostState(in.StateDir)
	if err != nil {
		return fmt.Errorf("%s pushed to %s, but loading local state failed (next sync will self-heal): %w", op, short, err)
	}
	newHead, err := repo.HeadSHA()
	if err != nil {
		return fmt.Errorf("%s pushed to %s, but reading HEAD failed (next sync will self-heal): %w", op, short, err)
	}
	if newHead == "" {
		return nil
	}
	if st.LastSyncedSHA == nil {
		st.LastSyncedSHA = map[string]string{}
	}
	if st.LastSyncedAt == nil {
		st.LastSyncedAt = map[string]time.Time{}
	}
	st.LastSyncedSHA[in.Profile] = newHead
	st.LastSyncedAt[in.Profile] = time.Now().UTC()
	if err := saveHostState(in.StateDir, st); err != nil {
		return fmt.Errorf("%s pushed to %s, but saving local state failed (next sync will self-heal): %w", op, short, err)
	}
	return nil
}
