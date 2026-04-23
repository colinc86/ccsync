package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
)

// TestHomeDashboardIncludesWordmarkAndFooterBar pins the iter-1 UI
// refresh: Home always leads with the ccsync wordmark and ends with
// a keycap-styled footer bar. A screen that renders as a bare
// paragraph of status text loses the "that's ccsync" identity beat
// the user sees on every launch; a screen that drops the footer
// hides the primary keyboard verb. This test is deliberately shape-
// only — the exact copy can evolve, but the presence of these two
// structural elements is load-bearing.
func TestHomeDashboardIncludesWordmarkAndFooterBar(t *testing.T) {
	ctx := &AppContext{
		State: &state.State{
			SyncRepoURL:   "git@example.com:x/y.git",
			ActiveProfile: "default",
			LastSyncedSHA: map[string]string{"default": "abcdef1"},
			LastSyncedAt:  map[string]time.Time{"default": time.Now().Add(-5 * time.Minute)},
		},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		HostName: "test-host",
		StateDir: t.TempDir(),
	}
	h := newHome(ctx)
	out := h.renderDashboard()

	if !strings.Contains(out, "ccsync") {
		t.Error("Home should contain the ccsync wordmark")
	}
	// The footer bar renders a primary keycap for "enter" and muted
	// keycaps for the secondaries. Presence check — "enter" + "more"
	// + "help" must all appear.
	for _, kw := range []string{"enter", "more", "help", "quit"} {
		if !strings.Contains(out, kw) {
			t.Errorf("footer bar missing %q", kw)
		}
	}
	// Profile chip glyph (◉) should be rendered for a bootstrapped
	// state — it's the visual beat that says "here's your profile".
	if !strings.Contains(out, "◉") {
		t.Error("Home should render the profile chip glyph")
	}
}

// TestSyncStageTreeMarksActiveAndCompleted pins the iter-2 progress
// rendering: events up through "snapshot" should render "fetch" and
// "discover" as ✓ (completed), "snapshot" as the spinner frame
// (active), and every later stage as muted ◦ (upcoming). Replaces
// the pre-iter-2 scrolling-log view that looked identical every
// frame.
func TestSyncStageTreeMarksActiveAndCompleted(t *testing.T) {
	events := []sync.Event{
		{Stage: "fetch", Message: "fetching remote"},
		{Stage: "discover", Message: "walking local"},
		{Stage: "snapshot", Message: "snapshotting"},
	}
	out := renderStageTree(events, "⠋", false)

	// Completed stages emit the check glyph.
	if !strings.Contains(out, "✓") {
		t.Error("expected ✓ for completed stages")
	}
	// Active stage uses the provided spinner frame.
	if !strings.Contains(out, "⠋") {
		t.Error("expected spinner frame on the active stage")
	}
	// Future stages render muted with their label text present
	// (so the user sees the canonical flow).
	if !strings.Contains(out, "pushing to remote") {
		t.Error("upcoming stage labels should be rendered (muted)")
	}
}

// TestAppShellHeaderHasLogoBreadcrumbAndProfileChip pins the iter-4
// app-shell refactor: renderHeader now produces a navigation rail
// (logo · breadcrumb trail) on the left and a status cluster
// (profile chip · sync badge) on the right. Pre-iter-4 it was just
// breadcrumbs + a badge; the new shape gives every screen a
// consistent top strip that carries identity, location, and health
// without each screen reinventing its own.
func TestAppShellHeaderHasLogoBreadcrumbAndProfileChip(t *testing.T) {
	ctx := &AppContext{
		State: &state.State{
			SyncRepoURL:   "git@example.com:x/y.git",
			ActiveProfile: "default",
			LastSyncedSHA: map[string]string{"default": "abcdef1"},
			LastSyncedAt:  map[string]time.Time{"default": time.Now().Add(-1 * time.Minute)},
		},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		HostName: "t-host",
		StateDir: t.TempDir(),
	}
	// Simulate a two-screen stack (Home + a deeper screen) so the
	// breadcrumb code path runs.
	stack := []screen{newHome(ctx), fakeScreen{title: "sync preview"}}

	out := renderHeader(stack, ctx, 120)
	if !strings.Contains(out, "ccsync") {
		t.Error("header should show the ccsync logo")
	}
	if !strings.Contains(out, "sync preview") {
		t.Error("header should show the leaf breadcrumb")
	}
	if !strings.Contains(out, "◉") || !strings.Contains(out, "default") {
		t.Error("header should show the profile chip for bootstrapped state")
	}
}

// TestToastLifecycle pins the iter-7 behavior: a showToastMsg sets
// AppModel.toast and schedules a matching toastExpireMsg. Delivery
// of that expire msg with the current id clears the toast; a stale
// expire (one whose id has been superseded by a newer toast) is a
// no-op so the replacement survives its full display window.
func TestToastLifecycle(t *testing.T) {
	ctx := &AppContext{
		State:    &state.State{LastSyncedSHA: map[string]string{}},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		StateDir: t.TempDir(),
	}
	m := New(ctx)

	// First toast: seq becomes 1, toast set.
	m2, _ := m.Update(showToastMsg{kind: toastSuccess, text: "done"})
	app, _ := m2.(AppModel)
	if app.toast == nil {
		t.Fatal("showToastMsg should set AppModel.toast")
	}
	if app.toast.id != 1 {
		t.Errorf("first toast id = %d, want 1", app.toast.id)
	}

	// Second toast arrives before the first expires: seq 2, older
	// expire tick must be ignored.
	m3, _ := app.Update(showToastMsg{kind: toastInfo, text: "two"})
	app, _ = m3.(AppModel)
	if app.toast.id != 2 || app.toast.text != "two" {
		t.Errorf("replacement toast state = id=%d text=%q", app.toast.id, app.toast.text)
	}
	// Stale expire for id=1 lands — toast must survive.
	m4, _ := app.Update(toastExpireMsg{id: 1})
	app, _ = m4.(AppModel)
	if app.toast == nil || app.toast.id != 2 {
		t.Fatal("stale expire for a superseded toast cleared the replacement")
	}
	// Expire for id=2 lands — now it clears.
	m5, _ := app.Update(toastExpireMsg{id: 2})
	app, _ = m5.(AppModel)
	if app.toast != nil {
		t.Fatal("matching-id expire did not clear the toast")
	}
}

// TestPaletteContextAwareSortSurfacesResolve pins the UX iter-2
// behaviour: when conflicts exist, opening the palette with an empty
// query should surface "Resolve conflicts" as the first row. The
// contextScore boost is what makes this possible; regressing either
// the boost or the sort ordering would leave users digging for the
// action they came to the palette for.
func TestPaletteContextAwareSortSurfacesResolve(t *testing.T) {
	ctx := &AppContext{
		State: &state.State{
			SyncRepoURL:   "git@example.com:x/y.git",
			ActiveProfile: "default",
			LastSyncedSHA: map[string]string{"default": "abc"},
		},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		StateDir: t.TempDir(),
		Plan: &sync.Plan{
			Conflicts: []sync.FileConflict{{Path: "profiles/default/claude/agents/foo.md"}},
		},
	}
	p := newPalette(ctx)
	if len(p.matches) == 0 {
		t.Fatal("palette should show commands with an empty query")
	}
	top := p.commands[p.matches[0]]
	if top.label != "Resolve conflicts" {
		t.Errorf("with unresolved conflicts, top empty-query result should be 'Resolve conflicts'; got %q",
			top.label)
	}
}

// TestCommandPaletteFilteringAndExecution pins the iter-10 command
// palette: typing narrows the match list, enter dispatches the
// cursored command plus a closePalette cmd, and esc closes without
// dispatching. A regression in fuzzy matching or the batched
// enter-flow would leave the palette feeling unresponsive; this
// test guards the three load-bearing behaviors.
func TestCommandPaletteFilteringAndExecution(t *testing.T) {
	ctx := &AppContext{
		State: &state.State{
			SyncRepoURL:   "git@example.com:x/y.git",
			ActiveProfile: "default",
			LastSyncedSHA: map[string]string{"default": "abc"},
		},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		StateDir: t.TempDir(),
	}
	p := newPalette(ctx)
	if len(p.matches) == 0 {
		t.Fatal("palette should show commands under an empty query")
	}

	// Type "set" — Settings should surface near the top.
	for _, r := range "set" {
		_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(p.matches) == 0 {
		t.Fatal("typing 'set' should match at least Settings")
	}
	top := p.commands[p.matches[0]]
	if top.label != "Settings" {
		t.Errorf("expected Settings at cursor; got %q", top.label)
	}

	// esc → closePaletteCmd only (no action dispatch).
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should produce a closePalette cmd")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("esc cmd produced nil msg")
	}

	// Fresh palette, type "doc" (Doctor), enter → batches close +
	// the doctor-screen switchTo. Just asserting a cmd is returned;
	// the batched payload is opaque to reflect without bubbletea-
	// internal peeking, and the underlying dispatch is exercised
	// live by hitting enter in the actual TUI.
	p = newPalette(ctx)
	for _, r := range "doc" {
		_, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(p.matches) == 0 || p.commands[p.matches[0]].label != "Doctor" {
		t.Errorf("expected Doctor to be the top match for 'doc'; got %d matches, first=%q",
			len(p.matches),
			func() string {
				if len(p.matches) == 0 {
					return ""
				}
				return p.commands[p.matches[0]].label
			}())
	}
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a filtered match should produce a cmd")
	}
}

// fakeScreen is a minimal screen implementation for header tests so we
// don't have to spin up real sync-preview / conflict-resolver models
// with all their dependencies. Title() is all the header needs.
type fakeScreen struct{ title string }

func (f fakeScreen) Title() string                         { return f.title }
func (f fakeScreen) Init() tea.Cmd                         { return nil }
func (f fakeScreen) Update(tea.Msg) (tea.Model, tea.Cmd) { return f, nil }
func (f fakeScreen) View() string                          { return "" }

// TestHomeDashboardBeforeBootstrapShowsHeroCard pins the no-repo
// state: Home renders a neutral hero card and a footer bar with the
// primary "enter" key bound to setup. Pre-iter-1 this was a bare
// paragraph of italicised hints; the goal now is that every launch
// — even on a fresh install — leads with a recognizable ccsync
// layout.
func TestHomeDashboardBeforeBootstrapShowsHeroCard(t *testing.T) {
	ctx := &AppContext{
		State:    &state.State{LastSyncedSHA: map[string]string{}},
		Config:   &config.Config{Profiles: map[string]config.ProfileSpec{"default": {}}},
		HostName: "fresh-host",
		StateDir: t.TempDir(),
	}
	h := newHome(ctx)
	out := h.renderDashboard()

	if !strings.Contains(out, "ccsync") {
		t.Error("Pre-bootstrap Home should still carry the wordmark")
	}
	if !strings.Contains(out, "NOT CONFIGURED") {
		t.Error("Pre-bootstrap Home should render the neutral hero card title")
	}
	if !strings.Contains(out, "start setup") {
		t.Error("Pre-bootstrap footer bar should bind enter to 'start setup'")
	}
}
