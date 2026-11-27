// Package git is domain.Git with go-git: it reads repositories without a git
// binary.
package git

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// Git implements domain.Git.
type Git struct{}

func (Git) Head(ctx context.Context, r domain.GitRemote) (string, error) {
	rem := gogit.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "origin", URLs: []string{r.URL}})
	refs, err := rem.ListContext(withCheck(ctx, r), &gogit.ListOptions{Auth: auth(r)})
	if err != nil {
		return "", refused(err)
	}
	want := plumbing.NewBranchReferenceName(r.Branch)
	for _, ref := range refs {
		if ref.Name() == want {
			return ref.Hash().String(), nil
		}
	}
	return "", nil
}

func (Git) Clone(ctx context.Context, dir string, r domain.GitRemote) (domain.GitCommit, error) {
	repo, err := gogit.PlainCloneContext(withCheck(ctx, r), dir, true, &gogit.CloneOptions{
		URL:           r.URL,
		Auth:          auth(r),
		ReferenceName: plumbing.NewBranchReferenceName(r.Branch),
		SingleBranch:  true,
		Depth:         1,
		NoCheckout:    true,
		Tags:          gogit.NoTags,
	})
	if err != nil {
		return nil, refused(err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, err
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, err
	}
	return commit{c}, nil
}

func auth(r domain.GitRemote) transport.AuthMethod {
	if r.Username == "" && r.Password == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: r.Username, Password: r.Password}
}

// refusedError keeps go-git's message and matches domain.ErrGitRefused.
type refusedError struct{ error }

func (e refusedError) Unwrap() error      { return e.error }
func (refusedError) Is(target error) bool { return target == domain.ErrGitRefused }

func refused(err error) error {
	if errors.Is(err, transport.ErrAuthenticationRequired) || errors.Is(err, transport.ErrRepositoryNotFound) ||
		errors.Is(err, transport.ErrAuthorizationFailed) {
		return refusedError{err}
	}
	return err
}

type commit struct{ c *object.Commit }

func (c commit) Hash() string { return c.c.Hash.String() }

func (c commit) File(path string) (domain.GitFile, error) {
	f, err := c.c.File(path)
	if err != nil {
		return domain.GitFile{}, err
	}
	return file(f), nil
}

func (c commit) Walk(dir string, fn func(domain.GitFile) error) error {
	tree, err := c.c.Tree()
	if err == nil && dir != "." {
		tree, err = tree.Tree(dir)
	}
	if err != nil {
		return err
	}
	return tree.Files().ForEach(func(f *object.File) error { return fn(file(f)) })
}

func file(f *object.File) domain.GitFile {
	mode := domain.GitRegular
	switch f.Mode {
	case filemode.Executable:
		mode = domain.GitExecutable
	case filemode.Symlink:
		mode = domain.GitSymlink
	case filemode.Submodule:
		mode = domain.GitSubmodule
	}
	return domain.GitFile{Name: f.Name, Size: f.Size, Mode: mode, Contents: func() ([]byte, error) {
		s, err := f.Contents()
		return []byte(s), err
	}}
}

type checkKey struct{}

// withCheck has the fetches of ctx call r.CheckAddr as they dial: the
// address connected to can differ from the one the name had when linking
// (a redirect, a name resolving again).
func withCheck(ctx context.Context, r domain.GitRemote) context.Context {
	if r.CheckAddr == nil {
		return ctx
	}
	return context.WithValue(ctx, checkKey{}, r.CheckAddr)
}

func dialControl(ctx context.Context, _, addr string, _ syscall.RawConn) error {
	check, _ := ctx.Value(checkKey{}).(func(netip.Addr) error)
	if check == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	return check(ip)
}

func init() {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, ControlContext: dialControl}).DialContext
	t := githttp.NewClient(&http.Client{Transport: tr})
	client.InstallProtocol("http", t)
	client.InstallProtocol("https", t)
}
