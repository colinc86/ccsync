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

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/gitx"
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

	// AutoSyncedOnLaunch latches after the first launch-driven auto-sync
	// has fired so subsequent plan refreshes (the periodic background
	// tick, the post-sync refresh) don't cascade into another sync. The
	// file-watcher path is unaffected; it triggers on actual filesystem
	// changes, not plan deltas.
	AutoSyncedOnLaunch bool
	// AutoSyncing is a latch used by the plan-refresh → auto-sync
	// handoff to avoid racing two real syncs when the refresh finishes
	// while an earlier auto-sync is still in flight.
	AutoSyncing bool

	// PendingProfileChoice latches after a successful bootstrap
	// until the user has picked or created a profile. Gates the
	// auto-sync launcher: on a fresh install in auto mode, the
	// first plan refresh would otherwise fire a background sync
	// under the hardcoded "default" profile BEFORE the user
	// chose theirs, landing their local ~/.claude content as a
	// commit under the wrong profile. The gate clears when
	// profile picker's finalize runs, after which auto-sync
	// resumes normally.
	PendingProfileChoice bool

	// RestartBinaryPath, when non-empty after the TUI exits, signals
	// main() to syscall.Exec the named binary in place of the current
	// process. Set by the Update screen after a successful self-
	// install so the user lands on the freshly-written binary without
	// manually relaunching. main() resolves the exec argv + env from
	// the current process, so user-visible flags survive the swap.
	RestartBinaryPath string

	// TermWidth / TermHeight reflect the most recent tea.WindowSizeMsg
	// the AppModel saw. Stashed on the shared context so screens born
	// mid-session (e.g. Settings, opened from Home long after the
	// initial WindowSizeMsg fires) can size their viewports without
	// waiting for a resize. Updated by AppModel.Update on every
	// WindowSizeMsg; screens read but never write.
	TermWidth  int
	TermHeight int
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

// renderError is the TUI-wide convention for showing an error in a
// screen's View. Routes through gitx.Friendly so known sentinels
// (auth required, repo not found, non-fast-forward, network) collapse
// to one-liners without the go-git chain trailing after. Returns ""
// for nil so callers can unconditionally append its result.
//
// Rendered as a red-bordered card so errors feel like an event the
// user needs to acknowledge, not a line of prose they might miss in
// a busy screen. The `!` glyph + "ERROR" caps title mirror every
// other hero-card state (clean/pending/conflict) for visual rhythm.
func renderError(err error) string {
	if err == nil {
		return ""
	}
	body := theme.Bad.Render("! ERROR") + "\n" +
		theme.Subtle.Render(gitx.Friendly(err))
	return theme.CardConflict.Width(56).Render(body)
}

// escapeCapturer is an optional screen capability: when CapturesEscape()
// returns true, the app routes the esc key to the screen's Update instead of
// popping. Screens with modal sub-states (editing a field, confirming a
// destructive action) implement this so esc cancels the modal rather than
// the whole screen.
type escapeCapturer interface {
	CapturesEscape() bool
}

// terminalScreen is an optional capability for screens whose "back one
// step" destination is a now-invalid view — a consumed dry-run plan, a
// post-sync preview that no longer reflects disk state, a completed
// wizard. When IsTerminal() returns true, ESC pops the entire stack
// back to Home instead of popping a single layer. Screens return true
// only when their work is complete; intermediate states still fall
// through to the default pop-one behaviour.
type terminalScreen interface {
	IsTerminal() bool
}

// AppModel is the root Bubble Tea model.
type AppModel struct {
	ctx     *AppContext
	screens []screen
	width   int
	height  int
	help    bool // `?` overlay visible

	// toast is the current transient notice overlay, nil when absent.
	// toastSeq bumps on every showToastMsg so stale expire ticks (from
	// a toast that got replaced by a newer one) don't cut the
	// replacement short.
	toast    *toastPayload
	toastSeq int

	// palette is the Ctrl+K command-palette overlay. Nil when
	// closed. Owns its own Update while visible so the underlying
	// screen doesn't see palette keystrokes (users typing into the
	// palette would otherwise trigger shortcuts on whatever screen
	// they had open).
	palette *paletteModel

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
		paletteTipOnceCmd(m.ctx),
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
		// Mirror onto the shared context so screens created later in
		// the session (after this initial size message has already
		// passed through) can still size their viewports correctly.
		m.ctx.TermWidth = msg.Width
		m.ctx.TermHeight = msg.Height
	case tea.KeyMsg:
		// While the help overlay is visible, any key dismisses it — we
		// intentionally don't let the top screen see the keystroke so the
		// user's "press anything to close this" expectation holds.
		if m.help {
			m.help = false
			return m, nil
		}
		// Command palette captures all input while visible. It owns
		// its own navigation + closes via paletteClosedMsg (handled
		// below), so route every key straight through and stop.
		if m.palette != nil {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			p, cmd := m.palette.Update(msg)
			m.palette = p
			return m, cmd
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+k":
			// Open the command palette. ctrl+k mirrors the VS-Code /
			// Linear convention; on macOS this doesn't conflict with
			// any system shortcut, and on the Linux side bubbletea
			// passes it through cleanly.
			m.palette = newPalette(m.ctx)
			return m, textinput.Blink
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
			// Three-way fan-out:
			//   1. Screen owns esc (modal sub-state) — pass through so the
			//      screen's Update can cancel/back within itself.
			//   2. Screen is terminal (work complete, prior stack is
			//      stale) — pop all the way to Home so "back one step"
			//      doesn't land on a dead dry-run view.
			//   3. Default — pop a single screen.
			if len(m.screens) > 0 {
				top := m.screens[len(m.screens)-1]
				if cap, ok := top.(escapeCapturer); ok && cap.CapturesEscape() {
					break
				}
				if term, ok := top.(terminalScreen); ok && term.IsTerminal() && len(m.screens) > 1 {
					m.screens = m.screens[:1]
					return m, nil
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
		// First plan refresh under auto mode with pending actions and no
		// conflicts: trigger the real sync silently. The file-watcher
		// already covers live changes while the TUI is open — this is
		// the symmetric path for "change happened before launch." Latched
		// by AutoSyncedOnLaunch so periodic refreshes don't re-trigger.
		if cmd := maybeLaunchAutoSync(m.ctx); cmd != nil {
			return m, cmd
		}
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
		// sync and keep listening for the next event. Coalesce into any
		// already-running auto-sync — the new event will be picked up on
		// the next tick once the in-flight one returns.
		if m.ctx.AutoSyncing {
			return m, nextAutoWatchEventCmd(m.autoWatchCh)
		}
		m.ctx.AutoSyncing = true
		return m, tea.Batch(
			runAutoSyncCmd(m.ctx),
			nextAutoWatchEventCmd(m.autoWatchCh),
		)
	case autoSyncAppliedMsg:
		// After any auto-sync (success, conflict-bail, or error) refresh
		// the plan so the dashboard reflects current state. Errors are
		// intentionally not surfaced as a pop-up — auto mode is silent
		// by design; the status bar shows "fetch failed" when relevant.
		m.ctx.AutoSyncing = false
		m.ctx.RefreshState()
		return m, refreshPlanCmd(m.ctx)

	case paletteClosedMsg:
		// The command palette is dismissed — clear the overlay so the
		// underlying screen starts receiving keystrokes again.
		m.palette = nil
		return m, nil
	case paletteOpenHelpMsg:
		// The palette asked to open the help overlay on its way out.
		// Flip the help flag; the palette has already been cleared by
		// the paletteClosedMsg that batched alongside this.
		m.help = true
		return m, nil

	case showToastMsg:
		// A screen has asked to surface a transient notice. Bump the
		// sequence so any in-flight expire tick for the previous
		// toast is invalidated; schedule a fresh expire for this one.
		m.toastSeq++
		m.toast = &toastPayload{id: m.toastSeq, kind: msg.kind, text: msg.text}
		return m, scheduleToastExpire(m.toastSeq)
	case toastExpireMsg:
		// Only clear when the expiring tick matches the current
		// toast's id. A newer toast arriving during this Tick's
		// window will have bumped toastSeq; the stale tick lands
		// here and is a no-op.
		if m.toast != nil && m.toast.id == msg.id {
			m.toast = nil
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
	rule := renderHeaderRule(m.width)
	status := statusBar(m.ctx)
	footer := theme.Hint.Render(navigationHint(m.screens) + " • ctrl+k palette • ? help")
	body := top.View()
	if m.help {
		body = renderHelpOverlay()
	}
	// Command palette takes precedence over the help overlay (same
	// screen-estate slot). When it's open, the shell shows the
	// palette instead of the screen's body so input focus is
	// visually obvious — the user is driving the palette, not the
	// screen underneath.
	if m.palette != nil {
		body = renderPalette(m.palette)
	}

	// Toast slot — right-justified strip above the status bar. Only
	// reserved when a toast is active so there's no empty gap
	// bouncing the layout around between "no toast" and "toast
	// present" frames. The toast is rendered as a bordered pill;
	// PlaceHorizontal slides it to the right edge with the body's
	// width for anchor.
	var toastLine string
	if m.toast != nil {
		t := renderToast(m.toast)
		if m.width > 0 {
			toastLine = lipgloss.PlaceHorizontal(m.width, lipgloss.Right, t)
		} else {
			toastLine = t
		}
	}

	pieces := []string{header, rule, "", body}
	if toastLine != "" {
		pieces = append(pieces, "", toastLine)
	}
	pieces = append(pieces, "", status, footer)
	return lipgloss.JoinVertical(lipgloss.Left, pieces...)
}

// renderHeader builds the app-shell top strip: a tiny accented ccsync
// logo on the far left, the breadcrumb trail, and — right-justified —
// a profile chip + the compact freshness badge. When the terminal
// width isn't known yet, we fall back to a left-aligned layout
// (still correct, just not right-justified).
//
// The strip is deliberately thin: screens own the visual weight via
// their own hero cards. The header is a navigation/context rail,
// not a page title.
func renderHeader(screens []screen, ctx *AppContext, width int) string {
	logo := theme.WordmarkStyle.Render("ccsync")
	crumbs := renderBreadcrumbs(screens)
	left := logo
	if crumbs != "" {
		left = logo + theme.Subtle.Render("  ›  ") + crumbs
	}

	right := renderHeaderRight(ctx)
	if right == "" {
		return left
	}
	if width <= 0 {
		return left + "  " + right
	}
	gap := max(2, width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

// renderHeaderRight builds the right-hand cluster: a profile chip
// (visible once bootstrapped) followed by the compact sync badge.
// Returns empty when we have nothing to show (pre-bootstrap / no
// summary available).
func renderHeaderRight(ctx *AppContext) string {
	if ctx == nil || ctx.State == nil || ctx.State.SyncRepoURL == "" {
		return ""
	}
	badge := SummaryBadge(ctx.Summary(), true)
	profile := ctx.State.ActiveProfile
	if profile == "" {
		return badge
	}
	chip := theme.ChipNeutral.Render("◉ " + profile)
	if badge == "" {
		return chip
	}
	return chip + theme.Subtle.Render("  ·  ") + badge
}

// renderHeaderRule draws a one-char-tall divider right under the
// header. A thin row of light accent glyphs sits between the nav
// strip and the body, giving the app a consistent "this is the top"
// anchor regardless of which screen is up. Falls back to an em-dash
// rule when width is unknown (initial frame before WindowSizeMsg).
func renderHeaderRule(width int) string {
	if width <= 0 {
		return theme.Rule.Render(strings.Repeat("─", 40))
	}
	return theme.Rule.Render(strings.Repeat("─", width))
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

// renderBreadcrumbs returns the breadcrumb trail for the header. The
// leaf is rendered in the secondary accent (not the loud Heading
// underline — that'd compete with the screen's own page heading);
// non-leaf crumbs are muted. Chevron separator is subtle so the path
// reads as one fluid element.
func renderBreadcrumbs(screens []screen) string {
	if len(screens) == 0 {
		return ""
	}
	if len(screens) == 1 {
		return theme.Secondary.Render(screens[0].Title())
	}
	var parts []string
	for i, s := range screens {
		if i == len(screens)-1 {
			parts = append(parts, theme.Secondary.Render(s.Title()))
		} else {
			parts = append(parts, theme.Subtle.Render(s.Title()))
		}
	}
	return strings.Join(parts, theme.Subtle.Render("  ›  "))
}

// switchScreenMsg pushes a new screen on top of the stack.
// wrapCursor shifts cur by delta and wraps it inside [0, n). Empty
// lists (n <= 0) return the cursor unchanged so callers don't have
// to guard against a mod-by-zero. Used by every list-backed screen
// so up-at-top lands on the last row and down-at-bottom lands on
// the first, matching the "feel" users expect from every other
// terminal list.
func wrapCursor(cur, n, delta int) int {
	if n <= 0 {
		return cur
	}
	return ((cur+delta)%n + n) % n
}

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
