package merge

import "time"

// Binary performs a last-write-wins merge for opaque content.
// If one side is absent (nil), the other wins regardless of mtime.
func Binary(local []byte, localMTime time.Time, remote []byte, remoteMTime time.Time) Result {
	if local == nil && remote == nil {
		return Result{}
	}
	if local == nil {
		return Result{Merged: remote}
	}
	if remote == nil {
		return Result{Merged: local}
	}
	if localMTime.After(remoteMTime) {
		return Result{Merged: local}
	}
	return Result{Merged: remote}
}
