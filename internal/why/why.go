// Package why traces how ccsync's rules apply to a given path. It answers
// "why is (or isn't) this syncing?" by walking the same chain of rules the
// sync engine walks: .syncignore, profile exclude, jsonFiles include/exclude/
// redact. Pure function — no I/O beyond what the caller passes in.
package why

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/humanize"
	"github.com/colinc86/ccsync/internal/ignore"
	"github.com/colinc86/ccsync/internal/jsonfilter"
)

// Outcome is the final answer for a Why trace.
type Outcome int

const (
	// OutcomeSynced: the path would be part of normal sync under this profile.
	OutcomeSynced Outcome = iota
	// OutcomeSyncignored: .syncignore excluded it repo-wide.
	OutcomeSyncignored
	// OutcomeProfileExcluded: the active profile refuses it on this machine.
	OutcomeProfileExcluded
	// OutcomeJSONIncluded: a JSON key kept by an include rule.
	OutcomeJSONIncluded
	// OutcomeJSONExcluded: a JSON key dropped by an exclude rule.
	OutcomeJSONExcluded
	// OutcomeJSONRedacted: a JSON key value replaced by a keychain placeholder.
	OutcomeJSONRedacted
	// OutcomeUnknown: the path didn't match any rule ccsync tracks.
	OutcomeUnknown
)

func (o Outcome) String() string {
	switch o {
	case OutcomeSynced:
		return "synced"
	case OutcomeSyncignored:
		return "ignored"
	case OutcomeProfileExcluded:
		return "excluded by profile"
	case OutcomeJSONIncluded:
		return "included (JSON)"
	case OutcomeJSONExcluded:
		return "excluded (JSON)"
	case OutcomeJSONRedacted:
		return "redacted (JSON)"
	case OutcomeUnknown:
		return "no rule matched"
	}
	return "?"
}

// Step is one entry in the rule-evaluation trace.
type Step struct {
	Rule    string // "syncignore", "profile.exclude", "jsonFiles.include", etc.
	Pattern string // the exact pattern tested, if any
	Matched bool
	Note    string // freeform human note
}

// Trace is the full result for one Why call.
type Trace struct {
	Path    string // normalized repo-relative path (e.g. "claude/agents/foo.md")
	JSONKey string // set when Path points inside a JSON file
	Profile string
	Chain   []string // profile extends chain
	Steps   []Step
	Outcome Outcome
}

// Inputs bundles everything Explain needs. All fields are optional; missing
// data simply skips the corresponding rule class.
type Inputs struct {
	Config     *config.Config
	Profile    string
	Syncignore string // raw contents of .syncignore from the repo (or cfg.DefaultSyncignore)
	ClaudeDir  string // absolute path to ~/.claude (used to recognize local abs paths)
	ClaudeJSON string // absolute path to ~/.claude.json
}

// Explain traces how the rules would treat `target`. Accepts three forms:
//  1. repo-relative path: "claude/agents/foo.md"
//  2. absolute local path: "/Users/you/.claude/agents/foo.md"
//  3. file + JSON key: "~/.claude.json:$.mcpServers.gemini"
func Explain(in Inputs, target string) (*Trace, error) {
	if in.Config == nil {
		return nil, fmt.Errorf("nil config")
	}
	profileName := in.Profile
	if profileName == "" {
		profileName = "default"
	}

	path, jsonKey := splitJSONTarget(target)
	path = normalizePath(path, in.ClaudeDir, in.ClaudeJSON)

	t := &Trace{Path: path, JSONKey: jsonKey, Profile: profileName, Outcome: OutcomeUnknown}

	resolved, err := config.EffectiveProfile(in.Config, profileName)
	if err != nil {
		return t, err
	}
	t.Chain = resolved.Chain

	// 1. .syncignore (repo-wide)
	syncignore := in.Syncignore
	if syncignore == "" {
		syncignore = in.Config.DefaultSyncignore
	}
	if matchedPat := firstMatch(syncignore, claudeRelFromRepoPath(path)); matchedPat != "" {
		t.Steps = append(t.Steps, Step{
			Rule: ".syncignore", Pattern: matchedPat, Matched: true,
			Note: "path is ignored repo-wide",
		})
		t.Outcome = OutcomeSyncignored
		// continue down to annotate further, but .syncignore is final
		return t, nil
	}
	t.Steps = append(t.Steps, Step{Rule: ".syncignore", Matched: false, Note: "no match"})

	// 2. profile.exclude (per-profile path deny-list)
	if resolved.HasExcludes() {
		if matchedPat := firstMatch(resolved.ExcludeRules(), path); matchedPat != "" {
			t.Steps = append(t.Steps, Step{
				Rule:    fmt.Sprintf("profile[%s].exclude", profileName),
				Pattern: matchedPat, Matched: true,
				Note: fmt.Sprintf("excluded on this machine (profile %q)", profileName),
			})
			t.Outcome = OutcomeProfileExcluded
			return t, nil
		}
		t.Steps = append(t.Steps, Step{
			Rule: fmt.Sprintf("profile[%s].exclude", profileName), Matched: false,
			Note: "no match in " + humanize.Count(len(resolved.PathExcludes), "pattern"),
		})
	}

	// 3. jsonFiles rules (if the path points at a configured JSON file)
	if rule, jsonPath, ok := resolveJSONRule(in.Config, path, in.ClaudeJSON); ok {
		// include/exclude/redact only matter for a specific key. If the user
		// asked about a whole JSON file, fall through to OutcomeSynced.
		if jsonKey == "" {
			t.Steps = append(t.Steps, Step{
				Rule:    "jsonFiles[" + jsonPath + "]",
				Matched: true,
				Note: fmt.Sprintf("%d include / %d exclude / %d redact rules applied on this file",
					len(rule.Include), len(rule.Exclude), len(rule.Redact)),
			})
		} else {
			// Test each list in order — same precedence as jsonfilter.Apply:
			// exclude wins over redact wins over include.
			if matched := firstJSONMatch(rule.Exclude, jsonKey); matched != "" {
				t.Steps = append(t.Steps, Step{
					Rule:    "jsonFiles[" + jsonPath + "].exclude",
					Pattern: matched, Matched: true,
					Note: "this key is dropped before push",
				})
				t.Outcome = OutcomeJSONExcluded
				return t, nil
			}
			if matched := firstJSONMatch(rule.Redact, jsonKey); matched != "" {
				t.Steps = append(t.Steps, Step{
					Rule:    "jsonFiles[" + jsonPath + "].redact",
					Pattern: matched, Matched: true,
					Note: "value extracted to keychain; placeholder committed instead",
				})
				t.Outcome = OutcomeJSONRedacted
				return t, nil
			}
			if matched := firstJSONMatch(rule.Include, jsonKey); matched != "" {
				t.Steps = append(t.Steps, Step{
					Rule:    "jsonFiles[" + jsonPath + "].include",
					Pattern: matched, Matched: true,
					Note: "key passes through unchanged",
				})
				t.Outcome = OutcomeJSONIncluded
				return t, nil
			}
			// No explicit match; `include: ["$"]` means "take everything"
			if hasRootInclude(rule.Include) {
				t.Steps = append(t.Steps, Step{
					Rule:    "jsonFiles[" + jsonPath + "].include",
					Pattern: "$",
					Matched: true,
					Note:    "root include — every key passes through",
				})
				t.Outcome = OutcomeJSONIncluded
				return t, nil
			}
			t.Steps = append(t.Steps, Step{
				Rule: "jsonFiles[" + jsonPath + "]", Matched: false,
				Note: "no rule matched — this key is dropped unless `include: [$]`",
			})
			t.Outcome = OutcomeJSONExcluded
			return t, nil
		}
	}

	t.Outcome = OutcomeSynced
	return t, nil
}

// Format renders a Trace as human-readable multi-line text. Meant for
// `ccsync why` CLI output; the TUI overlay can use the same string or roll
// its own rendering off Trace.Steps.
func Format(t *Trace) string {
	if t == nil {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "  path:    %s\n", t.Path)
	if t.JSONKey != "" {
		fmt.Fprintf(&sb, "  key:     %s\n", t.JSONKey)
	}
	profileLabel := t.Profile
	if len(t.Chain) > 1 {
		profileLabel += " (extends " + strings.Join(t.Chain[1:], " ← ") + ")"
	}
	fmt.Fprintf(&sb, "  profile: %s\n\n", profileLabel)
	for _, s := range t.Steps {
		// → marks a rule that fired; · marks a rule that was checked and
		// didn't match. The Note text carries whether "fired" is good or bad.
		glyph := "·"
		if s.Matched {
			glyph = "→"
		}
		label := s.Rule
		if s.Pattern != "" {
			label += ": " + s.Pattern
		}
		fmt.Fprintf(&sb, "  %s %-40s %s\n", glyph, label, s.Note)
	}
	fmt.Fprintf(&sb, "\n  result:  %s\n", t.Outcome.String())
	return sb.String()
}

func firstMatch(rules, path string) string {
	m := ignore.New(rules)
	// go-gitignore doesn't expose which pattern matched, so we iterate manually.
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pm := ignore.New(line)
		if pm.Matches(path) {
			return line
		}
	}
	// Fallback: matcher says yes but no single line did (e.g. negation).
	if m.Matches(path) {
		return "(combined rules)"
	}
	return ""
}

// firstJSONMatch tests a JSON key against a list of patterns. A match covers
// either the key itself or any of its ancestor prefixes — this mirrors how
// jsonfilter.Apply treats include/exclude (e.g. excluding "$.projects" drops
// every key under it). Most-specific prefix wins.
func firstJSONMatch(patterns []string, key string) string {
	if key == "" {
		return ""
	}
	parts := strings.Split(key, ".")
	for depth := len(parts); depth >= 1; depth-- {
		sub := strings.Join(parts[:depth], ".")
		for _, pat := range patterns {
			m, err := jsonfilter.Compile(pat)
			if err != nil {
				continue
			}
			if m.MatchPath(sub) {
				return pat
			}
		}
	}
	return ""
}

func hasRootInclude(inc []string) bool {
	for _, s := range inc {
		if strings.TrimSpace(s) == "$" {
			return true
		}
	}
	return false
}

// splitJSONTarget accepts "file:$.json.path" and returns (file, jsonKey).
func splitJSONTarget(target string) (string, string) {
	if i := strings.Index(target, ":$"); i > 0 {
		return target[:i], strings.TrimPrefix(target[i+1:], "$.")
	}
	return target, ""
}

// normalizePath converts absolute local paths and ~-paths to repo-relative
// form ("claude/agents/foo.md", "claude.json"). Already-repo-relative paths
// pass through unchanged.
func normalizePath(p, claudeDir, claudeJSON string) string {
	if p == "" {
		return p
	}
	p = strings.TrimPrefix(p, "./")
	// absolute form: strip the user's Claude dir / Claude JSON path
	if claudeJSON != "" && (p == claudeJSON || p == filepath.ToSlash(claudeJSON)) {
		return "claude.json"
	}
	if claudeDir != "" {
		if strings.HasPrefix(p, claudeDir+"/") {
			return "claude/" + strings.TrimPrefix(p, claudeDir+"/")
		}
		if strings.HasPrefix(p, filepath.ToSlash(claudeDir)+"/") {
			return "claude/" + strings.TrimPrefix(p, filepath.ToSlash(claudeDir)+"/")
		}
	}
	// ~-prefix form
	if p == "~/.claude.json" {
		return "claude.json"
	}
	if strings.HasPrefix(p, "~/.claude/") {
		return "claude/" + strings.TrimPrefix(p, "~/.claude/")
	}
	return p
}

// claudeRelFromRepoPath strips the "claude/" prefix used for ~/.claude files,
// since .syncignore patterns are written relative to ~/.claude, not the repo.
func claudeRelFromRepoPath(p string) string {
	return strings.TrimPrefix(p, "claude/")
}

// resolveJSONRule looks up the jsonFiles rule that applies to a given
// repo-relative path. Returns the rule, the canonical key used in
// ccsync.yaml, and true if found.
func resolveJSONRule(cfg *config.Config, repoPath, claudeJSON string) (config.JSONFileRule, string, bool) {
	for key, rule := range cfg.JSONFiles {
		norm := normalizePath(key, "", claudeJSON)
		if norm == repoPath {
			return rule, key, true
		}
	}
	return config.JSONFileRule{}, "", false
}
