package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/doctor"
	"github.com/colinc86/ccsync/internal/theme"
)

type doctorScreenModel struct {
	ctx    *AppContext
	report doctor.Report
}

func newDoctorScreen(ctx *AppContext) *doctorScreenModel {
	return &doctorScreenModel{ctx: ctx, report: runCheck(ctx)}
}

func runCheck(ctx *AppContext) doctor.Report {
	repoPath := ""
	if ctx.State.SyncRepoURL != "" {
		repoPath = ctx.RepoPath
	}
	return doctor.Check(doctor.Inputs{
		ClaudeDir:  ctx.ClaudeDir,
		ClaudeJSON: ctx.ClaudeJSON,
		RepoPath:   repoPath,
		StateDir:   ctx.StateDir,
	})
}

func (m *doctorScreenModel) Title() string { return "Doctor — integrity checks" }
func (m *doctorScreenModel) Init() tea.Cmd { return nil }

func (m *doctorScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			m.report = runCheck(m.ctx)
		}
	}
	return m, nil
}

func (m *doctorScreenModel) View() string {
	var sb strings.Builder
	worstStyle := theme.Good
	switch m.report.Worst() {
	case doctor.SeverityWarn:
		worstStyle = theme.Warn
	case doctor.SeverityFail:
		worstStyle = theme.Bad
	}
	sb.WriteString(worstStyle.Render(fmt.Sprintf("status: %s", m.report.Worst())) + "\n\n")

	for _, f := range m.report.Findings {
		sev := theme.Good.Render(fmt.Sprintf("[%s]", f.Severity))
		switch f.Severity {
		case doctor.SeverityWarn:
			sev = theme.Warn.Render(fmt.Sprintf("[%s]", f.Severity))
		case doctor.SeverityFail:
			sev = theme.Bad.Render(fmt.Sprintf("[%s]", f.Severity))
		}
		sb.WriteString(fmt.Sprintf("%s %s\n", sev, theme.Secondary.Render(f.Check)))
		sb.WriteString("       " + f.Message + "\n")
		if f.Suggest != "" {
			sb.WriteString("       " + theme.Hint.Render("→ "+f.Suggest) + "\n")
		}
	}
	sb.WriteString("\n" + theme.Primary.Render("r ") + "re-run")
	return sb.String()
}
