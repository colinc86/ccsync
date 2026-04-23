// Package crypto provides passphrase-based file encryption used for the
// optional "encrypt the sync repo" feature. Each repo file is encrypted
// with chacha20-poly1305 under a key derived from the user's passphrase
// (scrypt). A .ccsync-encrypted marker file in the repo root carries the
// salt so every machine derives the same key from the same passphrase.
package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/scrypt"
)

// MarkerFilename is the path (relative to the repo root) of the
// marker file that indicates a repo is encrypted. Stored in plaintext; its
// presence is the signal, its contents hold the KDF salt.
const MarkerFilename = ".ccsync-encrypted"

// KeyLen is chacha20-poly1305's 32-byte key size.
const KeyLen = chacha20poly1305.KeySize

// Marker is the on-disk shape of the marker file.
type Marker struct {
	Version int    `json:"version"`
	Scheme  string `json:"scheme"`  // always "scrypt-chacha20poly1305" for v1
	SaltB64 string `json:"saltB64"` // base64'd scrypt salt
}

// ReadMarker loads the marker from repoPath. Returns (nil, nil) when the
// repo is not encrypted (no marker present).
func ReadMarker(repoPath string) (*Marker, error) {
	data, err := os.ReadFile(filepath.Join(repoPath, MarkerFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m Marker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", MarkerFilename, err)
	}
	return &m, nil
}

// WriteMarker atomically writes a marker file to repoPath.
func WriteMarker(repoPath string, m *Marker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(repoPath, MarkerFilename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RemoveMarker deletes the marker file. No-op if it doesn't exist.
func RemoveMarker(repoPath string) error {
	err := os.Remove(filepath.Join(repoPath, MarkerFilename))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// NewMarker mints a fresh marker with a random salt, ready for enable.
func NewMarker() (*Marker, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return &Marker{
		Version: 1,
		Scheme:  "scrypt-chacha20poly1305",
		SaltB64: base64.StdEncoding.EncodeToString(salt),
	}, nil
}

// DeriveKey turns (passphrase, salt-from-marker) into the symmetric key.
// Uses scrypt with work factors appropriate for interactive login (~100ms
// on a modern laptop).
func (m *Marker) DeriveKey(passphrase string) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(m.SaltB64)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	return scrypt.Key([]byte(passphrase), salt, 1<<15, 8, 1, KeyLen)
}

// Encrypt returns an encrypted ciphertext prefixed with a magic header
// and a random nonce. An empty plaintext is NOT passed through — it
// encrypts to a minimum-size blob of magic + nonce + Poly1305 tag
// (36 bytes). Decrypt reverses this and yields an empty slice. Callers
// that care about "empty stays empty" (to reduce repo bloat for blank
// files) must short-circuit themselves; we always emit a validated
// envelope here so HasMagic can reliably distinguish ciphertext from
// stray plaintext during migration.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != KeyLen {
		return nil, fmt.Errorf("key length %d != %d", len(key), KeyLen)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	// Prepend a short magic so we can cheaply tell whether a given blob is
	// already encrypted (useful during migration and for sanity checks).
	ct := aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(Magic)+len(nonce)+len(ct))
	out = append(out, Magic...)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Decrypt reverses Encrypt. Returns a helpful error if the payload isn't
// an encrypted blob (missing magic).
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	if !HasMagic(ciphertext) {
		return nil, errors.New("data is not an encrypted blob (missing magic)")
	}
	if len(key) != KeyLen {
		return nil, fmt.Errorf("key length %d != %d", len(key), KeyLen)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	body := ciphertext[len(Magic):]
	if len(body) < aead.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := body[:aead.NonceSize()], body[aead.NonceSize():]
	return aead.Open(nil, nonce, ct, nil)
}

// Magic is the 8-byte header prepended to every encrypted blob. Lets us
// tell plaintext from ciphertext during migration without a decrypt trial.
var Magic = []byte("CCSYNCE1")

// HasMagic reports whether data starts with our encryption magic.
func HasMagic(data []byte) bool {
	if len(data) < len(Magic) {
		return false
	}
	for i, b := range Magic {
		if data[i] != b {
			return false
		}
	}
	return true
}
