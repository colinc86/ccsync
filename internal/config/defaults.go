package config

import _ "embed"

//go:embed defaults.yaml
var defaultYAML []byte

// DefaultYAML returns the embedded default ccsync.yaml bytes.
func DefaultYAML() []byte { return defaultYAML }

// DefaultGitignore returns the .gitignore bytes the repo root should
// carry. The leading slash in each pattern restricts the match to repo
// root so user-authored ~/.claude content can include (harmless) .bak
// or .tmp files without them being filtered out of the sync.
//
// Scope: atomic-write artifacts that the ccsync orchestrator itself
// creates at the repo root —
//   - ccsync.yaml.bak (intentional rollback sibling from SaveWithBackup)
//   - *.tmp files produced by the tmp+rename pattern if a crash
//     interrupts the rename step
//
// Pre-fix, SaveWithBackup wrote ccsync.yaml.bak next to ccsync.yaml at
// the repo root and AddAll happily staged it. The .bak got committed and
// then proliferated across every machine's clone — every user's tree
// acquired a stale copy of some past config, and a crash mid-rename
// could publish a .tmp fragment to the whole fleet.
func DefaultGitignore() []byte {
	return []byte("/*.bak\n/*.tmp\n")
}
