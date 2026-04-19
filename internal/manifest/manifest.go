// Package manifest loads, saves, and maintains the authoritative file
// manifest at the root of the sync repo. It's the three-way base for the
// sync engine: each entry records SHA256, size, mtime, and the host UUID
// that last wrote the file.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

const FormatVersion = 1

// Manifest is the top-level manifest.json schema.
type Manifest struct {
	Version   int              `json:"version"`
	UpdatedAt time.Time        `json:"updatedAt"`
	UpdatedBy string           `json:"updatedBy"`
	Files     map[string]Entry `json:"files"`
}

// Entry records metadata for one tracked file (key = slash-separated path).
type Entry struct {
	SHA256         string    `json:"sha256"`
	Size           int64     `json:"size"`
	MTime          time.Time `json:"mtime"`
	LastModifiedBy string    `json:"lastModifiedBy"`
}

// New returns an empty manifest owned by hostUUID.
func New(hostUUID string) *Manifest {
	return &Manifest{
		Version:   FormatVersion,
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: hostUUID,
		Files:     map[string]Entry{},
	}
}

// Load reads a manifest from disk. If path does not exist, returns an empty
// manifest — that's the legitimate first-run case.
func Load(path, hostUUID string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(hostUUID), nil
		}
		return nil, err
	}
	defer f.Close()

	var m Manifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m.Files == nil {
		m.Files = map[string]Entry{}
	}
	return &m, nil
}

// Save atomically writes the manifest to disk.
func (m *Manifest) Save(path string) error {
	m.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SortedPaths returns manifest keys in deterministic order.
func (m *Manifest) SortedPaths() []string {
	out := make([]string, 0, len(m.Files))
	for k := range m.Files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Get returns the entry for path and whether it exists.
func (m *Manifest) Get(path string) (Entry, bool) {
	e, ok := m.Files[path]
	return e, ok
}

// Set records an entry for path.
func (m *Manifest) Set(path string, e Entry) {
	m.Files[path] = e
}

// Delete removes an entry.
func (m *Manifest) Delete(path string) {
	delete(m.Files, path)
}

// SHA256File computes the hex digest of the given file's contents.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256Bytes computes the hex digest of the given bytes.
func SHA256Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
