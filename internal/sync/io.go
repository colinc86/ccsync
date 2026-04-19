package sync

import (
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via tmp + rename.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// listProfilesFromRepo enumerates subdirectories of <repo>/profiles/.
func listProfilesFromRepo(repoPath string) []string {
	entries, err := os.ReadDir(filepath.Join(repoPath, "profiles"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}
