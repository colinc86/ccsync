package sync

import (
	"github.com/colinc86/ccsync/internal/merge"
)

// IsDeleteVsModify reports whether the conflict represents a
// delete-on-one-side vs modify-on-the-other scenario. The policy-
// based auto-resolver always escapes to the manual picker for
// these, because "this machine wins" when "this machine" means "I
// deleted the file" silently removes the other side's work —
// destructive enough to keep a human in the loop.
func IsDeleteVsModify(c FileConflict) bool {
	if c.LocalData == nil || c.RemoteData == nil {
		return true
	}
	for _, sub := range c.Conflicts {
		if !sub.LocalPresent || !sub.RemotePresent {
			return true
		}
		if sub.Kind == merge.ConflictJSONDeleteMod {
			return true
		}
	}
	return false
}

// AnyDeleteVsModify reports whether any conflict in the slice is a
// delete-vs-modify escape case. One destructive row in the batch
// forces the whole batch to the picker, because otherwise the user
// accepts a bulk policy for the safe rows and silently loses work
// on the unsafe ones.
func AnyDeleteVsModify(conflicts []FileConflict) bool {
	for _, c := range conflicts {
		if IsDeleteVsModify(c) {
			return true
		}
	}
	return false
}

// ResolutionsFromPolicy builds the map ApplyResolutions expects,
// picking LocalData or RemoteData on every conflict per the named
// policy. Callers should pre-check AnyDeleteVsModify and escape to
// the manual picker first — this function doesn't filter; it
// applies the policy uniformly to whatever it receives. Returns
// nil if policy is neither "local" nor "cloud".
func ResolutionsFromPolicy(conflicts []FileConflict, policy string) map[string][]byte {
	switch policy {
	case "local":
		out := make(map[string][]byte, len(conflicts))
		for _, c := range conflicts {
			out[c.Path] = c.LocalData
		}
		return out
	case "cloud":
		out := make(map[string][]byte, len(conflicts))
		for _, c := range conflicts {
			out[c.Path] = c.RemoteData
		}
		return out
	}
	return nil
}
