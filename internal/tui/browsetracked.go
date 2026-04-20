package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/discover"
	"github.com/colinc86/ccsync/internal/ignore"
	"github.com/colinc86/ccsync/internal/theme"
	"github.com/colinc86/ccsync/internal/why"
)

// browseTrackedModel is a per-profile file browser. It runs discovery and
// shows every tracked path alongside its effective include/exclude status
// for the active profile. Space toggles an exclude rule on the active
// profile (exact path for agents, glob for skill directories).
type browseTrackedModel struct {
	ctx      *AppContext
	entries  []browseEntry
	filtered []int
	cursor   int
	loading  bool
	err      error
	message  string
	spin     spinner.Model

	filtering bool
	filterIn  textinput.Model

	// syncignore flow — triggered by `i` on a highlighted row.
	ignoring     ignoreStage
	ignoreTarget string // the path the user chose to act on
	patIn        textinput.Model
}

type ignoreStage int

const (
	ignoreOff     ignoreStage = iota
	ignoreChoose              // picking path / parent / pattern
	ignorePattern             // editing the pattern via textinput
)

type browseEntry struct {
	RelPath     string // repo-relative, e.g. "claude/agents/foo.md"
	Size        int64
	Excluded    bool   // effective status for the active profile
	MatchedRule string // the gitignore pattern that caused exclusion, if any
}

type browseLoadedMsg struct {
	entries []browseEntry
	err     error
}

func newBrowseTracked(ctx *AppContext) *browseTrackedModel {
	ti := textinput.New()
	ti.Placeholder = "filter…"
	ti.CharLimit = 48
	ti.Width = 32

	pat := textinput.New()
	pat.CharLimit = 96
	pat.Width = 40

	return &browseTrackedModel{
		ctx:      ctx,
		loading:  true,
		spin:     newSpinner(),
		filterIn: ti,
		patIn:    pat,
	}
}

func (m *browseTrackedModel) Title() string {
	return "Tracked files — profile: " + m.ctx.State.ActiveProfile
}

func (m *browseTrackedModel) Init() tea.Cmd {
	return tea.Batch(loadBrowseEntries(m.ctx), m.spin.Tick)
}

func (m *browseTrackedModel) CapturesEscape() bool {
	return m.filtering || m.ignoring != ignoreOff
}

func loadBrowseEntries(ctx *AppContext) tea.Cmd {
	return func() tea.Msg {
		// syncignore (repo-wide) first
		syncignore := ctx.Config.DefaultSyncignore
		if data, err := os.ReadFile(filepath.Join(ctx.RepoPath, ".syncignore")); err == nil {
			syncignore = string(data)
		}
		matcher := ignore.New(syncignore)

		res, err := discover.Walk(discover.Inputs{
			ClaudeDir:  ctx.ClaudeDir,
			ClaudeJSON: ctx.ClaudeJSON,
		}, matcher)
		if err != nil {
			return browseLoadedMsg{err: err}
		}

		resolved, err := config.EffectiveProfile(ctx.Config, ctx.State.ActiveProfile)
		if err != nil {
			return browseLoadedMsg{err: err}
		}
		var profileMatcher *ignore.Matcher
		if resolved.HasExcludes() {
			profileMatcher = ignore.New(resolved.ExcludeRules())
		}

		entries := make([]browseEntry, 0, len(res.Tracked))
		for _, e := range res.Tracked {
			be := browseEntry{RelPath: e.RelPath, Size: e.Size}
			if profileMatcher != nil && profileMatcher.Matches(e.RelPath) {
				be.Excluded = true
				// Find which rule matched (same approach as why.firstMatch — iterate
				// lines individually so we can report the one that caught this path).
				for _, rule := range resolved.PathExcludes {
					single := ignore.New(rule)
					if single.Matches(e.RelPath) {
						be.MatchedRule = rule
						break
					}
				}
			}
			entries = append(entries, be)
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].RelPath < entries[j].RelPath
		})
		return browseLoadedMsg{entries: entries}
	}
}

func (m *browseTrackedModel) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filterIn.Value()))
	m.filtered = m.filtered[:0]
	for i, e := range m.entries {
		if q == "" || strings.Contains(strings.ToLower(e.RelPath), q) {
			m.filtered = append(m.filtered, i)
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = 0
	}
}

func (m *browseTrackedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case browseLoadedMsg:
		m.loading = false
		m.err = msg.err
		m.entries = msg.entries
		m.applyFilter()
		return m, nil

	case tea.KeyMsg:
		if m.filtering {
			switch msg.String() {
			case "enter", "esc":
				m.filtering = false
				m.filterIn.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.filterIn, cmd = m.filterIn.Update(msg)
			m.applyFilter()
			return m, cmd
		}
		if m.ignoring != ignoreOff {
			return m.updateIgnore(msg)
		}

		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			m.message = ""
		case "down", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
			m.message = ""
		case "/":
			m.filtering = true
			m.filterIn.Focus()
			return m, textinput.Blink
		case "c":
			m.filterIn.SetValue("")
			m.applyFilter()
		case " ":
			return m, m.toggleCursor()
		case "i":
			if len(m.filtered) == 0 {
				return m, nil
			}
			m.ignoring = ignoreChoose
			m.ignoreTarget = m.entries[m.filtered[m.cursor]].RelPath
			m.err = nil
			return m, nil
		case "?":
			if len(m.filtered) == 0 {
				return m, nil
			}
			e := m.entries[m.filtered[m.cursor]]
			target := e.RelPath
			syncignore := m.ctx.Config.DefaultSyncignore
			if data, err := os.ReadFile(filepath.Join(m.ctx.RepoPath, ".syncignore")); err == nil {
				syncignore = string(data)
			}
			tr, werr := why.Explain(why.Inputs{
				Config: m.ctx.Config, Profile: m.ctx.State.ActiveProfile,
				Syncignore: syncignore,
				ClaudeDir:  m.ctx.ClaudeDir, ClaudeJSON: m.ctx.ClaudeJSON,
			}, target)
			if werr != nil {
				m.err = werr
				return m, nil
			}
			m.message = "\n" + why.Format(tr)
		}
	}
	return m, nil
}

// toggleCursor flips the active profile's exclusion rule for the highlighted
// path. For skills (a directory under claude/skills/), we toggle the whole
// directory via a `**` glob — a "skill" is folder-shaped and the user
// usually wants all-or-nothing on it. For everything else, toggle the exact
// path. Rules already in the active profile's Exclude.Paths are removed on
// toggle; otherwise they're appended.
func (m *browseTrackedModel) toggleCursor() tea.Cmd {
	if len(m.filtered) == 0 {
		return nil
	}
	e := m.entries[m.filtered[m.cursor]]
	pat := patternForPath(e.RelPath)

	profName := m.ctx.State.ActiveProfile
	spec, ok := m.ctx.Config.Profiles[profName]
	if !ok {
		m.err = fmt.Errorf("profile %q not found", profName)
		return nil
	}
	if spec.Exclude == nil {
		spec.Exclude = &config.ProfileExclude{}
	}
	paths := spec.Exclude.Paths

	// Did *this* profile have the exact pattern we'd add? If so, remove it.
	removeIdx := -1
	for i, p := range paths {
		if p == pat {
			removeIdx = i
			break
		}
	}

	if removeIdx >= 0 {
		spec.Exclude.Paths = append(paths[:removeIdx], paths[removeIdx+1:]...)
		m.message = fmt.Sprintf("re-included: %s", e.RelPath)
	} else {
		// If excluded by an inherited rule, let the user know an add won't
		// help and suggest editing ccsync.yaml. Re-inclusion via `!pat` is
		// out of scope for this MVP.
		if e.Excluded && e.MatchedRule != "" && e.MatchedRule != pat {
			m.err = fmt.Errorf("excluded by inherited pattern %q; edit ccsync.yaml to change",
				e.MatchedRule)
			return nil
		}
		spec.Exclude.Paths = append(paths, pat)
		m.message = fmt.Sprintf("excluded: %s (rule %q added to %q)", e.RelPath, pat, profName)
	}

	m.ctx.Config.Profiles[profName] = spec
	if err := m.ctx.Config.SaveWithBackup(m.ctx.ConfigPath()); err != nil {
		m.err = err
		return nil
	}
	m.err = nil
	// Reload entries so statuses reflect the new rule.
	return loadBrowseEntries(m.ctx)
}

// updateIgnore drives the "add a rule to .syncignore" flow: a small menu
// (path / parent-dir / pattern) followed by an optional pattern edit.
func (m *browseTrackedModel) updateIgnore(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ignoring == ignorePattern {
		switch msg.String() {
		case "esc":
			m.ignoring = ignoreChoose
			m.patIn.Blur()
			return m, nil
		case "enter":
			pat := strings.TrimSpace(m.patIn.Value())
			if pat == "" {
				m.err = fmt.Errorf("pattern required")
				return m, nil
			}
			return m, m.applyIgnore(pat)
		}
		var cmd tea.Cmd
		m.patIn, cmd = m.patIn.Update(msg)
		return m, cmd
	}

	// ignoreChoose
	switch msg.String() {
	case "esc":
		m.ignoring = ignoreOff
		m.ignoreTarget = ""
		return m, nil
	case "1", "f":
		return m, m.applyIgnore(syncignoreRel(m.ignoreTarget))
	case "2", "d":
		parent := parentSyncignorePattern(m.ignoreTarget)
		if parent == "" {
			m.err = fmt.Errorf("no parent directory to ignore")
			return m, nil
		}
		return m, m.applyIgnore(parent)
	case "3", "p":
		m.ignoring = ignorePattern
		m.patIn.SetValue(defaultExtensionPattern(m.ignoreTarget))
		m.patIn.CursorEnd()
		m.patIn.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

// applyIgnore appends pattern to .syncignore, exits the flow, and reloads
// the file list so the newly-ignored entries drop out.
func (m *browseTrackedModel) applyIgnore(pattern string) tea.Cmd {
	path := filepath.Join(m.ctx.RepoPath, ".syncignore")
	if err := appendSyncignore(path, pattern); err != nil {
		m.err = err
		return nil
	}
	m.err = nil
	m.message = fmt.Sprintf("added to .syncignore: %s", pattern)
	m.ignoring = ignoreOff
	m.ignoreTarget = ""
	m.patIn.Blur()
	return loadBrowseEntries(m.ctx)
}

// syncignoreRel returns the path the way .syncignore expects — patterns are
// written relative to ~/.claude, not the repo tree, so we strip the leading
// "claude/" prefix for files under the Claude directory. "claude.json"
// passes through unchanged (it's not under the claude dir).
func syncignoreRel(rel string) string {
	if after, ok := strings.CutPrefix(rel, "claude/"); ok {
		return after
	}
	return rel
}

// parentSyncignorePattern returns the directory pattern for the row's parent,
// or "" if it has none (top-level file like claude.json).
func parentSyncignorePattern(rel string) string {
	rel = syncignoreRel(rel)
	dir, _ := filepath.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		return ""
	}
	return dir + "/"
}

// defaultExtensionPattern returns a sensible starting pattern for the
// "pattern…" branch — typically "*.ext" when the file has an extension,
// otherwise the file's base name with no extension.
func defaultExtensionPattern(rel string) string {
	base := filepath.Base(rel)
	ext := filepath.Ext(base)
	if ext != "" {
		return "*" + ext
	}
	return base
}

// appendSyncignore writes a rule to .syncignore. Creates the file if missing
// and skips the write entirely if the exact pattern is already present.
func appendSyncignore(path, pattern string) error {
	var existing []byte
	if data, err := os.ReadFile(path); err == nil {
		existing = data
	} else if !os.IsNotExist(err) {
		return err
	}
	// Dedup: bail if the pattern is already on its own line.
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		existing = append(existing, '\n')
	}
	existing = append(existing, []byte(pattern+"\n")...)
	return os.WriteFile(path, existing, 0o644)
}

// patternForPath chooses the ccsync.yaml exclude pattern to use for a path.
// Skill dirs get the whole subtree; everything else gets the exact path.
func patternForPath(rel string) string {
	if strings.HasPrefix(rel, "claude/skills/") {
		parts := strings.Split(rel, "/")
		if len(parts) >= 3 { // claude / skills / <skill>/...
			return strings.Join(parts[:3], "/") + "/**"
		}
	}
	return rel
}

func (m *browseTrackedModel) View() string {
	var sb strings.Builder

	if m.loading {
		return m.spin.View() + " " + theme.Hint.Render("walking local Claude config…")
	}
	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render(m.message) + "\n\n")
	}
	if m.ignoring != ignoreOff {
		return sb.String() + m.renderIgnoreFlow()
	}
	if m.filtering || m.filterIn.Value() != "" {
		sb.WriteString(theme.Secondary.Render("filter: ") + m.filterIn.View())
		fmt.Fprintf(&sb, "  %s\n\n",
			theme.Hint.Render(fmt.Sprintf("(%d/%d)", len(m.filtered), len(m.entries))))
	}

	if len(m.filtered) == 0 {
		if m.filterIn.Value() != "" {
			sb.WriteString(theme.Hint.Render("no matches — press c to clear filter"))
		} else {
			sb.WriteString(theme.Hint.Render("no tracked files"))
		}
		return sb.String()
	}

	excludedCount := 0
	for _, e := range m.entries {
		if e.Excluded {
			excludedCount++
		}
	}
	fmt.Fprintf(&sb, "%s  %s  %s\n\n",
		theme.Secondary.Render(fmt.Sprintf("%d file(s)", len(m.entries))),
		theme.Warn.Render(fmt.Sprintf("%d excluded", excludedCount)),
		theme.Good.Render(fmt.Sprintf("%d syncing", len(m.entries)-excludedCount)),
	)

	start, end := windowAround(m.cursor, len(m.filtered), 20)
	for i := start; i < end; i++ {
		e := m.entries[m.filtered[i]]
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		box := theme.Good.Render("☑")
		line := e.RelPath
		if e.Excluded {
			box = theme.Hint.Render("☐")
			line = theme.Hint.Render(e.RelPath)
			if e.MatchedRule != "" {
				line += theme.Hint.Render(fmt.Sprintf("  — %s", e.MatchedRule))
			}
		}
		fmt.Fprintf(&sb, "%s%s  %s\n", cursor, box, line)
	}

	sb.WriteString("\n" +
		theme.Primary.Render("space ") + "toggle • " +
		theme.Primary.Render("i ") + "syncignore • " +
		theme.Primary.Render("? ") + "why • " +
		theme.Primary.Render("/ ") + "filter • " +
		theme.Hint.Render("↑↓ move • c clear"))
	return sb.String()
}

// renderIgnoreFlow returns the UI for the per-row "add to .syncignore"
// action: a 3-option picker, plus an optional textinput for the pattern.
func (m *browseTrackedModel) renderIgnoreFlow() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("add to .syncignore") + "\n\n")
	fmt.Fprintf(&sb, "  %s  %s\n\n", theme.Secondary.Render("target:"), m.ignoreTarget)

	pathPat := syncignoreRel(m.ignoreTarget)
	parentPat := parentSyncignorePattern(m.ignoreTarget)
	if parentPat == "" {
		parentPat = theme.Hint.Render("(no parent directory)")
	}
	extPat := defaultExtensionPattern(m.ignoreTarget)

	if m.ignoring == ignoreChoose {
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Primary.Render("1"), "this exact path   "+theme.Hint.Render("→ "+pathPat))
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Primary.Render("2"), "parent directory  "+theme.Hint.Render("→ "+parentPat))
		fmt.Fprintf(&sb, "  %s  %s\n", theme.Primary.Render("3"), "pattern…          "+theme.Hint.Render("starts at "+extPat))
		sb.WriteString("\n" + theme.Hint.Render("1/2/3 choose • esc cancel"))
		return sb.String()
	}

	// ignorePattern
	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render("pattern:"), m.patIn.View())
	sb.WriteString("\n" + theme.Hint.Render("enter save • esc back"))
	return sb.String()
}
