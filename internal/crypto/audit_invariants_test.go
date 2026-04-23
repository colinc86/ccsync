package crypto

import (
	"bytes"
	"testing"
)

// TestDecryptTruncatedEnvelopeBoundary pins iter-43 audit: Decrypt
// must reject envelopes that carry magic+nonce but no ciphertext
// (including no Poly1305 tag) with a clean error, not a panic or an
// empty-plaintext return. chacha20poly1305's Open requires at least
// a 16-byte tag after the nonce; truncated payloads fail
// authentication. This test pins that rejection so a future
// refactor of the len check can't regress to empty-plaintext-OK.
func TestDecryptTruncatedEnvelopeBoundary(t *testing.T) {
	marker, err := NewMarker()
	if err != nil {
		t.Fatal(err)
	}
	key, err := marker.DeriveKey("pp")
	if err != nil {
		t.Fatal(err)
	}

	// Build magic + (24-byte) nonce + nothing — the ciphertext (incl.
	// Poly1305 tag) is empty.
	env := append([]byte{}, Magic...)
	nonce := make([]byte, 24)
	env = append(env, nonce...)

	_, err = Decrypt(key, env)
	if err == nil {
		t.Fatal("Decrypt must reject envelope with no ciphertext body")
	}
}

// TestDecryptWrongKeyLengthRejected pins iter-43 audit: Decrypt
// validates the key length up-front (line 136) with a clear
// error message. Keys are keyring-derived so length mismatch is
// rare, but if it ever happens (corrupt keychain entry, truncated
// marker), we want the error to name the mismatch rather than
// produce an opaque chacha20poly1305 error later.
func TestDecryptWrongKeyLengthRejected(t *testing.T) {
	shortKey := bytes.Repeat([]byte{0x01}, KeyLen-1)

	// Build a valid-looking envelope just to get past HasMagic.
	env := append([]byte{}, Magic...)
	env = append(env, make([]byte, 24)...) // nonce
	env = append(env, make([]byte, 16)...) // fake tag

	_, err := Decrypt(shortKey, env)
	if err == nil {
		t.Fatal("Decrypt must reject a short key")
	}
}
