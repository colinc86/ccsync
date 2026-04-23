// Package snapshot creates timestamped copies of tracked files before any
// write-to-disk operation. It's Layer 1 of the backup/safety model — every
// sync that writes to disk leaves a snapshot users can revert from.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const metaFile = "meta.json"

// Meta is the per-snapshot metadata stored as meta.json inside the snapshot dir.
type Meta struct {
	ID        string    `json:"id"`
	Op        string    `json:"op"`
	CreatedAt time.Time `json:"createdAt"`
	Profile   string    `json:"profile,omitempty"`
	Files     []string  `json:"files"` // absolute paths captured
	Pinned    bool      `json:"pinned,omitempty"`
}

// Take copies each existing file in absPaths into root/<id>/. Returns the ID.
// Non-existent paths are silently skipped (a file may be pending-create).
func Take(root, op, profile string, absPaths []string) (Meta, error) {
	// ID disambiguator: the wall-clock format has one-second
	// granularity, so two Takes within the same second would collide,
	// the second's MkdirAll would silently accept the existing dir,
	// and its writes would overwrite the first's snapshot — silent
	// data loss on the rollback-safety path. Append nanoseconds so
	// wall-clock-unique Takes stay distinct; append PID as a final
	// tiebreaker for OS configurations where time.Now() has coarser
	// resolution than one nanosecond (Windows with QPC quirks, some
	// virtualised clocks) or for tests that mock time.
	now := time.Now().UTC()
	id := now.Format("20060102T150405.000000000Z") + "-" + strconv.Itoa(os.Getpid()) + "-" + op
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Meta{}, err
	}

	captured := make([]string, 0, len(absPaths))
	for _, abs := range absPaths {
		data, err := os.ReadFile(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Meta{}, fmt.Errorf("read %s: %w", abs, err)
		}
		dst := filepath.Join(dir, mirrorPath(abs))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return Meta{}, err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return Meta{}, err
		}
		captured = append(captured, abs)
	}

	m := Meta{
		ID:        id,
		Op:        op,
		CreatedAt: now,
		Profile:   profile,
		Files:     captured,
	}
	if err := writeMeta(dir, m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

// List returns snapshot metadata, newest first.
func List(root string) ([]Meta, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := readMeta(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	// Stable sort with ID as tiebreaker so ties in CreatedAt (possible
	// when wall-clock resolution is coarser than one nanosecond or
	// when tests mock time) produce a deterministic order. Without
	// this, Prune could evict the wrong snapshot among equally-aged
	// siblings — rare but non-deterministic.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// Restore copies each captured file from the snapshot back to its
// original path. Read-all-then-write-all so a corrupt snapshot fails
// before any live file is touched — the pre-v0.6.8 code read-and-
// wrote in the same loop, which left the user in a half-rolled-back
// state if any source file was missing partway through.
func Restore(root, id string) error {
	dir := filepath.Join(root, id)
	m, err := readMeta(dir)
	if err != nil {
		return err
	}

	// Phase 1: read every source before touching local disk. Any
	// corruption here aborts the restore with zero writes applied.
	type restoreOp struct {
		dst  string
		data []byte
	}
	ops := make([]restoreOp, 0, len(m.Files))
	for _, abs := range m.Files {
		src := filepath.Join(dir, mirrorPath(abs))
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read snapshot %s: %w", src, err)
		}
		ops = append(ops, restoreOp{dst: abs, data: data})
	}

	// Phase 2: write each file. A mid-flight write failure can still
	// leave partial state — truly atomic multi-file restore would
	// need per-file tempfile+rename with a post-commit cleanup — but
	// this upgrades the common "corrupt snapshot" failure from
	// partial-restore to no-op, which is what the rollback safety
	// contract actually promises.
	for _, op := range ops {
		if err := os.MkdirAll(filepath.Dir(op.dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(op.dst, op.data, 0o644); err != nil {
			return fmt.Errorf("restore %s: %w", op.dst, err)
		}
	}
	return nil
}

// Pin marks a snapshot as exempt from pruning.
func Pin(root, id string) error {
	dir := filepath.Join(root, id)
	m, err := readMeta(dir)
	if err != nil {
		return err
	}
	m.Pinned = true
	return writeMeta(dir, m)
}

// Prune deletes snapshots older than keepWithin, except the most recent
// keepCount and any pinned snapshots.
func Prune(root string, keepCount int, keepWithin time.Duration) error {
	snaps, err := List(root)
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-keepWithin)
	for i, s := range snaps {
		if i < keepCount || s.Pinned || s.CreatedAt.After(cutoff) {
			continue
		}
		dir := filepath.Join(root, s.ID)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

// mirrorPath turns an absolute path into a snapshot-internal relative path
// that round-trips back to the same absolute location on Restore.
// Example: "/Users/c/.claude/x" → "Users/c/.claude/x".
func mirrorPath(abs string) string {
	return strings.TrimPrefix(filepath.ToSlash(abs), "/")
}

func writeMeta(dir string, m Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, metaFile), data, 0o600)
}

func readMeta(dir string) (Meta, error) {
	data, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
}
