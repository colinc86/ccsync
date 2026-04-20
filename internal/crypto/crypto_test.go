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

func mustKey(t *testing.T, m *Marker, passphrase string) []byte {
	t.Helper()
	k, err := m.DeriveKey(passphrase)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
