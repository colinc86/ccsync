package gitx

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

// TestFriendlyCollapsesSentinel proves Friendly drops the raw go-git
// chain in favor of the sentinel's message. Pre-fix, the TUI rendered
// the full err.Error() which appended "transport.ErrAuthenticationRequired: …"
// after our friendly prefix — jargon at the tail, the user's eye
// landed on the wrong thing.
func TestFriendlyCollapsesSentinel(t *testing.T) {
	wrapped := Translate(fmt.Errorf("wrap: %w", transport.ErrAuthenticationRequired))
	got := Friendly(wrapped)
	if got != ErrAuthRequired.Error() {
		t.Errorf("Friendly should collapse to sentinel; got %q, want %q", got, ErrAuthRequired.Error())
	}
}

func TestFriendlyPatternMatches(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no such host", errors.New("dial tcp: lookup git.example.com: no such host"),
			"couldn't reach the remote — check your internet connection"},
		{"connection refused", errors.New("dial tcp 1.2.3.4: connect: connection refused"),
			"couldn't connect to the remote — check the URL and your connection"},
		{"i/o timeout", errors.New("read tcp: i/o timeout"),
			"the remote is slow or unreachable — check your connection and try again"},
		{"no space", errors.New("write /var/foo: no space left on device"),
			"no space left on device — free up disk space and retry"},
		{"fs perm", errors.New("open /root/secret: permission denied"),
			"permission denied — check file permissions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Friendly(c.err); got != c.want {
				t.Errorf("Friendly(%q) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

func TestFriendlyNilAndPassthrough(t *testing.T) {
	if Friendly(nil) != "" {
		t.Error("Friendly(nil) must be empty")
	}
	unmatched := errors.New("some weird error we don't recognize")
	if got := Friendly(unmatched); got != unmatched.Error() {
		t.Errorf("unrecognized errors should pass through unchanged; got %q", got)
	}
}
