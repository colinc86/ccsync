// Package tui hosts the Bubble Tea application that drives ccsync end-to-end.
// A top-level Model maintains a stack of screens; each screen is its own
// tea.Model reachable via switchScreen / popScreen messages.
package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
	"github.com/colinc86/ccsync/internal/updater"
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

	// Plan / PlanTime / PlanErr / Fetching cache the result of the most
	// recent background dry-run. Summary() reads from here. Writes happen
	// in AppModel.Update on planRefreshDoneMsg; never touch from inside a
	// screen's Update (the app is the single writer).
	Plan     *sync.Plan
	PlanTime time.Time
	PlanErr  error
	Fetching bool

	// TickGen invalidates stale periodic-refresh ticks. When the user
	// changes the fetch interval we bump this counter, so any in-flight
	// tea.Tick scheduled under the old interval is ignored when it fires
	// (its embedded gen won't match). Prevents duplicate tickers after
	// repeated setting changes.
	TickGen int

	// Update-check cache. Populated by the background checker; read by
	// Settings to show availability inline. LatestVersion is the tag
	// string ("v0.4.0"); UpdateAvailable is true iff it differs from the
	// running binary's version. UpdateInstalling is an in-flight latch so
	// a second fetch tick can't race-dispatch a concurrent install under
	// auto mode (os.Rename over a running binary is safe; two Renames at
	// once aren't).
	LatestVersion    string
	UpdateAvailable  bool
	UpdateCheckedAt  time.Time
	UpdateCheckErr   error
	UpdateInstalling bool
}

// ConfigPath returns the on-disk ccsync.yaml path. Before bootstrap, an
// in-repo path that doesn't exist yet — callers should check for existence.
func (c *AppContext) ConfigPath() string {
	return filepath.Join(c.RepoPath, "ccsync.yaml")
}

// RefreshState re-reads ~/.ccsync/state.json and replaces c.State. Call this
// after any subprocess (sync.Run, sync.RollbackTo) that mutates state on
// disk behind the TUI's back — otherwise c.State.LastSyncedSHA goes stale
// and the Home dashboard / status bar keep reporting the old freshness.
func (c *AppContext) RefreshState() {
	if st, err := state.Load(c.StateDir); err == nil {
		c.State = st
	}
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

	// Apply secrets-backend override from state so TUI flows honor the
	// user's choice even when the env var is unset.
	secrets.SetBackend(string(st.SecretsBackend))

	hostName, _ := os.Hostname()
	gitName, gitEmail := resolveGitAuthor()
	authorName := st.AuthorName
	if authorName == "" {
		if gitName != "" {
			authorName = gitName
		} else {
			authorName = hostName
		}
	}
	authorEmail := st.AuthorEmail
	if authorEmail == "" {
		if gitEmail != "" {
			authorEmail = gitEmail
		} else {
			authorEmail = hostName + "@ccsync.local"
		}
	}
	return &AppContext{
		Config:     cfg,
		State:      st,
		StateDir:   stateDir,
		RepoPath:   repoPath,
		ClaudeDir:  filepath.Join(home, ".claude"),
		ClaudeJSON: filepath.Join(home, ".claude.json"),
		HostName:   authorName,
		Email:      authorEmail,
	}, nil
}

func loadOrDefaultConfig(repoPath string) (*config.Config, error) {
	p := filepath.Join(repoPath, "ccsync.yaml")
	if _, err := os.Stat(p); err == nil {
		return config.Load(p)
	}
	return config.LoadDefault()
}

// resolveGitAuthor reads user.name / user.email from the global git config
// as a source for author identity when state.json has no override. Used
// by NewContext so onboarding can skip the identity step: if the user has
// a reasonable global git config (which anyone who's made a commit
// anywhere has), we don't pester them to set it again for ccsync commits.
//
// Best-effort — any error short-circuits to "" and NewContext falls back
// to hostname-derived defaults. Running `git config` without a git
// install returns exec.ErrNotFound, which we silently swallow.
func resolveGitAuthor() (name, email string) {
	if out, err := exec.Command("git", "config", "--global", "--get", "user.name").Output(); err == nil {
		name = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "config", "--global", "--get", "user.email").Output(); err == nil {
		email = strings.TrimSpace(string(out))
	}
	return
}

type screen interface {
	tea.Model
	Title() string
}

// escapeCapturer is an optional screen capability: when CapturesEscape()
// returns true, the app routes the esc key to the screen's Update instead of
// popping. Screens with modal sub-states (editing a field, confirming a
// destructive action) implement this so esc cancels the modal rather than
// the whole screen.
type escapeCapturer interface {
	CapturesEscape() bool
}

// AppModel is the root Bubble Tea model.
type AppModel struct {
	ctx     *AppContext
	screens []screen
	width   int
	height  int
	help    bool // `?` overlay visible

	// autoWatchCh is the subscription channel delivering debounced file-
	// change events from the filesystem watcher goroutine. Non-nil only
	// when SyncMode == auto and the repo is bootstrapped.
	autoWatchCh <-chan struct{}
}

// New constructs the root model. First-run users (no sync repo and the
// onboarding wizard hasn't been dismissed) land on the onboarding flow
// on top of Home; everyone else lands straight on Home.
func New(ctx *AppContext) AppModel {
	screens := []screen{newHome(ctx)}
	if needsOnboarding(ctx) {
		screens = append(screens, newOnboarding(ctx))
	}
	return AppModel{ctx: ctx, screens: screens}
}

// needsOnboarding returns true when the wizard should be shown at launch.
// Existing users with a bootstrapped repo skip regardless of flag value
// (the flag is new in v0.2 and backfills false) — we detect "new user"
// via SyncRepoURL==""; the flag then stops the nag after the first run
// completes, even on a machine that never completed bootstrap.
func needsOnboarding(ctx *AppContext) bool {
	if ctx == nil || ctx.State == nil {
		return false
	}
	if ctx.State.SyncRepoURL != "" {
		return false
	}
	return !ctx.State.OnboardingComplete
}

// Init satisfies tea.Model. Kicks off the first status refresh, schedules
// the next periodic refresh if opted in, starts the auto-sync watcher
// when SyncMode == auto, and triggers a one-shot update check plus its
// own daily cadence.
func (m AppModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		refreshPlanCmd(m.ctx),
		schedulePeriodicRefresh(m.ctx),
		startAutoWatchCmd(m.ctx),
		checkForUpdateCmd(),
		schedulePeriodicUpdateCheck(),
	}
	if len(m.screens) > 0 {
		cmds = append(cmds, m.screens[0].Init())
	}
	return tea.Batch(nonNilCmds(cmds)...)
}

func nonNilCmds(cmds []tea.Cmd) []tea.Cmd {
	out := cmds[:0]
	for _, c := range cmds {
		if c != nil {
			out = append(out, c)
		}
	}
	return out
}

// Update routes messages: global keys are handled here, everything else is
// delegated to the top screen on the stack.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		// While the help overlay is visible, any key dismisses it — we
		// intentionally don't let the top screen see the keystroke so the
		// user's "press anything to close this" expectation holds.
		if m.help {
			m.help = false
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if len(m.screens) == 1 {
				return m, tea.Quit
			}
		case "?":
			// Capture `?` globally. Screens that want to own `?` for
			// themselves (BrowseTracked uses it for "why") should bind
			// their own handler BEFORE we get here — but AppModel sees
			// keys first, so instead BrowseTracked rebinds to a
			// different key. We accept that global `?` wins.
			m.help = true
			return m, nil
		case "esc":
			// If the top screen captures escape (e.g. Settings while editing
			// a field), let it consume the key instead of popping the stack.
			if len(m.screens) > 0 {
				if cap, ok := m.screens[len(m.screens)-1].(escapeCapturer); ok && cap.CapturesEscape() {
					break
				}
			}
			if len(m.screens) > 1 {
				m.screens = m.screens[:len(m.screens)-1]
				return m, nil
			}
			// On Home with nothing to pop back to, esc quits — matching
			// the footer hint "esc back • ctrl+c quit" doesn't lie anymore.
			return m, tea.Quit
		}
	case switchScreenMsg:
		m.screens = append(m.screens, msg.s)
		return m, msg.s.Init()
	case popScreenMsg:
		if len(m.screens) > 1 {
			m.screens = m.screens[:len(m.screens)-1]
		}
		return m, nil
	case popToRootMsg:
		if len(m.screens) > 1 {
			m.screens = m.screens[:1]
		}
		return m, nil

	case planRefreshStartedMsg:
		m.ctx.Fetching = true
		return m, nil
	case planRefreshDoneMsg:
		m.ctx.Fetching = false
		m.ctx.Plan = msg.plan
		m.ctx.PlanErr = msg.err
		m.ctx.PlanTime = msg.at
		return m, nil
	case periodicRefreshTickMsg:
		// Drop ticks from a superseded interval — the user changed the
		// fetch-interval setting and bumped TickGen, so this tick belongs
		// to a schedule that no longer exists.
		if msg.gen != m.ctx.TickGen {
			return m, nil
		}
		return m, tea.Batch(refreshPlanCmd(m.ctx), schedulePeriodicRefresh(m.ctx))

	case updateCheckDoneMsg:
		m.ctx.LatestVersion = msg.latest
		m.ctx.UpdateCheckedAt = msg.at
		m.ctx.UpdateCheckErr = msg.err
		m.ctx.UpdateAvailable = msg.err == nil &&
			msg.latest != "" &&
			msg.latest != "v"+updater.CurrentVersion()
		// If the user opted into auto-install, silently dispatch it. The
		// returned cmd is nil when conditions don't apply (manual mode,
		// Homebrew install, no newer version), which tea.Batch skips.
		return m, autoInstallIfNeeded(m.ctx)

	case autoInstallDoneMsg:
		// Silent by design — auto mode doesn't notify. On success, the
		// running process keeps its inode of the old binary (Unix inode
		// semantics); the next launch picks up the new version. Clear
		// the pending flag so the Settings row stops showing "available".
		// Either way, release the in-flight latch so a future tick can
		// retry (e.g. if install failed on a permission error that gets
		// cleared later).
		m.ctx.UpdateInstalling = false
		if msg.err == nil {
			m.ctx.UpdateAvailable = false
		}
		return m, nil

	case periodicUpdateCheckTickMsg:
		// Re-run the check and schedule another tick for ~24h from now.
		return m, tea.Batch(checkForUpdateCmd(), schedulePeriodicUpdateCheck())

	case autoWatchStartedMsg:
		// The watcher goroutine is up; stash the channel and start pulling
		// one event at a time via nextAutoWatchEventCmd. Cancel handle is
		// retained for a future shutdown hook; bubbletea has no native
		// "on quit" seam, so for now the goroutine is orphaned at tea.Quit
		// and the OS reaps it when the process exits.
		_ = msg.cancel // intentionally retained but unused; see comment
		m.autoWatchCh = msg.ch
		return m, nextAutoWatchEventCmd(m.autoWatchCh)
	case autoSyncTickMsg:
		// File watcher detected a debounced change. Fire a background
		// sync and keep listening for the next event.
		return m, tea.Batch(
			runAutoSyncCmd(m.ctx),
			nextAutoWatchEventCmd(m.autoWatchCh),
		)
	case autoSyncAppliedMsg:
		// After any auto-sync (success, conflict-bail, or error) refresh
		// the plan so the dashboard reflects current state. Errors are
		// intentionally not surfaced as a pop-up — auto mode is silent
		// by design; the status bar shows "fetch failed" when relevant.
		m.ctx.RefreshState()
		return m, refreshPlanCmd(m.ctx)
	}

	top := m.screens[len(m.screens)-1]
	updated, cmd := top.Update(msg)
	if s, ok := updated.(screen); ok {
		m.screens[len(m.screens)-1] = s
	}
	return m, cmd
}

// View renders the top screen inside a titled card. A persistent status bar
// below the body keeps profile / exclude / host-class context visible on
// every screen. When the user is deep in the screen stack, the header shows
// breadcrumbs (Home > Settings > ccsync.yaml) so they know how to get back,
// and a freshness badge (✓ in sync / ↑N ↓M) sits on the right so the user
// always knows the repo's pulse without navigating back to Home.
func (m AppModel) View() string {
	if len(m.screens) == 0 {
		return ""
	}
	top := m.screens[len(m.screens)-1]
	header := renderHeader(m.screens, m.ctx, m.width)
	status := statusBar(m.ctx)
	footer := theme.Hint.Render(navigationHint(m.screens) + " • ? help")
	body := top.View()
	if m.help {
		body = renderHelpOverlay()
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", status, footer)
}

// renderHeader builds the top line: breadcrumbs on the left, a compact
// freshness badge on the right. When the terminal width isn't known yet
// (WindowSizeMsg hasn't arrived), we fall back to a space-separated
// left-aligned layout — still correct, just not right-justified.
func renderHeader(screens []screen, ctx *AppContext, width int) string {
	crumbs := renderBreadcrumbs(screens)
	if ctx == nil || ctx.State == nil || ctx.State.SyncRepoURL == "" {
		return crumbs
	}
	badge := SummaryBadge(ctx.Summary(), true)
	if badge == "" {
		return crumbs
	}
	if width <= 0 {
		return crumbs + "  " + badge
	}
	gap := max(2, width-lipgloss.Width(crumbs)-lipgloss.Width(badge))
	return crumbs + strings.Repeat(" ", gap) + badge
}

// navigationHint picks the right one-liner for the current stack. On Home
// esc is the only "quit" path (ctrl+c is universal and not worth mentioning);
// deeper in the stack, esc pops back one screen.
func navigationHint(screens []screen) string {
	if len(screens) <= 1 {
		return "esc quit"
	}
	return "esc back"
}

// renderBreadcrumbs returns the header line: each screen's title separated
// by a subtle chevron, with only the leaf rendered in the heading style.
func renderBreadcrumbs(screens []screen) string {
	if len(screens) == 0 {
		return ""
	}
	if len(screens) == 1 {
		return theme.Heading.Render(screens[0].Title())
	}
	var parts []string
	for i, s := range screens {
		if i == len(screens)-1 {
			parts = append(parts, theme.Heading.Render(s.Title()))
		} else {
			parts = append(parts, theme.Subtle.Render(s.Title()))
		}
	}
	return strings.Join(parts, theme.Subtle.Render("  ›  "))
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

// popToRootMsg truncates the screen stack down to just the Home frame.
// Used by terminal "press any key to return" screens whose copy would
// otherwise lie — users in Home → SyncPreview → Sync → done had to press
// esc three times when the footer said "return to home".
type popToRootMsg struct{}

func popToRoot() tea.Cmd {
	return func() tea.Msg { return popToRootMsg{} }
}
