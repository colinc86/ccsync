package merge

import (
	"github.com/sergi/go-diff/diffmatchpatch"
)

// Text three-way merges base, local, and remote text.
// Strategy: try applying local's patch to remote and vice versa. If both
// succeed and yield the same result, it's a clean merge. Otherwise it's a
// file-level conflict; we default Merged to local so the file is readable.
func Text(base, local, remote string) Result {
	if local == remote {
		return Result{Merged: []byte(local)}
	}
	if base == local {
		return Result{Merged: []byte(remote)}
	}
	if base == remote {
		return Result{Merged: []byte(local)}
	}

	dmp := diffmatchpatch.New()

	diffsL := dmp.DiffMain(base, local, false)
	patchesL := dmp.PatchMake(base, diffsL)
	mergedR, appliedR := dmp.PatchApply(patchesL, remote)

	diffsR := dmp.DiffMain(base, remote, false)
	patchesR := dmp.PatchMake(base, diffsR)
	mergedL, appliedL := dmp.PatchApply(patchesR, local)

	if allTrue(appliedR) && allTrue(appliedL) && mergedR == mergedL {
		return Result{Merged: []byte(mergedR)}
	}

	return Result{
		Merged: []byte(local),
		Conflicts: []Conflict{{
			Path:   "",
			Kind:   ConflictTextHunk,
			Base:   base,
			Local:  local,
			Remote: remote,
		}},
	}
}

func allTrue(bs []bool) bool {
	for _, b := range bs {
		if !b {
			return false
		}
	}
	return true
}
