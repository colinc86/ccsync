package gitx

import (
	"errors"
	"fmt"
	"strings"

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
	ErrEmptyRemote     = errors.New("the remote repository is empty")
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
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		return fmt.Errorf("%w: %v", ErrEmptyRemote, err)
	}
	return err
}

// Friendly returns the most user-readable one-line form of err. Known
// gitx sentinels collapse to their own message (dropping the raw
// go-git chain, which is jargon the user doesn't need). Common
// non-gitx error shapes (network, filesystem) are pattern-matched
// and translated too — string-matching is fragile but go doesn't
// export sentinels for those cases, and "connection refused" is
// stable enough across platforms to rely on.
//
// Falls back to err.Error() when nothing matches. Returns "" for nil.
// Intended for TUI error rendering; never used in logs or tests
// (which should read err.Error() / errors.Is).
func Friendly(err error) string {
	if err == nil {
		return ""
	}
	for _, s := range []error{ErrAuthRequired, ErrAuthFailed, ErrRepoNotFound, ErrNonFastForward, ErrNothingToCommit} {
		if errors.Is(err, s) {
			return s.Error()
		}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such host"):
		return "couldn't reach the remote — check your internet connection"
	case strings.Contains(msg, "connection refused"):
		return "couldn't connect to the remote — check the URL and your connection"
	case strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded"):
		return "the remote is slow or unreachable — check your connection and try again"
	case strings.Contains(msg, "no space left on device"):
		return "no space left on device — free up disk space and retry"
	case strings.Contains(msg, "permission denied"):
		// If gitx sentinels didn't match above, this is almost
		// certainly filesystem permissions — auth-perm-denied would
		// have been caught by transport.ErrAuth* at an earlier layer.
		return "permission denied — check file permissions"
	}
	return msg
}
