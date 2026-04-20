package sync

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	cryptopkg "github.com/colinc86/ccsync/internal/crypto"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/secrets"
)

// EnableEncryption writes a fresh marker, stores passphrase in keychain,
// re-encrypts every non-metadata tracked file in the repo under profiles/,
// commits the migration, and pushes. Idempotent: running it on an
// already-encrypted repo returns an error so the user doesn't re-scramble.
func EnableEncryption(ctx context.Context, in Inputs, passphrase string) (Result, error) {
	if existing, err := cryptopkg.ReadMarker(in.RepoPath); err != nil {
		return Result{}, err
	} else if existing != nil {
		return Result{}, fmt.Errorf("repo is already encrypted")
	}

	marker, err := cryptopkg.NewMarker()
	if err != nil {
		return Result{}, err
	}
	key, err := marker.DeriveKey(passphrase)
	if err != nil {
		return Result{}, err
	}
	if err := cryptopkg.WriteMarker(in.RepoPath, marker); err != nil {
		return Result{}, err
	}
	if err := secrets.Store(SecretsKeyPassphrase, passphrase); err != nil {
		return Result{}, fmt.Errorf("store passphrase: %w", err)
	}

	if err := walkAndTransform(in.RepoPath, func(relPath string, data []byte) ([]byte, error) {
		if isMetadataPath(relPath) {
			return data, nil
		}
		if cryptopkg.HasMagic(data) {
			return data, nil // already encrypted somehow — leave alone
		}
		return cryptopkg.Encrypt(key, data)
	}); err != nil {
		return Result{}, err
	}

	return commitMigration(ctx, in, "enable repo encryption")
}

// DisableEncryption decrypts every tracked repo file, removes the marker,
// commits, pushes, and clears the keychain passphrase. Requires the
// current passphrase to be resolvable from the keychain so a half-broken
// state can't sneak through.
func DisableEncryption(ctx context.Context, in Inputs) (Result, error) {
	marker, err := cryptopkg.ReadMarker(in.RepoPath)
	if err != nil {
		return Result{}, err
	}
	if marker == nil {
		return Result{}, fmt.Errorf("repo is not encrypted")
	}
	pp, err := secrets.Fetch(SecretsKeyPassphrase)
	if err != nil {
		return Result{}, fmt.Errorf("need passphrase in keychain to disable encryption: %w", err)
	}
	key, err := marker.DeriveKey(pp)
	if err != nil {
		return Result{}, err
	}

	if err := walkAndTransform(in.RepoPath, func(relPath string, data []byte) ([]byte, error) {
		if isMetadataPath(relPath) {
			return data, nil
		}
		if !cryptopkg.HasMagic(data) {
			return data, nil // already plaintext
		}
		return cryptopkg.Decrypt(key, data)
	}); err != nil {
		return Result{}, err
	}
	if err := cryptopkg.RemoveMarker(in.RepoPath); err != nil {
		return Result{}, err
	}
	_ = secrets.Delete(SecretsKeyPassphrase)

	return commitMigration(ctx, in, "disable repo encryption")
}

// walkAndTransform runs transform(relPath, contents) on every regular file
// under profiles/ inside repoPath, writing the transformed bytes back
// atomically. Other files are untouched.
func walkAndTransform(repoPath string, transform func(relPath string, data []byte) ([]byte, error)) error {
	return filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(repoPath, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		// Only files under profiles/ are transformed. Top-level metadata
		// (.syncignore, ccsync.yaml, etc.) is left alone by isMetadataPath
		// anyway, but scoping here avoids stray matches.
		if !strings.HasPrefix(rel, "profiles/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := transform(rel, data)
		if err != nil {
			return fmt.Errorf("transform %s: %w", rel, err)
		}
		if len(out) == len(data) {
			same := true
			for i := range out {
				if out[i] != data[i] {
					same = false
					break
				}
			}
			if same {
				return nil
			}
		}
		return writeFileAtomic(path, out)
	})
}

func commitMigration(ctx context.Context, in Inputs, subject string) (Result, error) {
	repo, err := gitx.Open(in.RepoPath)
	if err != nil {
		return Result{}, err
	}
	if err := repo.AddAll(); err != nil {
		return Result{}, err
	}
	hasChanges, err := repo.HasChanges()
	if err != nil {
		return Result{}, err
	}
	if !hasChanges {
		return Result{}, nil
	}
	commitSHA, err := repo.Commit(subject, in.HostName, in.AuthorEmail)
	if err != nil {
		return Result{}, err
	}
	if err := repo.Push(ctx, in.Auth); err != nil {
		return Result{}, err
	}
	return Result{CommitSHA: commitSHA}, nil
}
