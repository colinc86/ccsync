package merge

import (
	"github.com/sergi/go-diff/diffmatchpatch"
)

// TextSegment is one piece of a text-merge plan. Exactly one of Agreed or
// Hunk is set. Agreed lines are the spans where local and remote are
// identical — they pass through unchanged. Hunks are spans where local
// and remote diverge and the user (or a picker) must choose a side.
type TextSegment struct {
	Agreed string    // non-empty when both sides match this span
	Hunk   *TextHunk // non-nil when local and remote disagree
}

// TextHunk is a single region where local and remote provided different
// lines. Either side may be empty (pure add/delete on that side).
type TextHunk struct {
	Local  string
	Remote string
}

// TextSegments splits local and remote into a sequence of agreed spans
// and conflict hunks using line-level diff. Intended for driving a
// per-hunk resolution UI; the full-file Text() function still handles
// auto-mergeable cases (same change on both sides, patches apply cleanly,
// etc.) so TextSegments is only called when Text() reports a conflict.
//
// Simplest useful model: diff local vs remote directly at line
// granularity and group runs of delete+insert as hunks. Skips the
// base-aware diff3 dance — by the time we get here the patch-apply merge
// has already failed, so any diff block between local and remote is by
// definition something the user has to look at.
func TextSegments(local, remote string) []TextSegment {
	dmp := diffmatchpatch.New()
	a, b, lineArr := dmp.DiffLinesToChars(local, remote)
	diffs := dmp.DiffMain(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArr)

	var out []TextSegment
	var pendingL, pendingR string
	flush := func() {
		if pendingL == "" && pendingR == "" {
			return
		}
		out = append(out, TextSegment{Hunk: &TextHunk{Local: pendingL, Remote: pendingR}})
		pendingL, pendingR = "", ""
	}
	for _, d := range diffs {
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			flush()
			out = append(out, TextSegment{Agreed: d.Text})
		case diffmatchpatch.DiffDelete:
			// DiffDelete means "present in local, absent in remote" given
			// DiffMain(local, remote) — so local contributes it.
			pendingL += d.Text
		case diffmatchpatch.DiffInsert:
			pendingR += d.Text
		}
	}
	flush()
	return out
}

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
