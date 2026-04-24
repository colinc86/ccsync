package sync

import (
	"strings"

	"github.com/colinc86/ccsync/internal/manifest"
	ccstate "github.com/colinc86/ccsync/internal/state"
)

// Partition groups a plan's actions by the policy attached to their
// category. Each action lands in exactly one bucket:
//
//   - Auto: policy=auto, apply without a prompt
//   - Review: policy=review, user must confirm each one
//   - Never: policy=never, already-filtered equivalent; we carry them
//     so the UI can show "X items skipped by policy"
//
// Profile-excluded and already-denied actions are filtered out before
// this partitioning — they're never user-reviewable. NoOp actions are
// kept in Auto (they'll be a no-op during the real sync anyway; useful
// for counting).
type Partition struct {
	Auto   []FileAction
	Review []FileAction
	Never  []FileAction
}

// PartitionPlan splits plan.Actions into three buckets based on the
// per-(category, direction) policies stored on state. `profilePrefix`
// is the "profiles/<name>/" prefix of the currently-active profile,
// used to classify paths correctly (the profiles/... prefix is stripped
// before calling category.Classify).
//
// Approve mode overlay: in state.IsApproveMode(), any add-new action
// (ActionAddRemote for push-new, ActionAddLocal for pull-new) is
// unconditionally routed to the Review bucket regardless of the
// per-category policy. Modify/delete actions still honour their
// category policy so users can keep approve mode lightweight —
// nothing flows without a prompt only for genuinely NEW files.
func PartitionPlan(plan Plan, st *ccstate.State) Partition {
	var p Partition
	approve := st.IsApproveMode()
	for _, a := range plan.Actions {
		if a.ExcludedByProfile || a.ExcludedByDeny {
			continue
		}
		dir := actionDirection(a.Action)
		if dir == "" {
			// NoOp or Merge — not a direct push or pull for policy
			// purposes. Let these flow through as auto.
			p.Auto = append(p.Auto, a)
			continue
		}
		if approve && isAddAction(a.Action) {
			p.Review = append(p.Review, a)
			continue
		}
		policy := st.PolicyFor(a.Category, dir)
		switch policy {
		case ccstate.PolicyNever:
			p.Never = append(p.Never, a)
		case ccstate.PolicyReview:
			p.Review = append(p.Review, a)
		default:
			p.Auto = append(p.Auto, a)
		}
	}
	return p
}

// isAddAction reports whether the action creates a new file on one
// side (as opposed to updating or deleting an existing one). Approve
// mode uses this to decide whether the row gates on user approval.
func isAddAction(a manifest.Action) bool {
	return a == manifest.ActionAddRemote || a == manifest.ActionAddLocal
}

// actionDirection maps a manifest.Action to the push/pull direction the
// review policy system cares about. NoOp / Merge / Conflict return "".
func actionDirection(a manifest.Action) ccstate.Direction {
	switch a {
	case manifest.ActionAddRemote, manifest.ActionPush, manifest.ActionDeleteRemote:
		return ccstate.DirPush
	case manifest.ActionAddLocal, manifest.ActionPull, manifest.ActionDeleteLocal:
		return ccstate.DirPull
	}
	return ""
}

// ActionIsPush reports whether the given action moves bytes from local
// to the repo. Exported for use by the TUI review screen which renders
// directional arrows per item.
func ActionIsPush(a manifest.Action) bool {
	return actionDirection(a) == ccstate.DirPush
}

// SummarizeAction returns a one-line human description of what the
// action will do, from the user's perspective. Used by the review UI.
func SummarizeAction(a FileAction) string {
	p := strings.TrimPrefix(a.Path, "profiles/")
	if i := strings.Index(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	switch a.Action {
	case manifest.ActionAddRemote:
		return "new · pushing " + p
	case manifest.ActionPush:
		return "update · pushing " + p
	case manifest.ActionDeleteRemote:
		return "delete · removing " + p + " from repo"
	case manifest.ActionAddLocal:
		return "new · pulling " + p
	case manifest.ActionPull:
		return "update · pulling " + p
	case manifest.ActionDeleteLocal:
		return "delete · removing " + p + " locally"
	}
	return p
}
