package tui

import (
	"context"
	"fmt"
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
		// This screen already paid for a dry-run; cache it so the status bar
		// and Home dashboard use the same plan without a second network hit.
		if m.err == nil {
			m.ctx.Plan = &msg.plan
			m.ctx.PlanTime = time.Now()
			m.ctx.PlanErr = nil
		}
		// Auto-apply on clean syncs, when the user opted in.
		if m.err == nil && m.ctx.State.AutoApplyClean && m.planIsClean() {
			return m, switchTo(newSync(m.ctx))
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if !m.loading && m.err == nil {
				return m, switchTo(newSync(m.ctx))
			}
		case "s":
			if !m.loading && m.err == nil {
				return m, switchTo(newSelectiveSync(m.ctx, m.plan))
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

	fmt.Fprintf(&sb, "%s   %s   %s\n",
		theme.Warn.Render(fmt.Sprintf("↑ %d push", len(push))),
		theme.Warn.Render(fmt.Sprintf("↓ %d pull", len(pull))),
		theme.Bad.Render(fmt.Sprintf("! %d conflict", len(m.plan.Conflicts))),
	)
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

	writeGroup := func(label string, actions []sync.FileAction) {
		if len(actions) == 0 {
			return
		}
		sb.WriteString(theme.Secondary.Render(label) + "\n")
		for i, a := range actions {
			if i >= 30 {
				fmt.Fprintf(&sb, theme.Hint.Render("  … %d more\n"), len(actions)-i)
				break
			}
			fmt.Fprintf(&sb, "  %s %s\n", actionGlyph(a.Action), a.Path)
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

	sb.WriteString("\n" + theme.Primary.Render("enter ") + "apply all • " +
		theme.Primary.Render("s ") + "selective • " +
		theme.Hint.Render("esc cancel"))
	return sb.String()
}

// planIsClean reports whether the plan has real work but no conflicts and
// no placeholder-restore concerns. Profile-excluded paths don't count as
// conflicts. An empty plan (nothing to do) is also "clean" — no-op apply.
func (m *syncPreviewModel) planIsClean() bool {
	return len(m.plan.Conflicts) == 0
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
