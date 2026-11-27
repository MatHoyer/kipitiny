package domain

import (
	"context"
	"errors"
	"net/netip"
)

// Git reads a branch of a remote repository (over http(s), or a local path).
// Errors are the implementation's, unwrapped, except that a refused
// repository also matches ErrGitRefused.
type Git interface {
	// Head is the commit the branch points at, without fetching; empty
	// when there is no such branch.
	Head(ctx context.Context, r GitRemote) (string, error)
	// Clone shallow-clones the branch into dir, an empty directory, and
	// returns its head commit, readable while dir exists.
	Clone(ctx context.Context, dir string, r GitRemote) (GitCommit, error)
}

// ErrGitRefused is a repository that asked for credentials, refused them or
// doesn't exist (forges answer the same for a private one).
var ErrGitRefused = errors.New("repository refused")

// GitRemote is a branch of a repository and how to reach it.
type GitRemote struct {
	URL    string
	Branch string
	// Username and Password authenticate over http(s); empty for a public
	// repository.
	Username, Password string
	// CheckAddr, when set, is called with each address a fetch connects to;
	// its error refuses the connection.
	CheckAddr func(netip.Addr) error
}

// GitCommit is a cloned commit.
type GitCommit interface {
	Hash() string
	// File is the file at path (slash-separated, from the repository's
	// root); an error when there is none, or it is a directory.
	File(path string) (GitFile, error)
	// Walk calls fn with each file under the directory dir ("." is the
	// root), its Name relative to dir; an error when dir isn't one.
	Walk(dir string, fn func(GitFile) error) error
}

type GitFileMode int

const (
	GitRegular GitFileMode = iota
	GitExecutable
	GitSymlink
	GitSubmodule
)

// GitFile is a file of a commit; Contents reads it.
type GitFile struct {
	Name     string
	Size     int64
	Mode     GitFileMode
	Contents func() ([]byte, error)
}
