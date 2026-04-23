package humanize

import (
	"testing"
	"time"
)

func TestAgo(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, ""},
		{"future", now.Add(time.Hour), ""},
		{"just now", now.Add(-10 * time.Second), "just now"},
		{"29s still just now", now.Add(-29 * time.Second), "just now"},
		{"30s rounds up to 0m", now.Add(-30 * time.Second), "0m ago"},
		{"5 minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"59 minutes", now.Add(-59 * time.Minute), "59m ago"},
		{"1 hour", now.Add(-time.Hour), "1h ago"},
		{"23 hours", now.Add(-23 * time.Hour), "23h ago"},
		{"24 hours", now.Add(-24 * time.Hour), "1d ago"},
		{"3 days", now.Add(-3 * 24 * time.Hour), "3d ago"},
		{"2 weeks falls back to date", now.Add(-14 * 24 * time.Hour), "2026-04-08"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agoFrom(c.t, now)
			if got != c.want {
				t.Errorf("agoFrom(%v, %v) = %q, want %q", c.t, now, got, c.want)
			}
		})
	}
}
