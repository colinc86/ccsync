// Package doctor runs integrity checks over a ccsync installation and the
// user's on-disk state. Return value drives the TUI Doctor screen and the
// ccsync doctor CLI (non-zero exit on issue).
package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	cryptopkg "github.com/colinc86/ccsync/internal/crypto"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/snapshot"
)

// Severity grades a check finding.
type Severity int

const (
	SeverityOK Severity = iota
	SeverityWarn
	SeverityFail
)

func (s Severity) String() string {
	switch s {
	case SeverityOK:
		return "OK"
	case SeverityWarn:
		return "WARN"
	case SeverityFail:
		return "FAIL"
	}
	return "?"
}

// Finding is one check result.
type Finding struct {
	Check    string
	Severity Severity
	Message  string
	Suggest  string
	// Fix, when non-nil, auto-repairs the issue. The CLI --fix flag and
	// the TUI Doctor "f" action iterate findings and invoke Fix for any
	// that have one. Fixes that need user input (pasting a missing
	// secret, confirming a destructive op) leave Fix nil.
	Fix func() error `json:"-"`
}

// Inputs describe what to check.
type Inputs struct {
	ClaudeDir  string
	ClaudeJSON string
	RepoPath   string
	StateDir   string
}

// Report groups findings.
type Report struct {
	Findings []Finding
}

// Worst returns the worst severity across findings.
func (r Report) Worst() Severity {
	w := SeverityOK
	for _, f := range r.Findings {
		if f.Severity > w {
			w = f.Severity
		}
	}
	return w
}

// ApplyFixes runs Fix on every finding that has one. Returns per-finding
// error for those that failed plus a count of successful fixes. Does not
// re-run checks afterward — caller invokes Check() again to see the
// updated state.
func (r Report) ApplyFixes() (applied int, errs []error) {
	for _, f := range r.Findings {
		if f.Fix == nil {
			continue
		}
		if err := f.Fix(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Check, err))
			continue
		}
		applied++
	}
	return applied, errs
}

// Check runs all integrity checks and returns a report.
func Check(in Inputs) Report {
	r := Report{}
	r.Findings = append(r.Findings,
		checkDanglingPlaceholders(in.ClaudeJSON, "~/.claude.json"),
		checkDanglingPlaceholders(filepath.Join(in.ClaudeDir, "settings.json"), "~/.claude/settings.json"),
		checkRepo(in.RepoPath),
		checkEncryption(in.RepoPath),
		checkSnapshots(filepath.Join(in.StateDir, "snapshots")),
	)
	return r
}

func checkDanglingPlaceholders(path, display string) Finding {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Finding{Check: "placeholders:" + display, Severity: SeverityOK,
				Message: "file not present (that's OK; it may not exist on this machine)"}
		}
		return Finding{Check: "placeholders:" + display, Severity: SeverityWarn,
			Message: fmt.Sprintf("couldn't read: %v", err)}
	}
	if strings.Contains(string(data), "<<REDACTED:ccsync:") {
		return Finding{
			Check:    "placeholders:" + display,
			Severity: SeverityFail,
			Message:  display + " contains unresolved redaction placeholders",
			Suggest:  "open ccsync → RedactionReview to paste missing secrets",
		}
	}
	return Finding{Check: "placeholders:" + display, Severity: SeverityOK,
		Message: "no dangling placeholders"}
}

func checkRepo(path string) Finding {
	if path == "" {
		return Finding{Check: "repo", Severity: SeverityWarn,
			Message: "no sync repo configured (run Bootstrap)"}
	}
	if _, err := gitx.Open(path); err != nil {
		return Finding{Check: "repo", Severity: SeverityFail,
			Message:  fmt.Sprintf("can't open sync repo at %s: %v", path, err),
			Suggest:  "re-clone from the remote URL, or run Bootstrap",
		}
	}
	return Finding{Check: "repo", Severity: SeverityOK,
		Message: "sync repo opens cleanly"}
}

// checkEncryption verifies that an encrypted repo is actually unlockable
// from this machine — marker readable, passphrase present in keychain,
// and at least one encrypted blob decryptable with the derived key.
// Catches the "repo was encrypted on another machine, this one has no
// passphrase yet, all syncs will silently fail" footgun.
func checkEncryption(repoPath string) Finding {
	check := "encryption"
	if repoPath == "" {
		return Finding{Check: check, Severity: SeverityOK,
			Message: "no repo — no encryption to check"}
	}
	marker, err := cryptopkg.ReadMarker(repoPath)
	if err != nil {
		return Finding{Check: check, Severity: SeverityWarn,
			Message: fmt.Sprintf("couldn't read encryption marker: %v", err)}
	}
	if marker == nil {
		return Finding{Check: check, Severity: SeverityOK,
			Message: "repo is plaintext (encryption not enabled)"}
	}
	pp, err := secrets.Fetch("repo-encryption-passphrase")
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			// Half-migration check: marker exists but no passphrase and
			// no ciphertext in the repo — that's a dead marker from a
			// failed enable. Auto-fixable by removing the marker.
			sample, _ := firstCiphertext(repoPath)
			f := Finding{
				Check:    check,
				Severity: SeverityFail,
				Message:  "repo is encrypted but this machine has no passphrase stored",
				Suggest:  "run `ccsync unlock` (or the TUI's Settings → repo encryption)",
			}
			if sample == nil {
				f.Message = "encryption marker exists but repo has no ciphertext (half-migration leftover)"
				f.Suggest = "ccsync doctor --fix will remove the marker"
				f.Fix = func() error { return cryptopkg.RemoveMarker(repoPath) }
			}
			return f
		}
		return Finding{Check: check, Severity: SeverityWarn,
			Message: fmt.Sprintf("couldn't read passphrase from keychain: %v", err)}
	}
	key, err := marker.DeriveKey(pp)
	if err != nil {
		return Finding{Check: check, Severity: SeverityFail,
			Message: fmt.Sprintf("can't derive key from stored passphrase: %v", err)}
	}
	// Find any encrypted blob and trial-decrypt. If the repo has zero
	// encrypted files (fresh enable, nothing to encrypt yet), we pass —
	// there's nothing to verify against.
	sample, err := firstCiphertext(repoPath)
	if err != nil {
		return Finding{Check: check, Severity: SeverityWarn,
			Message: fmt.Sprintf("walking for ciphertext: %v", err)}
	}
	if sample == nil {
		return Finding{Check: check, Severity: SeverityOK,
			Message: "encrypted (no ciphertext to verify against yet)"}
	}
	if _, err := cryptopkg.Decrypt(key, sample); err != nil {
		return Finding{
			Check:    check,
			Severity: SeverityFail,
			Message:  "stored passphrase no longer decrypts the repo",
			Suggest:  "run `ccsync unlock` to re-enter the correct passphrase",
		}
	}
	return Finding{Check: check, Severity: SeverityOK,
		Message: "encrypted and unlockable from this machine"}
}

// firstCiphertext returns the bytes of the first encrypted file under
// repoPath (skipping .git). nil bytes + nil error when there isn't one.
func firstCiphertext(repoPath string) ([]byte, error) {
	var result []byte
	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if cryptopkg.HasMagic(data) {
			result = data
			return filepath.SkipAll
		}
		return nil
	})
	return result, err
}

func checkSnapshots(root string) Finding {
	snaps, err := snapshot.List(root)
	if err != nil {
		return Finding{Check: "snapshots", Severity: SeverityWarn,
			Message: fmt.Sprintf("couldn't list snapshots: %v", err)}
	}
	// Scan for orphan dirs — subdirectories of root that snapshot.List
	// couldn't parse (missing/corrupt meta.json). They're unusable and
	// can be cleaned up by --fix.
	orphans := orphanSnapshotDirs(root)
	if len(orphans) > 0 {
		return Finding{
			Check:    "snapshots",
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("%d snapshot(s) retained, %d orphan dir(s) (missing meta.json)", len(snaps), len(orphans)),
			Suggest:  "ccsync doctor --fix will remove the orphan dirs",
			Fix: func() error {
				for _, dir := range orphans {
					if err := os.RemoveAll(dir); err != nil {
						return err
					}
				}
				return nil
			},
		}
	}
	return Finding{Check: "snapshots", Severity: SeverityOK,
		Message: fmt.Sprintf("%d snapshot(s) retained", len(snaps))}
}

// orphanSnapshotDirs returns absolute paths of subdirs of root that lack
// a readable meta.json. They can't be listed or restored, so they're
// dead weight — safe to remove.
func orphanSnapshotDirs(root string) []string {
	var out []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta := filepath.Join(root, e.Name(), "meta.json")
		if _, err := os.Stat(meta); err != nil {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}
