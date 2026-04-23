package humanize

import "testing"

func TestPlural(t *testing.T) {
	cases := []struct {
		n        int
		singular string
		want     string
	}{
		{0, "file", "files"},
		{1, "file", "file"},
		{2, "file", "files"},
		{17, "conflict", "conflicts"},
	}
	for _, c := range cases {
		if got := Plural(c.n, c.singular); got != c.want {
			t.Errorf("Plural(%d, %q) = %q, want %q", c.n, c.singular, got, c.want)
		}
	}
}

func TestPluralForm(t *testing.T) {
	if got := PluralForm(1, "leaf", "leaves"); got != "leaf" {
		t.Errorf("PluralForm(1) irregular = %q", got)
	}
	if got := PluralForm(3, "leaf", "leaves"); got != "leaves" {
		t.Errorf("PluralForm(3) irregular = %q", got)
	}
}

func TestCount(t *testing.T) {
	cases := []struct {
		n        int
		singular string
		want     string
	}{
		{0, "file", "0 files"},
		{1, "file", "1 file"},
		{2, "file", "2 files"},
	}
	for _, c := range cases {
		if got := Count(c.n, c.singular); got != c.want {
			t.Errorf("Count(%d, %q) = %q, want %q", c.n, c.singular, got, c.want)
		}
	}
}

func TestJoin(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a and b"},
		{[]string{"a", "b", "c"}, "a, b, and c"},
		{[]string{"a", "b", "c", "d"}, "a, b, c, and d"},
	}
	for _, c := range cases {
		if got := Join(c.in); got != c.want {
			t.Errorf("Join(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
