package profileinspect

import (
	"crypto/sha256"
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

	// 3. Walk repo side (every profile in the chain).
	repoByRel := walkRepoChain(in.RepoPath, resolved.Chain)

	// 4. Combine into Items. Each unique rel-path (relative to the
	//    active profile prefix) produces zero-or-more Items — mcp
	//    servers expand to one Item per server under the
	//    claude.json's mcpServers key.
	items := buildItems(localByRel, repoByRel, excluded)

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
func walkRepoChain(repoPath string, chain []string) map[string][]byte {
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
			if d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			out[filepath.ToSlash(rel)] = data
			return nil
		})
	}
	return out
}

// buildItems produces the flat Item list for the caller to group.
// Iterates every rel path known to either side, dispatches to the
// right extractor by category.Classify + file shape, and assigns a
// Status from a local-vs-repo cross-reference.
func buildItems(local map[string]*localEntry, repo map[string][]byte, excluded map[string]bool) []Item {
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
		status := statusFor(rel, local, repo, excluded)
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

// statusFor decides the sync state of one rel-path.
func statusFor(rel string, local map[string]*localEntry, repo map[string][]byte, excluded map[string]bool) Status {
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
		// Bytes differ — technically push-or-pull requires baseline,
		// but for an inventory inspector "there's drift" reads as
		// pending push from the local POV (the user's machine has
		// a newer version than the repo remembers). If it turns
		// out to be an incoming pull, the real sync plan will
		// correct the framing.
		return StatusPendingPush
	case hasLocal && !hasRepo:
		return StatusPendingPush
	case !hasLocal && hasRepo:
		return StatusPendingPull
	}
	return StatusSynced // unreachable
}

// itemsFrom dispatches a single rel-path to the right extractor.
// One rel may produce multiple Items (mcpServers splits a
// claude.json into N server Items + 1 settings Item). Everything
// else produces exactly one Item.
func itemsFrom(rel string, data []byte, status Status) []Item {
	switch rel {
	case "claude.json":
		out := extractMCPServers(data, status, rel)
		if summary := extractSettingsSummary(data, status, rel); summary != nil {
			out = append(out, *summary)
		}
		return out
	}

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
