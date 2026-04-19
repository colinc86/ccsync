// Package doctor runs integrity checks over a ccsync installation and the
// user's on-disk state. Return value drives the TUI Doctor screen and the
// ccsync doctor CLI (non-zero exit on issue).
package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/colinc86/ccsync/internal/gitx"
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

// Check runs all integrity checks and returns a report.
func Check(in Inputs) Report {
	r := Report{}
	r.Findings = append(r.Findings,
		checkDanglingPlaceholders(in.ClaudeJSON, "~/.claude.json"),
		checkDanglingPlaceholders(filepath.Join(in.ClaudeDir, "settings.json"), "~/.claude/settings.json"),
		checkRepo(in.RepoPath),
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

func checkSnapshots(root string) Finding {
	snaps, err := snapshot.List(root)
	if err != nil {
		return Finding{Check: "snapshots", Severity: SeverityWarn,
			Message: fmt.Sprintf("couldn't list snapshots: %v", err)}
	}
	return Finding{Check: "snapshots", Severity: SeverityOK,
		Message: fmt.Sprintf("%d snapshot(s) retained", len(snaps))}
}
