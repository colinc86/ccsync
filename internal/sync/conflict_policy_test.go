package sync

import (
	"reflect"
	"testing"

	"github.com/colinc86/ccsync/internal/merge"
)

// TestResolutionsFromPolicy_Local pins that "local" builds the
// resolutions map pointing every conflict at its LocalData. Silent
// auto-resolve depends on this wiring — if Local ever drifted to
// pick MergedData or something else, the primary-machine UX would
// silently take a merged-guess version instead of the user's
// actual local bytes.
func TestResolutionsFromPolicy_Local(t *testing.T) {
	conflicts := []FileConflict{
		{Path: "profiles/x/claude/a.md", LocalData: []byte("A-local"), RemoteData: []byte("A-remote")},
		{Path: "profiles/x/claude/b.md", LocalData: []byte("B-local"), RemoteData: []byte("B-remote")},
	}
	got := ResolutionsFromPolicy(conflicts, "local")
	want := map[string][]byte{
		"profiles/x/claude/a.md": []byte("A-local"),
		"profiles/x/claude/b.md": []byte("B-local"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("local policy resolutions = %v, want %v", got, want)
	}
}

// TestResolutionsFromPolicy_Cloud pins that "cloud" writes repo
// bytes back to local. Mirror-machine UX depends on this —
// anything else and a secondary machine would push its stale
// local over the fleet's authoritative copy.
func TestResolutionsFromPolicy_Cloud(t *testing.T) {
	conflicts := []FileConflict{
		{Path: "profiles/x/claude/a.md", LocalData: []byte("A-local"), RemoteData: []byte("A-remote")},
	}
	got := ResolutionsFromPolicy(conflicts, "cloud")
	if string(got["profiles/x/claude/a.md"]) != "A-remote" {
		t.Errorf("cloud policy: resolution = %q, want A-remote", got["profiles/x/claude/a.md"])
	}
}

// TestResolutionsFromPolicy_AskReturnsNil pins that "ask" (and
// unrecognised values) return nil so callers can distinguish
// "use this map" from "fall back to the picker." A non-nil empty
// map would be taken by ApplyResolutions to mean "no resolutions"
// and silently drop all conflicts — wrong for manual mode.
func TestResolutionsFromPolicy_AskReturnsNil(t *testing.T) {
	conflicts := []FileConflict{{Path: "x", LocalData: []byte("l"), RemoteData: []byte("r")}}
	if got := ResolutionsFromPolicy(conflicts, "ask"); got != nil {
		t.Errorf("ask policy should return nil, got %v", got)
	}
	if got := ResolutionsFromPolicy(conflicts, ""); got != nil {
		t.Errorf("empty policy should return nil, got %v", got)
	}
	if got := ResolutionsFromPolicy(conflicts, "garbage"); got != nil {
		t.Errorf("unknown policy should return nil, got %v", got)
	}
}

// TestAnyDeleteVsModify_NilDataEscapes pins the first escape
// case: a conflict where one side is absent (nil bytes) means
// one machine deleted and the other modified. Automated policies
// MUST escape to the picker for these — silently "taking this
// machine's" when "this machine's" is a delete would erase the
// other side's recent edit with no paper trail.
func TestAnyDeleteVsModify_NilDataEscapes(t *testing.T) {
	got := AnyDeleteVsModify([]FileConflict{
		{Path: "clean", LocalData: []byte("l"), RemoteData: []byte("r")},
		{Path: "deleted-remote", LocalData: []byte("l"), RemoteData: nil},
	})
	if !got {
		t.Error("expected escape on nil RemoteData (remote side deleted, local modified)")
	}
}

// TestAnyDeleteVsModify_JSONDeleteModEscapes pins the JSON
// variant: merge.ConflictJSONDeleteMod flags a key that existed in
// one JSON side and was removed from the other. Same destructive
// concern as whole-file delete-vs-modify — escape to picker so the
// user decides whether to resurrect or commit the deletion.
func TestAnyDeleteVsModify_JSONDeleteModEscapes(t *testing.T) {
	got := AnyDeleteVsModify([]FileConflict{
		{
			Path:      "x",
			LocalData: []byte(`{"a":1}`), RemoteData: []byte(`{}`),
			Conflicts: []merge.Conflict{{Path: "a", Kind: merge.ConflictJSONDeleteMod, LocalPresent: true, RemotePresent: false}},
		},
	})
	if !got {
		t.Error("expected escape on JSONDeleteMod sub-conflict")
	}
}

// TestAnyDeleteVsModify_AllCleanAllowsPolicy pins the happy path:
// plain simultaneous edits with both sides present → policy can
// safely auto-resolve. No escape needed.
func TestAnyDeleteVsModify_AllCleanAllowsPolicy(t *testing.T) {
	got := AnyDeleteVsModify([]FileConflict{
		{Path: "x", LocalData: []byte("l"), RemoteData: []byte("r")},
		{Path: "y", LocalData: []byte("lx"), RemoteData: []byte("ry")},
	})
	if got {
		t.Error("plain edit conflicts shouldn't trigger the delete-vs-modify escape")
	}
}
