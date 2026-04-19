package gitx

import (
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Sentinel errors. Callers can errors.Is() against these.
var (
	ErrAuthRequired    = errors.New("the remote requires authentication; configure SSH key or HTTPS token in Settings")
	ErrAuthFailed      = errors.New("authentication to the remote failed; check your SSH key or HTTPS token")
	ErrRepoNotFound    = errors.New("the remote repository was not found; check the URL")
	ErrNothingToCommit = errors.New("nothing to commit")
	ErrNonFastForward  = errors.New("remote has changes we don't yet have; pull first")
)

// Translate wraps a go-git error into a user-readable one while preserving
// the original chain via errors.Unwrap. Returns nil unchanged.
func Translate(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, transport.ErrAuthenticationRequired):
		return fmt.Errorf("%w: %v", ErrAuthRequired, err)
	case errors.Is(err, transport.ErrAuthorizationFailed):
		return fmt.Errorf("%w: %v", ErrAuthFailed, err)
	case errors.Is(err, transport.ErrRepositoryNotFound):
		return fmt.Errorf("%w: %v", ErrRepoNotFound, err)
	case errors.Is(err, git.ErrEmptyCommit):
		return fmt.Errorf("%w: %v", ErrNothingToCommit, err)
	case errors.Is(err, git.ErrNonFastForwardUpdate):
		return fmt.Errorf("%w: %v", ErrNonFastForward, err)
	}
	return err
}
