package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/colinc86/ccsync/internal/theme"
)

// diffModel shows a colored line-level diff of two byte sequences inside a
// scrollable viewport. Reusable across SyncPreview, ConflictResolver, and
// SyncHistory — callers construct it with (label, before, after) and push
// it onto the screen stack.
type diffModel struct {
	label    string
	vp       viewport.Model
	ready    bool
	rendered string
}

func newDiffView(label string, before, after []byte) *diffModel {
	d := &diffModel{label: label}
	d.rendered = renderUnifiedDiff(before, after)
	return d
}

func (d *diffModel) Title() string { return "Diff — " + d.label }

func (d *diffModel) Init() tea.Cmd { return nil }

func (d *diffModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Reserve space for the app's header (2 lines) + body padding (2) +
		// status bar (1) + footer (1) + our own footer hint (2). Approximate.
		h := msg.Height - 10
		if h < 5 {
			h = 5
		}
		w := msg.Width
		if w < 20 {
			w = 20
		}
		if !d.ready {
			d.vp = viewport.New(w, h)
			d.ready = true
		} else {
			d.vp.Width = w
			d.vp.Height = h
		}
		d.vp.SetContent(d.rendered)
	case tea.KeyMsg:
		switch msg.String() {
		case "g":
			d.vp.GotoTop()
			return d, nil
		case "G":
			d.vp.GotoBottom()
			return d, nil
		}
	}
	if d.ready {
		var cmd tea.Cmd
		d.vp, cmd = d.vp.Update(msg)
		return d, cmd
	}
	return d, nil
}

func (d *diffModel) View() string {
	if !d.ready {
		return d.rendered
	}
	return d.vp.View() + "\n" +
		theme.Hint.Render("↑↓ scroll • g/G top/bottom • esc back")
}

// renderUnifiedDiff produces a line-level colored diff of before vs after.
// No @@ hunk headers (that's a git-style mode) — we render every line with
// context, which is more readable for the typical size of a ccsync file.
// For binary-looking inputs, fall back to a summary.
func renderUnifiedDiff(before, after []byte) string {
	if looksBinary(before) || looksBinary(after) {
		return theme.Hint.Render(fmt.Sprintf(
			"(binary content — %d → %d bytes, diff not shown)",
			len(before), len(after)))
	}
	beforeStr, afterStr := string(before), string(after)
	if beforeStr == afterStr {
		return theme.Hint.Render("(no changes)")
	}

	dmp := diffmatchpatch.New()
	a, b, lineArr := dmp.DiffLinesToChars(beforeStr, afterStr)
	diffs := dmp.DiffMain(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArr)

	var sb strings.Builder
	for _, d := range diffs {
		text := d.Text
		// go-diff returns each chunk with trailing newlines preserved — iterate
		// line-by-line so we can color each line individually.
		lines := strings.SplitAfter(text, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			clean := strings.TrimRight(line, "\n")
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				sb.WriteString(theme.Hint.Render("  " + clean))
			case diffmatchpatch.DiffInsert:
				sb.WriteString(theme.Good.Render("+ " + clean))
			case diffmatchpatch.DiffDelete:
				sb.WriteString(theme.Bad.Render("- " + clean))
			}
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// looksBinary reports whether data appears to be binary content (has a NUL
// byte in the first 512 bytes). Rough but matches git's heuristic.
func looksBinary(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
