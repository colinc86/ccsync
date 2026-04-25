package tui

import "testing"

// TestFitSettingsWindow_RendersEverythingWhenBudgetIsZero pins the
// "no WindowSizeMsg yet" fallback. A zero/negative budget returns
// the full slice so the first frame doesn't blank-out the screen
// before bubbletea has reported the terminal height.
func TestFitSettingsWindow_RendersEverythingWhenBudgetIsZero(t *testing.T) {
	rows := []settingRow{
		heading("a"),
		display("a1", "v"),
		display("a2", "v"),
		heading("b"),
		display("b1", "v"),
	}
	start, end := fitSettingsWindow(rows, 2, 0)
	if start != 0 || end != len(rows) {
		t.Errorf("zero budget should render everything; got [%d,%d)", start, end)
	}
}

// TestFitSettingsWindow_KeepsCursorVisible pins the load-bearing
// invariant: whatever window we return MUST contain the cursor row.
// Otherwise the cursor disappears off-screen and the user has no
// idea where their selection is.
func TestFitSettingsWindow_KeepsCursorVisible(t *testing.T) {
	rows := make([]settingRow, 30)
	for i := range rows {
		rows[i] = display("row", "v")
	}
	for _, c := range []int{0, 5, 14, 22, 29} {
		start, end := fitSettingsWindow(rows, c, 10)
		if c < start || c >= end {
			t.Errorf("cursor %d not in window [%d,%d)", c, start, end)
		}
	}
}

// TestFitSettingsWindow_ChargesHeadingsThreeLines pins that headings
// (which render as blank+label+rule) count as 3 lines against the
// budget. Without this, a section-heavy slice would over-stuff the
// viewport and the rendered output would scroll the cursor off.
func TestFitSettingsWindow_ChargesHeadingsThreeLines(t *testing.T) {
	rows := []settingRow{
		heading("section"),  // 3 lines
		display("row", "v"), // 1 line each
		display("row", "v"),
		display("row", "v"),
		display("row", "v"),
	}
	// Budget 6 fits the heading (3) + at most 3 normal rows.
	start, end := fitSettingsWindow(rows, 0, 6)
	rendered := 0
	for i := start; i < end; i++ {
		if isHeading(rows[i]) {
			rendered += 3
		} else {
			rendered++
		}
	}
	if rendered > 6 {
		t.Errorf("rendered line count %d exceeds budget 6 in window [%d,%d)", rendered, start, end)
	}
}

// TestFitSettingsWindow_TightBudgetStillIncludesCursor pins the
// edge case where lineBudget is barely big enough for the cursor
// row alone. The window MUST still include it — clipping the cursor
// is worse than overflowing the viewport by one line.
func TestFitSettingsWindow_TightBudgetStillIncludesCursor(t *testing.T) {
	rows := []settingRow{
		display("a", "v"),
		display("b", "v"),
		display("c", "v"),
	}
	start, end := fitSettingsWindow(rows, 1, 1)
	if start != 1 || end != 2 {
		t.Errorf("tight budget should isolate the cursor row; got [%d,%d)", start, end)
	}
}
