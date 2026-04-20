// Package gitx wraps go-git to give ccsync a small, plain-English API over
// clone/open/pull/commit/push. Transport details stay out of callers, and
// library errors are translated in errors.go before they leave this package.
package gitx

import (
	"context"
	"errors"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Repo is a working-tree repository.
type Repo struct {
	repo *git.Repository
	path string
}

// Clone clones url into path. Fails if path exists.
func Clone(ctx context.Context, url, path string, auth transport.AuthMethod) (*Repo, error) {
	r, err := git.PlainCloneContext(ctx, path, false, &git.CloneOptions{
		URL:  url,
		Auth: auth,
	})
	if err != nil {
		return nil, Translate(err)
	}
	return &Repo{repo: r, path: path}, nil
}

// Init initializes a new repo at path. If remoteURL is non-empty, sets origin.
func Init(path, remoteURL string) (*Repo, error) {
	r, err := git.PlainInit(path, false)
	if err != nil {
		return nil, Translate(err)
	}
	if remoteURL != "" {
		if _, err := r.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{remoteURL},
		}); err != nil {
			return nil, Translate(err)
		}
	}
	return &Repo{repo: r, path: path}, nil
}

// Open opens an existing repo at path.
func Open(path string) (*Repo, error) {
	r, err := git.PlainOpen(path)
	if err != nil {
		return nil, Translate(err)
	}
	return &Repo{repo: r, path: path}, nil
}

// Path returns the working-tree path.
func (r *Repo) Path() string { return r.path }

// IsEmpty reports whether the repo has zero commits.
func (r *Repo) IsEmpty() (bool, error) {
	_, err := r.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return true, nil
		}
		return false, Translate(err)
	}
	return false, nil
}

// HeadSHA returns the hex SHA at HEAD, or "" if the repo has no commits.
func (r *Repo) HeadSHA() (string, error) {
	ref, err := r.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return "", nil
		}
		return "", Translate(err)
	}
	return ref.Hash().String(), nil
}

// BlobAtCommit returns the file content at path at the given commit SHA.
// ok=false means the file does not exist at that commit.
func (r *Repo) BlobAtCommit(commitSHA, path string) ([]byte, bool, error) {
	if commitSHA == "" {
		return nil, false, nil
	}
	h := plumbing.NewHash(commitSHA)
	commit, err := r.repo.CommitObject(h)
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return nil, false, nil
		}
		return nil, false, Translate(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, false, Translate(err)
	}
	file, err := tree.File(path)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, false, nil
		}
		return nil, false, Translate(err)
	}
	contents, err := file.Contents()
	if err != nil {
		return nil, false, Translate(err)
	}
	return []byte(contents), true, nil
}

// BlobAtHead returns the file content at path at HEAD.
// ok=false means the file does not exist at HEAD (or the repo has no commits).
func (r *Repo) BlobAtHead(path string) ([]byte, bool, error) {
	ref, err := r.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil, false, nil
		}
		return nil, false, Translate(err)
	}
	commit, err := r.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, false, Translate(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, false, Translate(err)
	}
	file, err := tree.File(path)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, false, nil
		}
		return nil, false, Translate(err)
	}
	contents, err := file.Contents()
	if err != nil {
		return nil, false, Translate(err)
	}
	return []byte(contents), true, nil
}

// Pull fetches from origin and fast-forwards. Non-fast-forward returns an error.
// Callers handle non-FF by computing a three-way merge themselves.
func (r *Repo) Pull(ctx context.Context, auth transport.AuthMethod) error {
	wt, err := r.repo.Worktree()
	if err != nil {
		return Translate(err)
	}
	err = wt.PullContext(ctx, &git.PullOptions{
		RemoteName: "origin",
		Auth:       auth,
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return Translate(err)
}

// Fetch updates remote-tracking refs without touching the working tree.
func (r *Repo) Fetch(ctx context.Context, auth transport.AuthMethod) error {
	err := r.repo.FetchContext(ctx, &git.FetchOptions{
		RemoteName: "origin",
		Auth:       auth,
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return Translate(err)
}

// AddAll stages every change in the working tree.
func (r *Repo) AddAll() error {
	wt, err := r.repo.Worktree()
	if err != nil {
		return Translate(err)
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return Translate(err)
	}
	return nil
}

// Commit creates a commit and returns its SHA.
func (r *Repo) Commit(message, authorName, authorEmail string) (string, error) {
	wt, err := r.repo.Worktree()
	if err != nil {
		return "", Translate(err)
	}
	hash, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  authorName,
			Email: authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", Translate(err)
	}
	return hash.String(), nil
}

// Push pushes the current branch to origin.
func (r *Repo) Push(ctx context.Context, auth transport.AuthMethod) error {
	err := r.repo.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
		Auth:       auth,
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return Translate(err)
}

// HasChanges reports whether the worktree has uncommitted changes.
func (r *Repo) HasChanges() (bool, error) {
	wt, err := r.repo.Worktree()
	if err != nil {
		return false, Translate(err)
	}
	st, err := wt.Status()
	if err != nil {
		return false, Translate(err)
	}
	return !st.IsClean(), nil
}

// MergeBase returns the SHA of the merge-base of two revisions, or "" if none.
func (r *Repo) MergeBase(a, b string) (string, error) {
	ah := plumbing.NewHash(a)
	bh := plumbing.NewHash(b)
	ac, err := r.repo.CommitObject(ah)
	if err != nil {
		return "", Translate(err)
	}
	bc, err := r.repo.CommitObject(bh)
	if err != nil {
		return "", Translate(err)
	}
	bases, err := ac.MergeBase(bc)
	if err != nil {
		return "", Translate(err)
	}
	if len(bases) == 0 {
		return "", nil
	}
	return bases[0].Hash.String(), nil
}

// FilesAtCommit returns the file paths at the given commit SHA. Returns nil
// if commitSHA is empty or the commit isn't found.
func (r *Repo) FilesAtCommit(commitSHA string) ([]string, error) {
	if commitSHA == "" {
		return nil, nil
	}
	commit, err := r.repo.CommitObject(plumbing.NewHash(commitSHA))
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return nil, nil
		}
		return nil, Translate(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, Translate(err)
	}
	var out []string
	err = tree.Files().ForEach(func(f *object.File) error {
		out = append(out, f.Name)
		return nil
	})
	if err != nil {
		return nil, Translate(err)
	}
	return out, nil
}

// Log returns up to n most recent commit SHAs and subjects, newest first.
func (r *Repo) Log(n int) ([]LogEntry, error) {
	ref, err := r.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil, nil
		}
		return nil, Translate(err)
	}
	iter, err := r.repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return nil, Translate(err)
	}
	out := make([]LogEntry, 0, n)
	err = iter.ForEach(func(c *object.Commit) error {
		if len(out) >= n {
			return storerStop
		}
		out = append(out, LogEntry{
			SHA:     c.Hash.String(),
			Message: c.Message,
			Author:  c.Author.Name,
			Email:   c.Author.Email,
			When:    c.Author.When,
		})
		return nil
	})
	if err != nil && !errors.Is(err, storerStop) {
		return nil, Translate(err)
	}
	return out, nil
}

// LogEntry is one row in the commit history.
type LogEntry struct {
	SHA     string
	Message string
	Author  string
	Email   string
	When    time.Time
}

// storerStop is a sentinel used to break out of iter.ForEach early.
var storerStop = errors.New("gitx: stop iteration")

// BlameLine is one line of a path's blame output: text, plus the commit and
// author that last touched it.
type BlameLine struct {
	LineNo     int
	Text       string
	SHA        string
	AuthorName string
	AuthorMail string
	When       time.Time
}

// Blame returns per-line authorship for path at HEAD. Missing paths / empty
// repos return (nil, nil) so callers can distinguish "no info" from error.
func (r *Repo) Blame(path string) ([]BlameLine, error) {
	ref, err := r.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil, nil
		}
		return nil, Translate(err)
	}
	commit, err := r.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, Translate(err)
	}
	res, err := git.Blame(commit, path)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, nil
		}
		return nil, Translate(err)
	}
	out := make([]BlameLine, 0, len(res.Lines))
	for i, line := range res.Lines {
		out = append(out, BlameLine{
			LineNo:     i + 1,
			Text:       line.Text,
			SHA:        line.Hash.String(),
			AuthorName: line.AuthorName,
			AuthorMail: line.Author,
			When:       line.Date,
		})
	}
	return out, nil
}
