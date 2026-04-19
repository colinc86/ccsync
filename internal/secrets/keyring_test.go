package secrets

import (
	"errors"
	"testing"
)

func TestKey(t *testing.T) {
	if got := Key("default", "foo.bar"); got != "default:foo.bar" {
		t.Errorf("Key = %q", got)
	}
}

func TestStoreFetchDelete(t *testing.T) {
	MockInit()

	k := Key("test-profile", "x.y")
	if err := Store(k, "value"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := Fetch(k)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "value" {
		t.Errorf("Fetch = %q", got)
	}

	if err := Delete(k); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = Fetch(k)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, Fetch err = %v, want ErrNotFound", err)
	}

	// Deleting again is not an error.
	if err := Delete(k); err != nil {
		t.Errorf("second Delete should be no-op, got %v", err)
	}
}
