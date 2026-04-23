package tui

import "testing"

// TestWrapCursor pins the shared wrap helper used by every list-
// backed screen. Up-at-top lands on the last item, down-at-bottom
// lands on the first, and empty lists return the cursor unchanged
// so callers don't have to guard against mod-by-zero.
func TestWrapCursor(t *testing.T) {
	cases := []struct {
		name          string
		cur, n, delta int
		want          int
	}{
		{"middle forward", 2, 5, +1, 3},
		{"middle back", 2, 5, -1, 1},
		{"down at bottom wraps to zero", 4, 5, +1, 0},
		{"up at top wraps to last", 0, 5, -1, 4},
		{"single-item list stays", 0, 1, +1, 0},
		{"empty list returns input", 0, 0, +1, 0},
		{"negative n returns input", 3, -2, +1, 3},
		{"large delta still wraps", 0, 3, +7, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := wrapCursor(c.cur, c.n, c.delta)
			if got != c.want {
				t.Errorf("wrapCursor(cur=%d,n=%d,delta=%+d) = %d; want %d",
					c.cur, c.n, c.delta, got, c.want)
			}
		})
	}
}
