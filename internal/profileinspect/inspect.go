package profileinspect

import (
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/discover"
	"github.com/colinc86/ccsync/internal/ignore"
	"github.com/colinc86/ccsync/internal/state"
)

// Inputs is everything Inspect needs. All fields required except
// ClaudeJSON (may be empty when a user has no ~/.claude.json yet).
type Inputs struct {
	Config     *config.Config
	State      *state.State
	ClaudeDir  string // absolute path to ~/.claude
	ClaudeJSON string // absolute path to ~/.claude.json (may be empty)
	RepoPath   string // absolute path to ~/.ccsync/repo
}

// Inspect walks the user's local ~/.claude tree and the repo
// worktree for the active profile (including its extends chain),
// extracts title + description for each "thing", cross-references
// the two sides to assign a Status, and returns a View grouped by
// category. Pure function: no commits, no network, no state writes.
//
// The View's sections are ordered by sectionOrder (skills →
// commands → agents → mcp → memory → settings → CLAUDE.md → other).
// Empty sections are pruned so the TUI doesn't render zero-count
// headings. Items within each section sort alphabetically by title.
func Inspect(in Inputs) (*View, error) {
	profile := ""
	if in.State != nil {
		profile = in.State.ActiveProfile
	}
	if profile == "" {
		profile = "default"
	}
	view := &View{Profile: profile}
	if in.Config == nil {
		return view, nil
	}

	// 1. Resolve the extends chain so we can walk inherited content
	//    the same way sync.Run does.
	resolved, err := config.EffectiveProfile(in.Config, profile)
	if err != nil {
		return view, err
	}

	// 2. Walk local disk for pending-push detection + excluded
	//    marking. Uses the same .syncignore rules sync uses.
	localByRel, excluded := walkLocal(in)

	// 3. Walk repo side (every profile in the chain). Same
	//    .syncignore the local walker uses, so repo-side artefacts
	//    matching an ignore pattern don't show up as ghost
	//    pending-pulls against a local copy the sync engine
	//    legitimately skipped.
	syncMatcher := loadSyncignore(in.RepoPath, in.Config.DefaultSyncignore)
	repoByRel := walkRepoChain(in.RepoPath, resolved.Chain, syncMatcher)

	// Profile-excluded paths that ONLY exist on the repo side (never
	// pulled to local because the profile's exclude rule blocked
	// them) also need to flip to StatusExcluded. walkLocal only
	// annotates the excluded map for paths it saw on disk; pre-fix
	// everything else fell through to statusFor and reported
	// StatusPendingPull, tempting the user into a sync that fights
	// the exclusion their profile already declares. Apply the same
	// matcher to the repo-side keys so inherited excludes surface
	// correctly on a freshly-bootstrapped child profile.
	if resolved.HasExcludes() {
		profileMatcher := ignore.New(resolved.ExcludeRules())
		for rel := range repoByRel {
			if profileMatcher.Matches(rel) {
				excluded[rel] = true
			}
		}
	}

	// 4. Combine into Items. Each unique rel-path (relative to the
	//    active profile prefix) produces zero-or-more Items — mcp
	//    servers expand to one Item per server under the
	//    claude.json's mcpServers key.
	jsonRules := resolveAllJSONRules(in.Config, in.ClaudeDir, in.ClaudeJSON)
	// firstSync reflects whether this profile has ever been synced
	// on this machine. With an empty LastSyncedSHA, the engine's
	// first-sync-takes-remote rule (v0.6.4) will pull repo content
	// DOWN and overwrite local on any drift — so Inspect should
	// render drift rows as pending pull to match, not pending push.
	firstSync := in.State == nil || in.State.LastSyncedSHA[profile] == ""
	items := buildItems(localByRel, repoByRel, excluded, jsonRules, profile, firstSync)

	// 5. Group by Kind using a fixed order; prune empty sections.
	view.Sections = groupByKind(items)
	return view, nil
}

// localEntry captures the subset of discover.Entry we use.
type localEntry struct {
	AbsPath string
	RelPath string
	Bytes   []byte
	Sha     [32]byte
}

// walkLocal runs discover.Walk under the same .syncignore the sync
// engine uses, reads each tracked file's bytes, and returns a map
// keyed by the repo-relative RelPath (e.g. "claude/skills/x/SKILL.md"
// or "claude.json") plus an excluded set produced by the active
// profile's exclude rules.
func walkLocal(in Inputs) (map[string]*localEntry, map[string]bool) {
	out := map[string]*localEntry{}
	excluded := map[string]bool{}
	matcher := loadSyncignore(in.RepoPath, in.Config.DefaultSyncignore)
	disc, err := discover.Walk(discover.Inputs{
		ClaudeDir:  in.ClaudeDir,
		ClaudeJSON: in.ClaudeJSON,
	}, matcher)
	if err != nil {
		return out, excluded
	}
	var profileMatcher *ignore.Matcher
	if resolved, err := config.EffectiveProfile(in.Config, firstNonEmpty(in.State.ActiveProfile, "default")); err == nil {
		if resolved.HasExcludes() {
			profileMatcher = ignore.New(resolved.ExcludeRules())
		}
	}
	for _, e := range disc.Tracked {
		data, rerr := os.ReadFile(e.AbsPath)
		if rerr != nil {
			continue
		}
		out[e.RelPath] = &localEntry{
			AbsPath: e.AbsPath,
			RelPath: e.RelPath,
			Bytes:   data,
			Sha:     sha256.Sum256(data),
		}
		// Profile-level excludes are repo-path-shaped
		// ("claude/agents/secret.md") so the rel path matches.
		if profileMatcher != nil && profileMatcher.Matches(e.RelPath) {
			excluded[e.RelPath] = true
		}
	}
	return out, excluded
}

// walkRepoChain reads repo trees for every profile in chain (child
// first), projecting ancestor files into the child's rel-path
// namespace — identical to sync.Run's extends resolution. Returned
// map is keyed by the SAME rel-path shape walkLocal uses
// (no "profiles/<name>/" prefix) so the caller can simply match
// between the two maps.
func walkRepoChain(repoPath string, chain []string, syncMatcher *ignore.Matcher) map[string][]byte {
	out := map[string][]byte{}
	if repoPath == "" {
		return out
	}
	// Walk ancestors parent-first so child overrides land last.
	for i := len(chain) - 1; i >= 0; i-- {
		prefix := "profiles/" + chain[i] + "/"
		root := filepath.Join(repoPath, prefix)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			// .syncignore patterns are written relative to
			// ~/.claude, not to the repo tree, so strip the
			// leading "claude/" before matching (discover.go
			// applies the same normalisation on the local side).
			// claude.json sits at the root without the prefix and
			// falls through unchanged.
			matchRel := strings.TrimPrefix(rel, "claude/")
			if d.IsDir() {
				if syncMatcher != nil && syncMatcher.Matches(matchRel+"/") {
					return filepath.SkipDir
				}
				return nil
			}
			if syncMatcher != nil && syncMatcher.Matches(matchRel) {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			out[rel] = data
			return nil
		})
	}
	return out
}

// resolveAllJSONRules produces a rel-path → JSONFileRule map for
// every jsonFiles entry in the config. Keys are normalised to the
// repo-relative shape Inspect uses internally: "~/.claude.json" →
// "claude.json", "~/.claude/settings.json" → "claude/settings.json".
// Absolute-path keys are matched against the runtime-resolved
// ClaudeDir / ClaudeJSON and mapped the same way. Callers consult
// this map during status computation so any configured JSON file
// gets its sync engine-equivalent filter applied before byte
// comparison; without it, rules like the settings.json
// permissions.allow exclude show up as permanent drift.
func resolveAllJSONRules(cfg *config.Config, claudeDir, claudeJSON string) map[string]config.JSONFileRule {
	out := map[string]config.JSONFileRule{}
	if cfg == nil {
		return out
	}
	for key, rule := range cfg.JSONFiles {
		rel := ""
		switch {
		case key == "~/.claude.json" || key == claudeJSON:
			rel = "claude.json"
		case strings.HasPrefix(key, "~/.claude/"):
			rel = "claude/" + strings.TrimPrefix(key, "~/.claude/")
		case claudeDir != "" && strings.HasPrefix(key, claudeDir+"/"):
			rel = "claude/" + strings.TrimPrefix(key, claudeDir+"/")
		}
		if rel != "" {
			out[rel] = rule
		}
	}
	return out
}

// buildItems produces the flat Item list for the caller to group.
// Iterates every rel path known to either side, dispatches to the
// right extractor by category.Classify + file shape, and assigns a
// Status from a local-vs-repo cross-reference. jsonRules maps
// rel-path → ccsync.yaml JSONFileRule so any file with a redact /
// exclude policy goes through the same jsonfilter.Apply the sync
// engine uses before comparison — otherwise redacted secrets or
// explicitly-excluded keys (permissions.allow on settings.json,
// cachedGrowthBook* on claude.json) register as permanent drift.
func buildItems(local map[string]*localEntry, repo map[string][]byte, excluded map[string]bool, jsonRules map[string]config.JSONFileRule, profile string, firstSync bool) []Item {
	// Union of rel-paths across both sides.
	seen := map[string]struct{}{}
	for k := range local {
		seen[k] = struct{}{}
	}
	for k := range repo {
		seen[k] = struct{}{}
	}

	var items []Item
	for rel := range seen {
		// claude.json is special: its mcpServers keys each have an
		// independent sync identity, so we compare per-entry
		// instead of rubber-stamping every server with the file-
		// level status. The settings summary still inherits the
		// whole-file status because it's "everything in claude.json
		// that isn't mcpServers" — if those keys drift, the
		// summary row legitimately flips to pending-push.
		if rel == "claude.json" {
			var localData, repoData []byte
			if e, ok := local[rel]; ok {
				localData = e.Bytes
			}
			if r, ok := repo[rel]; ok {
				repoData = r
			}
			// File-level status for the settings summary has to
			// honour the same redact/exclude rules the sync
			// engine uses; otherwise the filter-excluded keys
			// (cachedGrowthBookFeatures, autoUpdatesProtected…)
			// register as drift and the summary row sits stuck
			// pending-push even when every user-visible key is
			// synced.
			rule := jsonRules[rel]
			fileStatus := jsonFilteredFileStatus(localData, repoData, excluded[rel], rule, profile, firstSync)
			items = append(items, extractMCPServers(
				localData, repoData, rule, profile, excluded[rel], rel)...)
			summaryData := localData
			if summaryData == nil {
				summaryData = repoData
			}
			if summary := extractSettingsSummary(summaryData, fileStatus, rel); summary != nil {
				items = append(items, *summary)
			}
			continue
		}

		// Any file covered by a jsonFiles rule (e.g.
		// claude/settings.json) gets filter-aware comparison too —
		// user-authored excludes like $.permissions.allow are what
		// the sync engine uses and the inspector should mirror, or
		// the row shows drift the engine won't act on.
		status := statusFor(rel, local, repo, excluded, firstSync)
		if rule, ok := jsonRules[rel]; ok && (local[rel] != nil || repo[rel] != nil) {
			var ld, rd []byte
			if e := local[rel]; e != nil {
				ld = e.Bytes
			}
			rd = repo[rel]
			status = jsonFilteredFileStatus(ld, rd, excluded[rel], rule, profile, firstSync)
		}
		// The bytes we'll extract metadata from: prefer local
		// (most recent authoritative copy from the user's POV);
		// fall back to repo.
		var data []byte
		if e, ok := local[rel]; ok {
			data = e.Bytes
		} else if r, ok := repo[rel]; ok {
			data = r
		}
		items = append(items, itemsFrom(rel, data, status)...)
	}
	return items
}

// canonicalJSON returns a stable byte-form of data so equality
// checks don't flip because of whitespace or key-order noise.
// Unparseable input falls through as-is — the caller treats it
// as "compare raw", which is the conservative choice.
func canonicalJSON(data []byte) []byte {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return data
	}
	out, err := json.Marshal(v)
	if err != nil {
		return data
	}
	return out
}

// jsonFilteredFileStatus computes the status of a JSON-backed file
// after running the configured jsonfilter rule over the local
// bytes. Equivalent to statusFor for any file, but filter-aware:
// redacted secrets and excluded machine-local keys match what the
// sync engine actually pushes, so the row doesn't sit stuck
// pending-push because of legitimate-by-design drift. Returns
// StatusSynced when both sides reduce to the same bytes; the
// usual local/repo presence cases otherwise.
func jsonFilteredFileStatus(localData, repoData []byte, fileExcluded bool, rule config.JSONFileRule, profile string, firstSync bool) Status {
	if fileExcluded {
		return StatusExcluded
	}
	hasLocal := len(localData) > 0
	hasRepo := len(repoData) > 0
	switch {
	case hasLocal && hasRepo:
		effective := effectiveLocalForCompare(localData, rule, profile)
		if sha256.Sum256(canonicalJSON(effective)) == sha256.Sum256(canonicalJSON(repoData)) {
			return StatusSynced
		}
		if firstSync {
			return StatusPendingPull
		}
		return StatusPendingPush
	case hasLocal && !hasRepo:
		return StatusPendingPush
	case !hasLocal && hasRepo:
		return StatusPendingPull
	}
	return StatusSynced
}

// statusFor decides the sync state of one rel-path. firstSync
// (LastSyncedSHA empty for the active profile) flips drift-
// direction to pending pull, matching the sync engine's
// first-sync-takes-remote rule; otherwise drift counts as a local
// edit since the last sync, i.e. pending push.
func statusFor(rel string, local map[string]*localEntry, repo map[string][]byte, excluded map[string]bool, firstSync bool) Status {
	if excluded[rel] {
		return StatusExcluded
	}
	le, hasLocal := local[rel]
	rd, hasRepo := repo[rel]
	switch {
	case hasLocal && hasRepo:
		if le.Sha == sha256.Sum256(rd) {
			return StatusSynced
		}
		if firstSync {
			return StatusPendingPull
		}
		return StatusPendingPush
	case hasLocal && !hasRepo:
		return StatusPendingPush
	case !hasLocal && hasRepo:
		return StatusPendingPull
	}
	return StatusSynced // unreachable
}

// itemsFrom dispatches a single rel-path to the right extractor.
// Claude's .json is handled up in buildItems because its MCP
// servers need per-entry diffing; this function handles the simpler
// one-file-one-item shapes.
func itemsFrom(rel string, data []byte, status Status) []Item {
	kind, cat := kindAndCategory(rel)
	switch kind {
	case KindSkill, KindCommand, KindAgent, KindMemory, KindClaudeMD:
		title, desc := extractMarkdownMeta(data, rel)
		return []Item{{
			Title:       title,
			Description: desc,
			Path:        rel,
			Bytes:       int64(len(data)),
			Kind:        kind,
			Status:      status,
		}}
	default:
		// "Other" — opaque files. Use filename + bytes-size hint.
		title := stemOf(rel)
		desc := humanBytes(int64(len(data)))
		_ = cat // reserved for future label routing
		return []Item{{
			Title:       title,
			Description: desc,
			Path:        rel,
			Bytes:       int64(len(data)),
			Kind:        KindOther,
			Status:      status,
		}}
	}
}

// kindAndCategory maps a repo-relative path to its Kind + the
// internal/category key. The two concepts overlap a lot but not
// perfectly — Memory can live under `claude/memory/` or at the root
// as `CLAUDE.md`, and the inspector's Kind discriminates between
// them for visual rendering.
func kindAndCategory(rel string) (Kind, string) {
	// CLAUDE.md is a top-level file, one of a handful with a
	// distinct kind for display. Everything else routes via
	// category.Classify.
	if strings.EqualFold(filepath.Base(rel), "CLAUDE.md") {
		return KindClaudeMD, category.ClaudeMD
	}
	cat := category.Classify(rel)
	switch cat {
	case category.Skills:
		return KindSkill, cat
	case category.Commands:
		return KindCommand, cat
	case category.Agents:
		return KindAgent, cat
	case category.Memory:
		return KindMemory, cat
	case category.MCPServers:
		return KindMCPServer, cat
	case category.GeneralSettings:
		return KindSettings, cat
	case category.ClaudeMD:
		return KindClaudeMD, cat
	}
	return KindOther, cat
}

// sectionOrder is the fixed render order for the inspector. Most
// user-facing "stuff" first (skills, commands, agents, servers),
// then the ambient / meta categories (memory, CLAUDE.md, settings),
// with "other" as a safety-net tail.
var sectionOrder = []Kind{
	KindSkill,
	KindCommand,
	KindAgent,
	KindMCPServer,
	KindMemory,
	KindClaudeMD,
	KindSettings,
	KindOther,
}

// groupByKind buckets items into Sections in sectionOrder. Empty
// sections are pruned so the screen doesn't render "Skills (0)"
// placeholder rows. Items within each section sort by title
// (case-insensitive) so repeated Inspect calls match.
func groupByKind(items []Item) []Section {
	byKind := map[Kind][]Item{}
	for _, it := range items {
		byKind[it.Kind] = append(byKind[it.Kind], it)
	}
	var out []Section
	for _, k := range sectionOrder {
		list := byKind[k]
		if len(list) == 0 {
			continue
		}
		sort.Slice(list, func(i, j int) bool {
			return strings.ToLower(list[i].Title) < strings.ToLower(list[j].Title)
		})
		out = append(out, Section{
			Kind:  k,
			Label: labelFor(k),
			Items: list,
		})
	}
	return out
}

// labelFor returns the user-facing group heading per Kind. Prefers
// category.Label for categories that map 1:1; uses richer names
// for Kinds that don't have a category entry.
func labelFor(k Kind) string {
	switch k {
	case KindSkill:
		return category.Label(category.Skills)
	case KindCommand:
		return category.Label(category.Commands)
	case KindAgent:
		// "agents" in category.Label → show as "Subagents" in the
		// inspector since that's the public-facing name Claude Code
		// uses.
		return "Subagents"
	case KindMCPServer:
		return category.Label(category.MCPServers)
	case KindMemory:
		return category.Label(category.Memory)
	case KindClaudeMD:
		return "CLAUDE.md"
	case KindSettings:
		return "Settings"
	}
	return "Other"
}

// loadSyncignore mirrors sync.Run's fallback: prefer the repo's
// committed .syncignore when present, otherwise the default from
// the embedded config. Nil-return-safe.
func loadSyncignore(repoPath, defaults string) *ignore.Matcher {
	body := defaults
	if repoPath != "" {
		if data, err := os.ReadFile(filepath.Join(repoPath, ".syncignore")); err == nil {
			body = string(data)
		}
	}
	return ignore.New(body)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// humanBytes renders a byte count as a short human string ("3 KB",
// "1.2 MB"). Used for opaque "other" items where we have nothing
// better to put in the description field.
func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return formatInt(n) + " B"
	case n < 1024*1024:
		return formatInt(n/1024) + " KB"
	default:
		return formatInt(n/(1024*1024)) + " MB"
	}
}

func formatInt(n int64) string {
	// Stdlib-only, no external deps. For sizes in the 0-999 range
	// this is all we need; for larger numbers strconv.FormatInt
	// would also be fine, but strconv feels heavy for one call in
	// a TUI subtitle.
	if n < 0 {
		return "0"
	}
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
