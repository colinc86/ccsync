package sync

import (
	"context"
	"errors"

	"github.com/colinc86/ccsync/internal/gitx"
)

// RunWithRetry wraps Run with a single automatic retry on non-fast-forward
// push failures — the race case where another machine pushed between our
// Fetch and our Push. Run's own Fetch+SyncToRemote pass is what makes a
// retry safe: on re-entry the worktree hard-resets to the new origin tip,
// the three-way merge runs again against the advanced base, and the
// resulting commit fast-forwards.
//
// Exposed as a thin wrapper rather than folded into Run because tests and
// dry-run-heavy callers (refreshPlanCmd) benefit from the single-attempt
// shape — they don't want the retry path hiding a genuine, surfaceable
// push rejection.
func RunWithRetry(ctx context.Context, in Inputs, events chan<- Event) (Result, error) {
	res, err := Run(ctx, in, events)
	if err == nil || !errors.Is(err, gitx.ErrNonFastForward) {
		return res, err
	}
	return Run(ctx, in, events)
}
