package jsonfilter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/tidwall/sjson"
)

// FilterResult is the output of Apply.
type FilterResult struct {
	Data       []byte                     // filtered, redacted JSON
	Redactions map[string]json.RawMessage // concrete path → original JSON-encoded value
}

// Apply runs rule against data and returns filtered JSON plus extracted secrets.
// profile is embedded in redaction placeholders for keychain lookup.
func Apply(data []byte, rule config.JSONFileRule, profile string) (FilterResult, error) {
	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return FilterResult{}, fmt.Errorf("parse: %w", err)
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		return FilterResult{}, err
	}

	// 1. Exclude
	for _, pat := range rule.Exclude {
		m, err := Compile(pat)
		if err != nil {
			return FilterResult{}, fmt.Errorf("bad exclude %q: %w", pat, err)
		}
		paths := m.Match(parsed)
		sort.Sort(sort.Reverse(sort.StringSlice(paths)))
		for _, p := range paths {
			var derr error
			out, derr = sjson.DeleteBytes(out, p)
			if derr != nil {
				return FilterResult{}, fmt.Errorf("delete %q: %w", p, derr)
			}
		}
		if err := json.Unmarshal(out, &parsed); err != nil {
			return FilterResult{}, err
		}
	}

	// 2. Redact
	redactions := map[string]json.RawMessage{}
	for _, pat := range rule.Redact {
		m, err := Compile(pat)
		if err != nil {
			return FilterResult{}, fmt.Errorf("bad redact %q: %w", pat, err)
		}
		for _, p := range m.Match(parsed) {
			val := extract(parsed, p)
			raw, err := json.Marshal(val)
			if err != nil {
				return FilterResult{}, err
			}
			redactions[p] = raw
			placeholder := fmt.Sprintf("<<REDACTED:ccsync:%s:%s>>", profile, p)
			out, err = sjson.SetBytes(out, p, placeholder)
			if err != nil {
				return FilterResult{}, fmt.Errorf("redact %q: %w", p, err)
			}
		}
		if err := json.Unmarshal(out, &parsed); err != nil {
			return FilterResult{}, err
		}
	}

	// 3. Include: if set and not bare root, keep only the included subtrees
	keepAll := len(rule.Include) == 0 || slices.Contains(rule.Include, "$")
	if !keepAll {
		filtered := map[string]interface{}{}
		for _, pat := range rule.Include {
			m, err := Compile(pat)
			if err != nil {
				return FilterResult{}, fmt.Errorf("bad include %q: %w", pat, err)
			}
			for _, p := range m.Match(parsed) {
				insertIntoMap(filtered, p, extract(parsed, p))
			}
		}
		out, err = json.Marshal(filtered)
		if err != nil {
			return FilterResult{}, err
		}
	}

	pretty, err := prettyJSON(out)
	if err != nil {
		return FilterResult{}, err
	}
	return FilterResult{Data: pretty, Redactions: redactions}, nil
}

// PreserveLocalExcludes splices values at `excludes` paths from the local
// original file into the incoming filtered bytes. Prevents the full-file
// overwrite on pull from wiping machine-local keys (oauthAccount,
// userID, permissions.allow, sessionId, …) that the filter legitimately
// kept out of the repo but that still need to live on this machine.
//
// Contract: incoming is what we're about to write (filtered + redaction-
// restored). localOriginal is the bytes currently on disk, or empty for
// a fresh machine. excludes are the same JSONPath-lite patterns that
// produced the filter — we re-compile each, match against the local
// original, and for every matched concrete path we stamp the local's
// value into the incoming document. Result: incoming's syncable fields
// are applied; local's machine-only fields are preserved. Local file
// missing or unparseable → returns incoming unchanged, with no error
// (first-run is a legitimate AddLocal, not a bug).
func PreserveLocalExcludes(incoming, localOriginal []byte, excludes []string) ([]byte, error) {
	if len(localOriginal) == 0 || len(excludes) == 0 {
		return incoming, nil
	}
	var localParsed any
	if err := json.Unmarshal(localOriginal, &localParsed); err != nil {
		// Local file isn't valid JSON — safer to just write incoming and
		// let the user re-establish any machine-local state manually than
		// to crash mid-sync. Rare; local should always be something we
		// wrote ourselves.
		return incoming, nil
	}
	out := incoming
	for _, pat := range excludes {
		m, err := Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("bad exclude %q: %w", pat, err)
		}
		for _, path := range m.Match(localParsed) {
			val := extract(localParsed, path)
			if val == nil {
				continue
			}
			raw, err := json.Marshal(val)
			if err != nil {
				return nil, err
			}
			out, err = sjson.SetRawBytes(out, path, raw)
			if err != nil {
				return nil, fmt.Errorf("splice %q: %w", path, err)
			}
		}
	}
	// Re-pretty so key ordering stays deterministic after splices.
	pretty, err := prettyJSON(out)
	if err != nil {
		return out, nil // best-effort; original write still works
	}
	return pretty, nil
}

// RestoreResult is the output of Restore.
type RestoreResult struct {
	Data    []byte
	Missing []string // placeholder paths that couldn't be resolved
}

// Restore re-inserts redacted values into filtered data. values maps concrete
// JSON path → JSON-encoded original value (as stored in the keyring).
func Restore(data []byte, values map[string]string) (RestoreResult, error) {
	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return RestoreResult{}, err
	}
	sites := findPlaceholders(parsed, "")

	out := data
	var missing []string
	for _, s := range sites {
		raw, ok := values[s.Path]
		if !ok {
			missing = append(missing, s.Path)
			continue
		}
		var err error
		out, err = sjson.SetRawBytes(out, s.Path, []byte(raw))
		if err != nil {
			return RestoreResult{}, fmt.Errorf("restore %q: %w", s.Path, err)
		}
	}
	pretty, err := prettyJSON(out)
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{Data: pretty, Missing: missing}, nil
}

type placeholderSite struct {
	Path         string
	Profile      string
	OriginalPath string
}

var placeholderRE = regexp.MustCompile(`^<<REDACTED:ccsync:([^:]+):(.+)>>$`)

func findPlaceholders(node interface{}, prefix string) []placeholderSite {
	var sites []placeholderSite
	switch n := node.(type) {
	case map[string]interface{}:
		for k, v := range n {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			sites = append(sites, findPlaceholders(v, path)...)
		}
	case []interface{}:
		for i, v := range n {
			path := fmt.Sprintf("%s.%d", prefix, i)
			sites = append(sites, findPlaceholders(v, path)...)
		}
	case string:
		if m := placeholderRE.FindStringSubmatch(n); m != nil {
			sites = append(sites, placeholderSite{
				Path:         prefix,
				Profile:      m[1],
				OriginalPath: m[2],
			})
		}
	}
	return sites
}

func extract(doc interface{}, path string) interface{} {
	if path == "" {
		return doc
	}
	parts := strings.Split(path, ".")
	cur := doc
	for _, p := range parts {
		switch v := cur.(type) {
		case map[string]interface{}:
			cur = v[p]
		case []interface{}:
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil
			}
			cur = v[idx]
		default:
			return nil
		}
	}
	return cur
}

func insertIntoMap(target map[string]interface{}, path string, val interface{}) {
	parts := strings.Split(path, ".")
	cur := target
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = val
			return
		}
		next, ok := cur[p].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			cur[p] = next
		}
		cur = next
	}
}

func prettyJSON(data []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
