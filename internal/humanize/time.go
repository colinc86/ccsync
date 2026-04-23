package humanize

import (
	"fmt"
	"time"
)

// Ago returns a coarse-grained human-readable "time since t" string like
// "2m ago", "3h ago", "just now". Zero or future times return "" so
// callers can unconditionally concat the result into a status line.
//
// Grain: <30s "just now"; <60m whole minutes; <24h whole hours; <14d
// whole days; else a UTC date. Coarser than a dashboard needs for
// second-precision, which is the point — "last synced 12m ago" is
// what the user actually wants.
func Ago(t time.Time) string {
	return agoFrom(t, time.Now())
}

// agoFrom is Ago with an injectable "now" for testing.
func agoFrom(t, now time.Time) string {
	if t.IsZero() || t.After(now) {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < 30*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.UTC().Format("2006-01-02")
	}
}
