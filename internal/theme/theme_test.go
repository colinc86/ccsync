package theme

import "testing"

func TestPaletteNonEmpty(t *testing.T) {
	colors := map[string]string{
		"Accent":   string(Accent),
		"Accent2":  string(Accent2),
		"Cream":    string(Cream),
		"Ink":      string(Ink),
		"Muted":    string(Muted),
		"Success":  string(Success),
		"Warning":  string(Warning),
		"Conflict": string(Conflict),
	}
	for name, v := range colors {
		if v == "" {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestStylesRender(t *testing.T) {
	for name, got := range map[string]string{
		"Primary":   Primary.Render("x"),
		"Secondary": Secondary.Render("x"),
		"Heading":   Heading.Render("x"),
		"Good":      Good.Render("x"),
		"Warn":      Warn.Render("x"),
		"Bad":       Bad.Render("x"),
		"Hint":      Hint.Render("x"),
	} {
		if got == "" {
			t.Errorf("%s rendered empty", name)
		}
	}
}
