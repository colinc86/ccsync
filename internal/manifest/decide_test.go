package manifest

import "testing"

func TestDecide(t *testing.T) {
	// Key: L=local, B=base, R=remote. Empty string = absent.
	cases := []struct {
		name                string
		local, base, remote string
		want                Action
	}{
		// 8 absence combinations
		{"all-absent", "", "", "", ActionNoOp},
		{"remote-only", "", "", "r", ActionAddLocal},
		{"local-only", "l", "", "", ActionAddRemote},
		{"both-added-same", "x", "", "x", ActionNoOp},
		{"both-added-diff", "l", "", "r", ActionConflict},
		{"both-deleted", "", "b", "", ActionNoOp},
		{"local-deleted-remote-unchanged", "", "b", "b", ActionDeleteRemote},
		{"local-deleted-remote-modified", "", "b", "r", ActionConflict},
		{"remote-deleted-local-unchanged", "b", "b", "", ActionDeleteLocal},
		{"remote-deleted-local-modified", "l", "b", "", ActionConflict},

		// all three present
		{"noop-all-equal", "x", "x", "x", ActionNoOp},
		{"local-changed", "l", "b", "b", ActionPush},
		{"remote-changed", "b", "b", "r", ActionPull},
		{"both-changed-same", "x", "b", "x", ActionNoOp},
		{"both-changed-diff", "l", "b", "r", ActionMerge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.local, tc.base, tc.remote)
			if got != tc.want {
				t.Errorf("Decide(%q,%q,%q) = %s, want %s",
					tc.local, tc.base, tc.remote, got, tc.want)
			}
		})
	}
}

func TestActionString(t *testing.T) {
	if ActionMerge.String() != "Merge" {
		t.Errorf("ActionMerge string = %q", ActionMerge.String())
	}
	if Action(999).String() != "Unknown" {
		t.Errorf("out-of-range action should be Unknown")
	}
}
