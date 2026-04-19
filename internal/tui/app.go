// Package tui hosts the Bubble Tea application that drives ccsync end-to-end.
// A top-level Model maintains a stack of screens; each screen is its own
// tea.Model reachable via switchScreen / popScreen messages.
package tui

import (
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
)

// AppContext bundles shared state passed to every screen.
type AppContext struct {
	Config     *config.Config
	State      *state.State
	StateDir   string
	RepoPath   string
	ClaudeDir  string
	ClaudeJSON string
	HostName   string
	Email      string
}

// ConfigPath returns the on-disk ccsync.yaml path. Before bootstrap, an
// in-repo path that doesn't exist yet — callers should check for existence.
func (c *AppContext) ConfigPath() string {
	return filepath.Join(c.RepoPath, "ccsync.yaml")
}

// NewContext resolves paths and loads config + state from disk. Fresh
// installs return a zero-valued State (no sync repo yet).
func NewContext() (*AppContext, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	stateDir := filepath.Join(home, ".ccsync")
	st, err := state.Load(stateDir)
	if err != nil {
		return nil, err
	}
	st.EnsureHostUUID()

	repoPath := filepath.Join(stateDir, "repo")
	cfg, err := loadOrDefaultConfig(repoPath)
	if err != nil {
		return nil, err
	}

	hostName, _ := os.Hostname()
	return &AppContext{
		Config:     cfg,
		State:      st,
		StateDir:   stateDir,
		RepoPath:   repoPath,
		ClaudeDir:  filepath.Join(home, ".claude"),
		ClaudeJSON: filepath.Join(home, ".claude.json"),
		HostName:   hostName,
		Email:      hostName + "@ccsync.local",
	}, nil
}

func loadOrDefaultConfig(repoPath string) (*config.Config, error) {
	p := filepath.Join(repoPath, "ccsync.yaml")
	if _, err := os.Stat(p); err == nil {
		return config.Load(p)
	}
	return config.LoadDefault()
}

type screen interface {
	tea.Model
	Title() string
}

// AppModel is the root Bubble Tea model.
type AppModel struct {
	ctx     *AppContext
	screens []screen
	width   int
	height  int
}

// New constructs the root model pre-populated with the Home screen.
func New(ctx *AppContext) AppModel {
	return AppModel{
		ctx:     ctx,
		screens: []screen{newHome(ctx)},
	}
}

// Init satisfies tea.Model.
func (m AppModel) Init() tea.Cmd {
	if len(m.screens) == 0 {
		return nil
	}
	return m.screens[0].Init()
}

// Update routes messages: global keys are handled here, everything else is
// delegated to the top screen on the stack.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if len(m.screens) == 1 {
				return m, tea.Quit
			}
		case "esc":
			if len(m.screens) > 1 {
				m.screens = m.screens[:len(m.screens)-1]
				return m, nil
			}
		}
	case switchScreenMsg:
		m.screens = append(m.screens, msg.s)
		return m, msg.s.Init()
	case popScreenMsg:
		if len(m.screens) > 1 {
			m.screens = m.screens[:len(m.screens)-1]
		}
		return m, nil
	}

	top := m.screens[len(m.screens)-1]
	updated, cmd := top.Update(msg)
	if s, ok := updated.(screen); ok {
		m.screens[len(m.screens)-1] = s
	}
	return m, cmd
}

// View renders the top screen inside a titled card.
func (m AppModel) View() string {
	if len(m.screens) == 0 {
		return ""
	}
	top := m.screens[len(m.screens)-1]
	header := theme.Heading.Render(top.Title())
	footer := theme.Hint.Render("esc back • ctrl+c quit")
	body := top.View()
	return lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", footer)
}

// switchScreenMsg pushes a new screen on top of the stack.
type switchScreenMsg struct{ s screen }

func switchTo(s screen) tea.Cmd {
	return func() tea.Msg { return switchScreenMsg{s: s} }
}

// popScreenMsg pops the top screen.
type popScreenMsg struct{}

func popScreen() tea.Cmd {
	return func() tea.Msg { return popScreenMsg{} }
}
