package sync

import (
	"errors"
	"fmt"
	"strings"

	cryptopkg "github.com/colinc86/ccsync/internal/crypto"
	"github.com/colinc86/ccsync/internal/secrets"
)

// SecretsKeyPassphrase is the keychain entry that caches the repo
// passphrase after the user enters it once. Exposed so the CLI/TUI
// enable/disable flows can store/fetch the same value.
const SecretsKeyPassphrase = "repo-encryption-passphrase"

// loadRepoEncryptionKey checks repoPath for a crypto marker and, if
// present, derives the symmetric key from the keychain-stored passphrase.
// Returns (nil, nil) when the repo is not encrypted.
func loadRepoEncryptionKey(repoPath string) ([]byte, error) {
	m, err := cryptopkg.ReadMarker(repoPath)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, nil
	}
	pp, err := secrets.Fetch(SecretsKeyPassphrase)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return nil, fmt.Errorf(
				"repo is encrypted but no passphrase is stored on this machine " +
					"(run `ccsync encrypt login` or enter it via the TUI)")
		}
		return nil, err
	}
	return m.DeriveKey(pp)
}

// maybeDecrypt returns plaintext. When the repo is not encrypted (key nil)
// or the payload lacks the magic header (old plaintext still lying around
// during mid-migration), data passes through unchanged.
func maybeDecrypt(key, data []byte) ([]byte, error) {
	if key == nil || !cryptopkg.HasMagic(data) {
		return data, nil
	}
	return cryptopkg.Decrypt(key, data)
}

// maybeEncrypt is the mirror of maybeDecrypt for the write path. Some
// well-known metadata files stay plaintext so a user browsing the repo on
// GitHub can still orient themselves — the marker, config, manifest, and
// readme. Everything else under profiles/ gets encrypted.
func maybeEncrypt(key []byte, repoPath string, data []byte) ([]byte, error) {
	if key == nil {
		return data, nil
	}
	if isMetadataPath(repoPath) {
		return data, nil
	}
	return cryptopkg.Encrypt(key, data)
}

func isMetadataPath(repoPath string) bool {
	switch repoPath {
	case cryptopkg.MarkerFilename, ".syncignore", "ccsync.yaml", "manifest.json", "README.md":
		return true
	}
	// Any .bak companion of the above stays plaintext too.
	if strings.HasSuffix(repoPath, ".bak") {
		return true
	}
	return false
}
