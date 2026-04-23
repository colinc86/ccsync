package crypto

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	m, err := NewMarker()
	if err != nil {
		t.Fatal(err)
	}
	key, err := m.DeriveKey("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("my sensitive agent file\n- one\n- two\n")
	ct, err := Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if !HasMagic(ct) {
		t.Fatal("missing magic on ciphertext")
	}
	if bytes.Contains(ct, plain) {
		t.Fatal("plaintext leaked into ciphertext")
	}
	back, err := Decrypt(key, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, plain) {
		t.Fatalf("round-trip mismatch: %q", back)
	}
}

func TestWrongPassphraseFails(t *testing.T) {
	m, _ := NewMarker()
	ct, _ := Encrypt(mustKey(t, m, "right"), []byte("hello"))
	if _, err := Decrypt(mustKey(t, m, "wrong"), ct); err == nil {
		t.Fatal("expected error decrypting with wrong passphrase")
	}
}

func TestSameSaltSameKey(t *testing.T) {
	m, _ := NewMarker()
	a := mustKey(t, m, "pp")
	b := mustKey(t, m, "pp")
	if !bytes.Equal(a, b) {
		t.Fatal("DeriveKey must be deterministic for same salt+passphrase")
	}
}

func TestRejectUnencryptedOnDecrypt(t *testing.T) {
	m, _ := NewMarker()
	key := mustKey(t, m, "pp")
	if _, err := Decrypt(key, []byte("plain text")); err == nil {
		t.Fatal("expected magic check to fail on plaintext")
	}
}

// TestEmptyPlaintextRoundTrips pins the "empty in, empty out" behavior.
// Pre-v0.6.x the doc comment claimed empty inputs passed through
// unchanged — not true; they encrypt to a 36-byte envelope (magic +
// nonce + Poly1305 tag). The envelope decrypts back to empty, which
// is the contract that actually matters. Keep this test to catch any
// future attempt to "optimize" empty plaintexts out; the magic header
// must be present on every ciphertext or HasMagic breaks during
// encrypt/decrypt migrations.
func TestEmptyPlaintextRoundTrips(t *testing.T) {
	m, _ := NewMarker()
	key := mustKey(t, m, "pp")
	ct, err := Encrypt(key, []byte{})
	if err != nil {
		t.Fatalf("encrypt empty: %v", err)
	}
	if !HasMagic(ct) {
		t.Fatal("encrypted empty blob must still carry magic header")
	}
	if len(ct) == 0 {
		t.Fatal("encrypted empty must NOT be zero-length — HasMagic would then fail to distinguish it from stray plaintext")
	}
	back, err := Decrypt(key, ct)
	if err != nil {
		t.Fatalf("decrypt empty ct: %v", err)
	}
	if len(back) != 0 {
		t.Errorf("empty round-trip produced %d bytes, want 0", len(back))
	}
}

func mustKey(t *testing.T, m *Marker, passphrase string) []byte {
	t.Helper()
	k, err := m.DeriveKey(passphrase)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
