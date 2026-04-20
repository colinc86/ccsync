package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

type syncPreviewModel struct {
	ctx     *AppContext
	loading bool
	err     error
	plan    sync.Plan
}

func newSyncPreview(ctx *AppContext) *syncPreviewModel {
	return &syncPreviewModel{ctx: ctx, loading: true}
}

func (m *syncPreviewModel) Title() string { return "Sync preview (dry run)" }

type previewDoneMsg struct {
	plan sync.Plan
	err  error
}

func (m *syncPreviewModel) Init() tea.Cmd {
	return runDryRun(m.ctx)
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
	case "ssh":
		kind = gitx.AuthSSH
	case "https":
		kind = gitx.AuthHTTPS
	case "":
		kind = gitx.AuthSSH
	}
	auth := gitx.AuthConfig{Kind: kind, SSHKeyPath: ctx.State.SSHKeyPath, HTTPSUser: ctx.State.HTTPSUser}
	a, _ := auth.Resolve()
	return a
}

func (m *syncPreviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case previewDoneMsg:
		m.loading = false
		m.err = msg.err
		m.plan = msg.plan
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
		return theme.Hint.Render("computing change set…")
	}
	if m.err != nil {
		return theme.Bad.Render("error: ") + m.err.Error()
	}

	added, modified, deleted := m.plan.Summary()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s   %s   %s\n",
		theme.Good.Render(fmt.Sprintf("+%d added", added)),
		theme.Warn.Render(fmt.Sprintf("~%d modified", modified)),
		theme.Bad.Render(fmt.Sprintf("-%d deleted", deleted)),
	))
	sb.WriteString("\n")

	if len(m.plan.Actions) == 0 {
		sb.WriteString(theme.Good.Render("in sync — nothing to do"))
		return sb.String()
	}

	shown := 0
	excluded := 0
	for _, a := range m.plan.Actions {
		if a.ExcludedByProfile {
			excluded++
			continue
		}
		if a.Action == manifest.ActionNoOp {
			continue
		}
		shown++
		sb.WriteString(fmt.Sprintf("  %s %s\n", actionGlyph(a.Action), a.Path))
		if shown >= 30 {
			sb.WriteString(theme.Hint.Render(fmt.Sprintf("  … %d more\n", len(m.plan.Actions)-shown)))
			break
		}
	}

	if len(m.plan.Conflicts) > 0 {
		sb.WriteString("\n" + theme.Bad.Render(fmt.Sprintf("%d conflict(s) — will be skipped:", len(m.plan.Conflicts))) + "\n")
		for i, c := range m.plan.Conflicts {
			if i >= 5 {
				sb.WriteString(theme.Hint.Render(fmt.Sprintf("  … %d more\n", len(m.plan.Conflicts)-i)))
				break
			}
			sb.WriteString("  ! " + c.Path + "\n")
		}
	}

	if excluded > 0 {
		sb.WriteString("\n" + theme.Hint.Render(
			fmt.Sprintf("%d path(s) excluded by profile %q — run `ccsync why <path>` to see which rule",
				excluded, m.ctx.State.ActiveProfile)) + "\n")
	}

	sb.WriteString("\n" + theme.Primary.Render("enter ") + "apply all • " +
		theme.Primary.Render("s ") + "selective • " +
		theme.Hint.Render("esc cancel"))
	return sb.String()
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
