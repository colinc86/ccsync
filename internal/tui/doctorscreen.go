package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

	// Hero health card — state-reactive border maps to worst-
	// severity so the user reads "am I ok?" before parsing any
	// finding. Green for OK, warm for WARN, red for FAIL.
	var (
		card     lipgloss.Style
		titleSty lipgloss.Style
		glyph    string
		headline string
	)
	switch m.report.Worst() {
	case doctor.SeverityOK:
		card = theme.CardClean
		titleSty = theme.Good.Bold(true)
		glyph = "✓"
		headline = "ALL HEALTHY"
	case doctor.SeverityWarn:
		card = theme.CardPending
		titleSty = theme.Warn.Bold(true)
		glyph = "◦"
		headline = "WARNINGS"
	case doctor.SeverityFail:
		card = theme.CardConflict
		titleSty = theme.Bad
		glyph = "!"
		headline = "NEEDS ATTENTION"
	}
	// Count findings by severity for the chip row.
	var ok, warn, fail int
	for _, f := range m.report.Findings {
		switch f.Severity {
		case doctor.SeverityOK:
			ok++
		case doctor.SeverityWarn:
			warn++
		case doctor.SeverityFail:
			fail++
		}
	}
	var stats []string
	if ok > 0 {
		stats = append(stats, theme.ChipGood.Render(fmt.Sprintf("✓ %d ok", ok)))
	}
	if warn > 0 {
		stats = append(stats, theme.ChipWarn.Render(fmt.Sprintf("◦ %d warn", warn)))
	}
	if fail > 0 {
		stats = append(stats, theme.ChipBad.Render(fmt.Sprintf("! %d fail", fail)))
	}
	heroBody := titleSty.Render(glyph+"  "+headline) + "\n" +
		strings.Join(stats, theme.Rule.Render("  ·  "))
	sb.WriteString(card.Width(56).Render(heroBody) + "\n\n")

	for _, f := range m.report.Findings {
		sev := theme.ChipGood.Render(" ok ")
		switch f.Severity {
		case doctor.SeverityWarn:
			sev = theme.ChipWarn.Render("warn")
		case doctor.SeverityFail:
			sev = theme.ChipBad.Render("fail")
		}
		sb.WriteString(fmt.Sprintf("%s  %s\n", sev, theme.Secondary.Render(f.Check)))
		sb.WriteString("       " + f.Message + "\n")
		if f.Suggest != "" {
			sb.WriteString("       " + theme.Hint.Render("→ "+f.Suggest) + "\n")
		}
	}
	sb.WriteString("\n" + renderFooterBar([]footerKey{
		{cap: "r", label: "re-run"},
		{cap: "esc", label: "back"},
	}))
	return sb.String()
}
