// Package profileinspect turns a ccsync profile into a human-facing
// inventory of "things" — skills, commands, agents, MCP servers,
// memory, settings — with titles and descriptions extracted from
// file content rather than shown as raw paths.
//
// The Inspector screen (internal/tui/profileinspect.go) renders the
// View this package produces. Keep the package pure — no TUI, no
// tea.Msg, no lipgloss. Screens consume View + its Sections/Items
// and do their own rendering.
package profileinspect

// Kind discriminates Item shapes for rendering. Each Kind maps to a
// glyph + palette in the screen layer; the package itself is
// display-agnostic but stamps a Kind on every Item so the TUI
// doesn't have to re-derive it.
type Kind int

const (
	KindSkill Kind = iota
	KindCommand
	KindAgent
	KindHook
	KindOutputStyle
	KindMCPServer
	KindMemory
	KindClaudeMD
	KindOther
)

// String returns the short identifier suitable for logs and tests.
// Screens get their visual weight from package theme styles, not
// from these strings, so keep them lower-case and stable.
func (k Kind) String() string {
	switch k {
	case KindSkill:
		return "skill"
	case KindCommand:
		return "command"
	case KindAgent:
		return "agent"
	case KindHook:
		return "hook"
	case KindOutputStyle:
		return "outputStyle"
	case KindMCPServer:
		return "mcp"
	case KindMemory:
		return "memory"
	case KindClaudeMD:
		return "claudeMD"
	}
	return "other"
}

// Status reports the sync state of a single Item. Cross-reference
// between local disk and the repo worktree: an item present on
// both sides with matching content is StatusSynced; one side missing
// (or bytes diverge) lands on one of the pending statuses.
type Status int

const (
	StatusSynced Status = iota
	StatusPendingPush
	StatusPendingPull
	StatusExcluded
)

// String returns the lowercase identifier for this status. Screens
// colour it via theme chips (StatusSynced → ChipGood, etc.) — the
// package returns the raw classifier and leaves styling to the TUI.
func (s Status) String() string {
	switch s {
	case StatusSynced:
		return "synced"
	case StatusPendingPush:
		return "pending push"
	case StatusPendingPull:
		return "pending pull"
	case StatusExcluded:
		return "excluded"
	}
	return ""
}

// Item is one "thing" the user has under their profile. Title is
// what the row leads with; Description is the subtitle. Path is the
// repo-relative path (or synthetic key like `claude.json#gemini`
// for MCP servers) so the TUI can show it in a detail view if the
// user drills in.
type Item struct {
	Title       string
	Description string
	Path        string
	Bytes       int64
	Kind        Kind
	Status      Status
}

// Section groups Items by category. Label is the user-facing name
// ("Skills", "Commands") — derived from internal/category.Label.
// Empty sections are pruned by the caller before View returns them,
// so every Section rendered has at least one Item.
type Section struct {
	Kind  Kind
	Label string
	Items []Item
}

// View is the full inspector output for one profile. Profile is the
// active profile name at Inspect time. Sections are rendered in a
// fixed order — defined by sectionOrder in inspect.go — so repeated
// Inspect calls produce identical layouts.
type View struct {
	Profile  string
	Sections []Section
}

// Empty reports whether there are zero Items across all Sections.
// The screen uses this to decide between a grouped list and the
// "nothing synced yet" hero card.
func (v *View) Empty() bool {
	if v == nil {
		return true
	}
	for _, s := range v.Sections {
		if len(s.Items) > 0 {
			return false
		}
	}
	return true
}
