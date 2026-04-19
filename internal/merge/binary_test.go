package merge

import (
	"testing"
	"time"
)

func TestBinaryLWW(t *testing.T) {
	local := []byte("local")
	remote := []byte("remote")
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Hour)

	if r := Binary(local, later, remote, earlier); string(r.Merged) != "local" {
		t.Errorf("later local should win: %q", r.Merged)
	}
	if r := Binary(local, earlier, remote, later); string(r.Merged) != "remote" {
		t.Errorf("later remote should win: %q", r.Merged)
	}
	if r := Binary(nil, earlier, remote, later); string(r.Merged) != "remote" {
		t.Errorf("absent local, remote wins: %q", r.Merged)
	}
	if r := Binary(local, earlier, nil, later); string(r.Merged) != "local" {
		t.Errorf("absent remote, local wins: %q", r.Merged)
	}
	if r := Binary(nil, earlier, nil, later); r.Merged != nil {
		t.Errorf("both absent should produce empty: %q", r.Merged)
	}
}
