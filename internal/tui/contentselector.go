package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/mcpextract"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

// contentSelectorModel is the per-chunk drill-down screen launched
// from Settings → content. It lists the items inside one content
// chunk (skills, MCP servers from settings.json, …) with a checkbox
// per item; toggling adds or removes a `paths:` entry on the active
// profile's exclude list, persisted via config.SaveWithBackup.
type contentSelectorModel struct {
	ctx       *AppContext
	chunk     string
	heading   string
	items     []contentSelectorItem
	cursor    int
	dirty     bool
	statusMsg string
}

type contentSelectorItem struct {
	// label is the user-facing name (skill name, MCP server key, …).
	label string
	// excludePath is the profile-exclude entry that gates this item.
	// Toggling on flips its presence in the active profile's
	// exclude.paths list.
	excludePath string
	// included is the live "is this synced" state, derived from the
	// profile's effective exclude rules at construction.
	included bool
}

func newContentSelector(ctx *AppContext, chunk string) *contentSelectorModel {
	m := &contentSelectorModel{
		ctx:     ctx,
		chunk:   chunk,
		heading: chunkHeading(chunk),
	}
	m.items = buildContentSelectorItems(ctx, chunk)
	return m
}

func (m *contentSelectorModel) Title() string {
	if m.heading == "" {
		return "Select content"
	}
	return "Select " + m.heading
}

func (m *contentSelectorModel) Init() tea.Cmd { return nil }

func (m *contentSelectorModel) CapturesEscape() bool { return false }

func (m *contentSelectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.cursor = wrapCursor(m.cursor, len(m.items), -1)
		case "down", "j":
			m.cursor = wrapCursor(m.cursor, len(m.items), +1)
		case " ", "x":
			if len(m.items) > 0 {
				m.items[m.cursor].included = !m.items[m.cursor].included
				m.dirty = true
			}
		case "a":
			anyOff := false
			for _, it := range m.items {
				if !it.included {
					anyOff = true
					break
				}
			}
			for i := range m.items {
				m.items[i].included = anyOff
			}
			m.dirty = true
		case "enter":
			if !m.dirty {
				return m, popToRoot()
			}
			if err := m.save(); err != nil {
				m.statusMsg = "save failed: " + err.Error()
				return m, nil
			}
			return m, popToRoot()
		}
	}
	return m, nil
}

func (m *contentSelectorModel) View() string {
	if len(m.items) == 0 {
		return theme.Hint.Render("nothing to select for " + m.heading + " yet — add some on disk and come back")
	}
	var sb strings.Builder
	on := 0
	for _, it := range m.items {
		if it.included {
			on++
		}
	}
	fmt.Fprintf(&sb, "%d of %d included · enter saves · esc cancels\n\n", on, len(m.items))
	start, end := windowAround(m.cursor, len(m.items), 20)
	for i := start; i < end; i++ {
		it := m.items[i]
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		box := theme.Hint.Render("[ ]")
		if it.included {
			box = theme.Good.Render("[x]")
		}
		sb.WriteString(fmt.Sprintf("%s%s %s\n", cursor, box, it.label))
	}
	if m.statusMsg != "" {
		sb.WriteString("\n" + theme.Warn.Render(m.statusMsg) + "\n")
	}
	sb.WriteString("\n" + theme.Hint.Render("space toggle • a toggle all • enter save"))
	return sb.String()
}

// save translates the in-memory inclusion state back into a
// profile.Exclude.paths list and writes the config to disk via
// SaveWithBackup. Excluded paths are the items whose `included` is
// false; enabling an item removes its excludePath if present.
func (m *contentSelectorModel) save() error {
	ctx := m.ctx
	if ctx == nil || ctx.Config == nil {
		return fmt.Errorf("no config")
	}
	profileName := ctx.State.ActiveProfile
	if profileName == "" {
		profileName = "default"
	}
	prof := ctx.Config.Profiles[profileName]
	current := map[string]bool{}
	if prof.Exclude != nil {
		for _, p := range prof.Exclude.Paths {
			current[p] = true
		}
	}
	for _, it := range m.items {
		if it.excludePath == "" {
			continue
		}
		if it.included {
			delete(current, it.excludePath)
		} else {
			current[it.excludePath] = true
		}
	}
	paths := make([]string, 0, len(current))
	for p := range current {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		prof.Exclude = nil
	} else {
		prof.Exclude = &config.ProfileExclude{Paths: paths}
	}
	ctx.Config.Profiles[profileName] = prof
	return ctx.Config.SaveWithBackup(ctx.ConfigPath())
}

// chunkHeading maps a state.ContentChunk* identifier to a human-
// readable label for the screen title and empty-state line.
func chunkHeading(chunk string) string {
	switch chunk {
	case state.ContentChunkAgents:
		return "agents"
	case state.ContentChunkSkills:
		return "skills"
	case state.ContentChunkCommands:
		return "commands"
	case state.ContentChunkHooks:
		return "hooks"
	case state.ContentChunkOutputStyles:
		return "output styles"
	case state.ContentChunkMCPClaudeJSON:
		return "MCP servers (~/.claude.json)"
	case state.ContentChunkMCPSettingsJSON:
		return "MCP servers (~/.claude/settings.json)"
	case state.ContentChunkHooksWiring:
		return "hook wiring"
	}
	return chunk
}

// buildContentSelectorItems gathers the universe of items inside a
// chunk and stamps each with its current include/exclude status. For
// directory chunks we list relative paths under the corresponding
// ~/.claude subdirectory. For JSON-slice chunks we list the keys
// inside the live source file's subtree, surfaced via mcpextract.
func buildContentSelectorItems(ctx *AppContext, chunk string) []contentSelectorItem {
	excluded := profileExcludePathsFor(ctx)
	switch chunk {
	case state.ContentChunkAgents:
		return dirItems(ctx, "agents", excluded)
	case state.ContentChunkSkills:
		return dirItems(ctx, "skills", excluded)
	case state.ContentChunkCommands:
		return dirItems(ctx, "commands", excluded)
	case state.ContentChunkHooks:
		return dirItems(ctx, "hooks", excluded)
	case state.ContentChunkOutputStyles:
		return dirItems(ctx, "output-styles", excluded)
	case state.ContentChunkMCPClaudeJSON,
		state.ContentChunkMCPSettingsJSON,
		state.ContentChunkHooksWiring:
		return sliceItems(ctx, chunk, excluded)
	}
	return nil
}

func dirItems(ctx *AppContext, dirName string, excluded map[string]bool) []contentSelectorItem {
	dirPath := filepath.Join(ctx.ClaudeDir, dirName)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil
	}
	var items []contentSelectorItem
	for _, e := range entries {
		name := e.Name()
		// Hidden files (.DS_Store, …) skip — they're not real items.
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Skill-style "<name>/SKILL.md" → the directory itself is
		// the unit; drop the SKILL.md noise. Command-style flat
		// markdown stays as-is.
		excludePath := "claude/" + dirName + "/" + name
		if e.IsDir() {
			excludePath += "/"
		}
		items = append(items, contentSelectorItem{
			label:       name,
			excludePath: excludePath,
			included:    !excluded[excludePath],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].label) < strings.ToLower(items[j].label)
	})
	return items
}

func sliceItems(ctx *AppContext, chunk string, excluded map[string]bool) []contentSelectorItem {
	var slice mcpextract.Slice
	for _, s := range mcpextract.Slices() {
		if s.ContentChunk == chunk {
			slice = s
			break
		}
	}
	if slice.ManagedPath == "" {
		return nil
	}
	var srcAbs string
	switch slice.SourcePath {
	case ".claude.json":
		srcAbs = ctx.ClaudeJSON
	case ".claude/settings.json":
		srcAbs = filepath.Join(ctx.ClaudeDir, "settings.json")
	}
	srcBytes, _ := os.ReadFile(srcAbs)
	managed, err := mcpextract.Extract(srcBytes, slice.JSONPath)
	if err != nil {
		return nil
	}
	keys, err := mcpextract.ListEntries(managed)
	if err != nil || len(keys) == 0 {
		return nil
	}
	var items []contentSelectorItem
	for _, k := range keys {
		excludePath := slice.ManagedPath + "#" + k
		items = append(items, contentSelectorItem{
			label:       k,
			excludePath: excludePath,
			included:    !excluded[excludePath],
		})
	}
	return items
}

func profileExcludePathsFor(ctx *AppContext) map[string]bool {
	out := map[string]bool{}
	if ctx == nil || ctx.Config == nil {
		return out
	}
	name := ctx.State.ActiveProfile
	if name == "" {
		name = "default"
	}
	prof := ctx.Config.Profiles[name]
	if prof.Exclude == nil {
		return out
	}
	for _, p := range prof.Exclude.Paths {
		out[p] = true
	}
	return out
}
