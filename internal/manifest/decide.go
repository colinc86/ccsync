package manifest

// Action is the per-file outcome of comparing local, base, and remote SHAs.
type Action int

const (
	ActionNoOp         Action = iota
	ActionAddLocal            // new on remote; pull
	ActionAddRemote           // new on local; push
	ActionPull                // remote changed; fast-forward pull
	ActionPush                // local changed; fast-forward push
	ActionDeleteLocal         // remote deleted; delete locally
	ActionDeleteRemote        // local deleted; delete on remote
	ActionMerge               // both sides changed; three-way merge
	ActionConflict            // unresolvable without user input (e.g. delete vs modify)
)

var actionNames = [...]string{
	"NoOp", "AddLocal", "AddRemote", "Pull", "Push",
	"DeleteLocal", "DeleteRemote", "Merge", "Conflict",
}

func (a Action) String() string {
	if int(a) < 0 || int(a) >= len(actionNames) {
		return "Unknown"
	}
	return actionNames[a]
}

// Decide returns the action for one file given three SHAs.
// An empty SHA means the file is absent at that side.
func Decide(local, base, remote string) Action {
	lp, bp, rp := local != "", base != "", remote != ""

	switch {
	case !lp && !bp && !rp:
		return ActionNoOp
	case !lp && !bp && rp:
		return ActionAddLocal
	case lp && !bp && !rp:
		return ActionAddRemote
	case lp && !bp && rp:
		if local == remote {
			return ActionNoOp
		}
		return ActionConflict
	case !lp && bp && !rp:
		return ActionNoOp // both sides deleted since base
	case !lp && bp && rp:
		if remote == base {
			return ActionDeleteRemote
		}
		return ActionConflict // local delete vs remote modify
	case lp && bp && !rp:
		if local == base {
			return ActionDeleteLocal
		}
		return ActionConflict // remote delete vs local modify
	default:
		if local == base && remote == base {
			return ActionNoOp
		}
		if local == base {
			return ActionPull
		}
		if remote == base {
			return ActionPush
		}
		if local == remote {
			return ActionNoOp
		}
		return ActionMerge
	}
}
