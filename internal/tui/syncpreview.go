package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

type syncPreviewModel struct {
	ctx     *AppContext
	loading bool
	err     error
	plan    sync.Plan
	spin    spinner.Model

	// visible is the filtered action list the user can cursor through
	// (profile-excluded and no-op actions skipped). Recomputed on load.
	visible []int // indices into plan.Actions
	cursor  int
}

func newSyncPreview(ctx *AppContext) *syncPreviewModel {
	return &syncPreviewModel{ctx: ctx, loading: true, spin: newSpinner()}
}

func (m *syncPreviewModel) Title() string { return "Sync preview (dry run)" }

type previewDoneMsg struct {
	plan sync.Plan
	err  error
}

func (m *syncPreviewModel) Init() tea.Cmd {
	return tea.Batch(runDryRun(m.ctx), m.spin.Tick)
}

func runDryRun(ctx *AppContext) tea.Cmd {
	return func() tea.Msg {
		in, err := buildSyncInputs(ctx, true)
		if err != nil {
			return previewDoneMsg{err: err}
		}
		res, err := sync.Run(context.Background(), in, nil)
		return previewDoneMsg{plan: res.Plan, err: err}
	}
}

// activeProfile returns the currently-active profile name, defaulting
// to "default" when unset. Shared by buildSyncInputs and the review
// screen wiring which both need the name to compose the
// "profiles/<name>/" path prefix.
func activeProfile(ctx *AppContext) string {
	p := ctx.State.ActiveProfile
	if p == "" {
		return "default"
	}
	return p
}

func buildSyncInputs(ctx *AppContext, dryRun bool) (sync.Inputs, error) {
	repoPath := filepath.Join(ctx.StateDir, "repo")
	profile := ctx.State.ActiveProfile
	if profile == "" {
		profile = "default"
	}
	return sync.Inputs{
		Config:      ctx.Config,
		Profile:     profile,
		ClaudeDir:   ctx.ClaudeDir,
		ClaudeJSON:  ctx.ClaudeJSON,
		RepoPath:    repoPath,
		StateDir:    ctx.StateDir,
		HostUUID:    ctx.State.HostUUID,
		HostName:    ctx.HostName,
		AuthorEmail: ctx.Email,
		DryRun:      dryRun,
		Auth:        buildAuth(ctx),
	}, nil
}

func buildAuth(ctx *AppContext) transport.AuthMethod {
	kind := gitx.AuthSSH
	switch ctx.State.Auth {
	case "ssh", "":
		kind = gitx.AuthSSH
	case "https":
		kind = gitx.AuthHTTPS
	}
	cfg := gitx.AuthConfig{
		Kind:       kind,
		SSHKeyPath: ctx.State.SSHKeyPath,
		HTTPSUser:  ctx.State.HTTPSUser,
	}
	if kind == gitx.AuthHTTPS {
		// HTTPS token lives in the secrets backend under a stable key so
		// flipping the backend picks up the same value.
		if tok, err := secrets.Fetch("https-token"); err == nil {
			cfg.HTTPSToken = tok
		}
	}
	a, _ := cfg.Resolve()
	return a
}

func (m *syncPreviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case previewDoneMsg:
		m.loading = false
		m.err = msg.err
		m.plan = msg.plan
		m.recomputeVisible()
		// This screen already paid for a dry-run; cache it so the status bar
		// and Home dashboard use the same plan without a second network hit.
		if m.err == nil {
			m.ctx.Plan = &msg.plan
			m.ctx.PlanTime = time.Now()
			m.ctx.PlanErr = nil
		}
		// Auto-apply gate. In auto mode, the promise is "install, sync,
		// forget" — anything the three-way merge resolves without a
		// conflict should apply without a keypress, including pushes and
		// pulls. The preview still renders briefly before the apply kicks
		// off so the user catches a glimpse of what's happening. In
		// manual mode, keep the stricter AutoApplyClean opt-in (only
		// apply when the plan is entirely empty) so explicit review is
		// still the default. Conflicts never auto-apply either way. If
		// any category's policy is "review", the review screen gates
		// the actual sync regardless of auto/manual.
		if m.err == nil && len(m.plan.Conflicts) == 0 {
			part := sync.PartitionPlan(m.plan, m.ctx.State)
			if len(part.Review) > 0 {
				return m, switchTo(newReviewScreen(m.ctx, part.Review, "profiles/"+activeProfile(m.ctx)+"/"))
			}
			if m.ctx.State.IsAutoMode() ||
				(m.ctx.State.AutoApplyClean && m.planIsClean()) {
				return m, switchTo(newSync(m.ctx))
			}
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if !m.loading && m.err == nil {
				part := sync.PartitionPlan(m.plan, m.ctx.State)
				if len(part.Review) > 0 {
					return m, switchTo(newReviewScreen(m.ctx, part.Review, "profiles/"+activeProfile(m.ctx)+"/"))
				}
				return m, switchTo(newSync(m.ctx))
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.visible)-1 {
				m.cursor++
			}
		case "d":
			if len(m.visible) == 0 {
				return m, nil
			}
			a := m.plan.Actions[m.visible[m.cursor]]
			before, after := diffBytesForAction(m.ctx, a, m.plan.Conflicts)
			return m, switchTo(newDiffView(a.Path, before, after))
		case "s":
			if !m.loading && m.err == nil {
				return m, switchTo(newSelectiveSync(m.ctx, m.plan))
			}
		case "p":
			// Pull-only: apply just the paths coming down from the repo.
			// Implemented via the selective-sync orchestrator (one-shot
			// filter; LastSyncedSHA doesn't advance, so pending pushes
			// remain for next time).
			if !m.loading && m.err == nil {
				only := directionFilter(m.plan, false /*wantPush*/)
				if len(only) == 0 {
					return m, nil
				}
				return m, switchTo(newDirectionalSync(m.ctx, only))
			}
		case "u":
			// Push-only: apply just the paths going up to the repo.
			if !m.loading && m.err == nil {
				only := directionFilter(m.plan, true /*wantPush*/)
				if len(only) == 0 {
					return m, nil
				}
				return m, switchTo(newDirectionalSync(m.ctx, only))
			}
		}
	}
	return m, nil
}

func (m *syncPreviewModel) View() string {
	if m.loading {
		return m.spin.View() + " " + theme.Hint.Render("computing change set…")
	}
	if m.err != nil {
		return theme.Bad.Render("error: ") + m.err.Error()
	}

	var sb strings.Builder
	var push, pull []sync.FileAction
	excluded := 0
	for _, a := range m.plan.Actions {
		if a.ExcludedByProfile {
			excluded++
			continue
		}
		if a.Action == manifest.ActionNoOp {
			continue
		}
		if isPushAction(a.Action) {
			push = append(push, a)
		} else if isPullAction(a.Action) {
			pull = append(pull, a)
		}
		if a.Action == manifest.ActionMerge {
			// Merge acts on both sides; list it in both buckets so the user
			// sees it wherever they're looking. Dedup is cheap — the path is
			// displayed, not counted twice in the summary header.
			pull = append(pull, a)
		}
	}

	// First-sync banner: when we've never synced on this machine, the
	// stakes are higher (push commits become the baseline for the repo;
	// pull introduces a wave of brand-new local files). Loud banner makes
	// sure the user pauses.
	profile := m.ctx.State.ActiveProfile
	if m.ctx.State.LastSyncedSHA[profile] == "" {
		sb.WriteString(theme.Warn.Render(
			"⚑ first sync on this machine — review carefully before applying") + "\n\n")
	}

	fmt.Fprintf(&sb, "%s   %s   %s\n",
		theme.Warn.Render(fmt.Sprintf("↑ %d push", len(push))),
		theme.Warn.Render(fmt.Sprintf("↓ %d pull", len(pull))),
		theme.Bad.Render(fmt.Sprintf("! %d conflict", len(m.plan.Conflicts))),
	)

	// Plain-English recap so a user doesn't have to decode the arrows.
	if summary := naturalLanguageSummary(push, pull, m.plan.Conflicts); summary != "" {
		sb.WriteString(theme.Hint.Render(summary) + "\n")
	}
	sb.WriteString("\n")

	if len(push)+len(pull)+len(m.plan.Conflicts) == 0 {
		sb.WriteString(theme.Good.Render("in sync — nothing to do"))
		if excluded > 0 {
			sb.WriteString("\n" + theme.Hint.Render(
				fmt.Sprintf("(%d path(s) excluded by profile %q — run `ccsync why <path>`)",
					excluded, m.ctx.State.ActiveProfile)))
		}
		return sb.String()
	}

	// Which visible index corresponds to the cursor? Compute once.
	cursorPath := ""
	if len(m.visible) > 0 && m.cursor >= 0 && m.cursor < len(m.visible) {
		cursorPath = m.plan.Actions[m.visible[m.cursor]].Path
	}
	writeGroup := func(label string, actions []sync.FileAction) {
		if len(actions) == 0 {
			return
		}
		sb.WriteString(theme.Secondary.Render(label) + "\n")
		// Bucket by top-level class within the block so 50-file syncs
		// don't render as a wall: "agents (3)", "skills (2)", etc.
		for _, bucket := range bucketActions(actions) {
			fmt.Fprintf(&sb, "  %s %s\n",
				theme.Hint.Render(fmt.Sprintf("%s (%d)", bucket.Label, len(bucket.Actions))), "")
			shown := 0
			for _, a := range bucket.Actions {
				if shown >= 10 {
					fmt.Fprintf(&sb, theme.Hint.Render("      … %d more\n"), len(bucket.Actions)-shown)
					break
				}
				cursor := "    "
				if a.Path == cursorPath {
					cursor = "  " + theme.Primary.Render("▸ ")
				}
				fmt.Fprintf(&sb, "%s%s %s\n", cursor, actionGlyph(a.Action), a.Path)
				shown++
			}
		}
		sb.WriteString("\n")
	}
	writeGroup(fmt.Sprintf("↑ push (%d)", len(push)), push)
	writeGroup(fmt.Sprintf("↓ pull (%d)", len(pull)), pull)

	if len(m.plan.Conflicts) > 0 {
		fmt.Fprintf(&sb, theme.Bad.Render("! conflicts (%d) — will be skipped:\n"), len(m.plan.Conflicts))
		for i, c := range m.plan.Conflicts {
			if i >= 5 {
				fmt.Fprintf(&sb, theme.Hint.Render("  … %d more\n"), len(m.plan.Conflicts)-i)
				break
			}
			sb.WriteString("  ! " + c.Path + "\n")
		}
		sb.WriteString("\n")
	}

	if excluded > 0 {
		sb.WriteString(theme.Hint.Render(
			fmt.Sprintf("%d path(s) excluded by profile %q — run `ccsync why <path>` to see which rule",
				excluded, m.ctx.State.ActiveProfile)) + "\n")
	}

	sb.WriteString("\n" +
		theme.Primary.Render("enter ") + "apply all • " +
		theme.Primary.Render("p ") + "pull only • " +
		theme.Primary.Render("u ") + "push only • " +
		theme.Primary.Render("d ") + "diff • " +
		theme.Primary.Render("s ") + "selective • " +
		theme.Hint.Render("↑↓ move • esc cancel"))
	return sb.String()
}

// planIsClean reports whether the plan has real work but no conflicts and
// no placeholder-restore concerns. Profile-excluded paths don't count as
// conflicts. An empty plan (nothing to do) is also "clean" — no-op apply.
func (m *syncPreviewModel) planIsClean() bool {
	return len(m.plan.Conflicts) == 0
}

// recomputeVisible rebuilds m.visible to hold indices of actions that are
// worth showing the user — actual work, not no-ops or profile-excluded.
func (m *syncPreviewModel) recomputeVisible() {
	m.visible = m.visible[:0]
	for i, a := range m.plan.Actions {
		if a.ExcludedByProfile {
			continue
		}
		if a.Action == manifest.ActionNoOp {
			continue
		}
		m.visible = append(m.visible, i)
	}
	if m.cursor >= len(m.visible) {
		m.cursor = 0
	}
}

// diffBytesForAction returns the (before, after) byte pair to show in the
// diff viewer for a given action. The orientation follows what's about to
// happen: "before" is the side that will be replaced, "after" is the side
// that will win. Conflict rows get their bytes from the plan's conflict
// record; everything else reads from disk lazily.
func diffBytesForAction(ctx *AppContext, a sync.FileAction, conflicts []sync.FileConflict) (before, after []byte) {
	for _, fc := range conflicts {
		if fc.Path == a.Path {
			return fc.LocalData, fc.RemoteData
		}
	}
	localAbs := a.LocalAbs
	repoAbs := filepath.Join(ctx.RepoPath, a.Path)

	// Read each side best-effort. Missing files map to nil, which
	// renderUnifiedDiff handles (shows "no changes" or an add/delete).
	localData, _ := safeReadFile(localAbs)
	repoData, _ := safeReadFile(repoAbs)

	switch a.Action {
	case manifest.ActionAddRemote, manifest.ActionPush, manifest.ActionDeleteRemote:
		// Moving local → repo. Before = repo (old), after = local (new).
		return repoData, localData
	case manifest.ActionAddLocal, manifest.ActionPull, manifest.ActionDeleteLocal:
		// Moving repo → local. Before = local (old), after = repo (new).
		return localData, repoData
	case manifest.ActionMerge:
		// Ambiguous; show the merge-product by convention: local vs remote
		// in that order so "+" lines are what's in the repo.
		return localData, repoData
	}
	return localData, repoData
}

// safeReadFile reads a file, returning nil bytes (not an error) when the
// path is empty or the file is absent — appropriate for diff inputs where
// "file not present" is a valid state.
func safeReadFile(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// naturalLanguageSummary builds a short human recap of what the plan will
// do. Returns "" for an empty plan so callers don't render a blank row.
//
// Examples:
//   "This sync will pull 5 files from the repo to ~/.claude."
//   "This sync will push 3 local files up to the repo and pull 2 down."
//   "This sync has 1 conflict — resolve before applying."
func naturalLanguageSummary(push, pull []sync.FileAction, conflicts []sync.FileConflict) string {
	var parts []string
	if n := len(push); n > 0 {
		parts = append(parts, fmt.Sprintf("push %d local file%s up to the repo",
			n, plural(n)))
	}
	if n := len(pull); n > 0 {
		parts = append(parts, fmt.Sprintf("pull %d file%s down into ~/.claude",
			n, plural(n)))
	}
	if len(parts) == 0 && len(conflicts) == 0 {
		return ""
	}
	var out string
	switch len(parts) {
	case 0:
		// only conflicts — handled below
	case 1:
		out = "This sync will " + parts[0] + "."
	case 2:
		out = "This sync will " + parts[0] + " and " + parts[1] + "."
	}
	if n := len(conflicts); n > 0 {
		c := fmt.Sprintf(" %d conflict%s need%s manual resolution.", n, plural(n), oneIfSingular(n))
		out += c
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// oneIfSingular returns "" for a singular subject ("1 conflict needs…") and
// "s" for plural ("2 conflicts need…") — matches subject-verb agreement for
// the specific pattern in naturalLanguageSummary.
func oneIfSingular(n int) string {
	if n == 1 {
		return "s"
	}
	return ""
}

// directionFilter returns the set of repo paths on one side of the sync:
// wantPush=true → only paths being sent up; wantPush=false → only paths
// coming down. Profile-excluded and no-op actions are skipped.
func directionFilter(plan sync.Plan, wantPush bool) map[string]bool {
	out := map[string]bool{}
	for _, a := range plan.Actions {
		if a.ExcludedByProfile || a.Action == manifest.ActionNoOp {
			continue
		}
		if wantPush && isPushAction(a.Action) {
			out[a.Path] = true
		}
		if !wantPush && isPullAction(a.Action) {
			out[a.Path] = true
		}
		if a.Action == manifest.ActionMerge {
			// Merges touch both sides; include in whichever direction.
			out[a.Path] = true
		}
	}
	return out
}

// newDirectionalSync kicks off a selective sync with a pre-built filter.
// The user doesn't see the selective-sync toggle screen — they've already
// chosen the direction in SyncPreview, so we go straight to the running
// Sync screen.
func newDirectionalSync(ctx *AppContext, only map[string]bool) *syncModel {
	m := newSync(ctx)
	m.onlyPaths = only
	return m
}

// actionBucket groups a set of actions under a human label (agents,
// skills, etc.) for the SyncPreview view. Order preserved from the
// declaration below so rendering is deterministic.
type actionBucket struct {
	Label   string
	Actions []sync.FileAction
}

// bucketActions groups actions by top-level class. Declaration order is
// the render order so "CLAUDE.md" shows up before "agents" etc. Classes
// align with the semantic categories the commit-message extractor uses.
func bucketActions(actions []sync.FileAction) []actionBucket {
	const (
		bClaudeMd   = "CLAUDE.md"
		bClaudeJSON = "claude.json"
		bAgents     = "agents"
		bSkills     = "skills"
		bCommands   = "commands"
		bOther      = "other"
	)
	order := []string{bClaudeMd, bClaudeJSON, bAgents, bSkills, bCommands, bOther}
	groups := map[string][]sync.FileAction{}
	for _, a := range actions {
		key := classifyPath(a.Path)
		groups[key] = append(groups[key], a)
	}
	var out []actionBucket
	for _, k := range order {
		if len(groups[k]) > 0 {
			out = append(out, actionBucket{Label: k, Actions: groups[k]})
		}
	}
	return out
}

// classifyPath picks the bucket label for a repo-relative path.
func classifyPath(repoPath string) string {
	// Strip the "profiles/<name>/" prefix to work with plain rels.
	rel := repoPath
	if strings.HasPrefix(rel, "profiles/") {
		rest := strings.TrimPrefix(rel, "profiles/")
		if i := strings.Index(rest, "/"); i >= 0 {
			rel = rest[i+1:]
		}
	}
	switch {
	case rel == "CLAUDE.md":
		return "CLAUDE.md"
	case rel == "claude.json":
		return "claude.json"
	case strings.HasPrefix(rel, "claude/agents/"):
		return "agents"
	case strings.HasPrefix(rel, "claude/skills/"):
		return "skills"
	case strings.HasPrefix(rel, "claude/commands/"):
		return "commands"
	}
	return "other"
}

// isPushAction reports whether this action moves data repo-ward.
func isPushAction(a manifest.Action) bool {
	switch a {
	case manifest.ActionAddRemote, manifest.ActionPush, manifest.ActionDeleteRemote:
		return true
	}
	return false
}

// isPullAction reports whether this action moves data local-ward.
func isPullAction(a manifest.Action) bool {
	switch a {
	case manifest.ActionAddLocal, manifest.ActionPull, manifest.ActionDeleteLocal:
		return true
	}
	return false
}

func actionGlyph(a manifest.Action) string {
	switch a {
	case manifest.ActionAddLocal, manifest.ActionAddRemote:
		return theme.Good.Render("+")
	case manifest.ActionDeleteLocal, manifest.ActionDeleteRemote:
		return theme.Bad.Render("-")
	case manifest.ActionPull, manifest.ActionPush, manifest.ActionMerge:
		return theme.Warn.Render("~")
	case manifest.ActionConflict:
		return theme.Bad.Render("!")
	}
	return "·"
}
