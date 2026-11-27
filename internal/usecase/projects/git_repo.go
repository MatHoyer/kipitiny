package projects

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/gitprovider"
)

// gitAuth is the credential reading the repository: its provider's token
// (none for a public repository).
func (c *Projects) gitAuth(ctx context.Context, g domain.ProjectGit) (transport.AuthMethod, error) {
	if g.ProviderID == "" {
		return nil, nil
	}
	p, token, err := c.gitProviderToken(ctx, g.ProviderID)
	if err != nil {
		return nil, err
	}
	if err := checkProviderRepo(p, g.RepoURL); err != nil {
		return nil, err
	}
	return &githttp.BasicAuth{Username: gitprovider.CloneUsername(p.Kind), Password: token}, nil
}

// gitAccessError explains a refused repository.
func gitAccessError(g domain.ProjectGit, op string, err error) error {
	if errors.Is(err, transport.ErrAuthenticationRequired) || errors.Is(err, transport.ErrRepositoryNotFound) ||
		errors.Is(err, transport.ErrAuthorizationFailed) {
		if g.ProviderID == "" {
			return fmt.Errorf("%s %s: %w (a private repository? read it through a git provider)", op, g.RepoURL, err)
		}
		return fmt.Errorf("%s %s: %w (does the git provider have access to it?)", op, g.RepoURL, err)
	}
	return fmt.Errorf("%s %s: %w", op, g.RepoURL, err)
}

// gitHead returns the commit the branch points at, without fetching.
func (c *Projects) gitHead(ctx context.Context, g domain.ProjectGit) (string, error) {
	auth, err := c.gitAuth(ctx, g)
	if err != nil {
		return "", err
	}
	rem := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "origin", URLs: []string{g.RepoURL}})
	refs, err := rem.ListContext(guardRepo(ctx, g), &git.ListOptions{Auth: auth})
	if err != nil {
		return "", gitAccessError(g, "list", err)
	}
	want := plumbing.NewBranchReferenceName(g.Branch)
	for _, r := range refs {
		if r.Name() == want {
			return r.Hash().String(), nil
		}
	}
	return "", fmt.Errorf("branch %s not found in %s", g.Branch, g.RepoURL)
}

// gitReadFile reads the compose file of the branch: its content and the
// commit.
func (c *Projects) gitReadFile(ctx context.Context, g domain.ProjectGit) ([]byte, string, error) {
	var data []byte
	var commit string
	err := c.WithGitFile(ctx, g, func(d []byte, cm string, _ repoFiles) error {
		data, commit = d, cm
		return nil
	})
	return data, commit, err
}

// repoFiles reads a path of the repository relative to the compose file
// (compose.Vars.Files).
type repoFiles = func(rel string) ([]domain.ServiceFile, error)

// WithGitFile shallow-clones the branch into a temporary directory (on
// disk: memory stays flat whatever the repository's size) and calls fn with
// the compose file, the commit, and a reader of the repository's other
// files, valid until fn returns.
//
// Unpacking holds the whole commit's files in memory for a moment. Go would
// keep that memory for minutes before returning it, which shows as the
// manager's usage: syncs are rare, so it goes back to the OS right away.
func (c *Projects) WithGitFile(ctx context.Context, g domain.ProjectGit, fn func(data []byte, commit string, files repoFiles) error) error {
	auth, err := c.gitAuth(ctx, g)
	if err != nil {
		return err
	}
	base := filepath.Join(c.cfg.DataDir, "git")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(base, "clone-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	defer func() {
		runtime.GC() // go-git's pooled buffers only go on the second cycle
		debug.FreeOSMemory()
	}()
	repo, err := git.PlainCloneContext(guardRepo(ctx, g), dir, true, &git.CloneOptions{
		URL:           g.RepoURL,
		Auth:          auth,
		ReferenceName: plumbing.NewBranchReferenceName(g.Branch),
		SingleBranch:  true,
		Depth:         1,
		NoCheckout:    true,
		Tags:          git.NoTags,
	})
	if err != nil {
		return gitAccessError(g, "clone", err)
	}
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("clone %s: %w", g.RepoURL, err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return err
	}
	f, err := commit.File(g.Path)
	if err != nil {
		return fmt.Errorf("%s not found on %s: %w", g.Path, g.Branch, err)
	}
	if f.Size > maxComposeBytes {
		return fmt.Errorf("%s is larger than %d bytes", g.Path, maxComposeBytes)
	}
	data, err := f.Contents()
	if err != nil {
		return err
	}
	return fn([]byte(data), head.Hash().String(), func(rel string) ([]domain.ServiceFile, error) {
		return commitFiles(commit, path.Dir(g.Path), rel, g.Branch)
	})
}

// commitFiles reads rel, relative to dir, from a commit: a file (Path "")
// or a directory's files, within a service's limits.
func commitFiles(commit *object.Commit, dir, rel, branch string) ([]domain.ServiceFile, error) {
	p := path.Clean(path.Join(dir, rel))
	if p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(rel) {
		return nil, fmt.Errorf("outside the repository")
	}
	read := func(f *object.File, name string) (domain.ServiceFile, error) {
		if f.Mode == filemode.Symlink || f.Mode == filemode.Submodule {
			return domain.ServiceFile{}, fmt.Errorf("%s is a symlink or submodule", path.Join(rel, name))
		}
		if f.Size > deploy.MaxServiceFileBytes {
			return domain.ServiceFile{}, fmt.Errorf("%s is larger than %d MB", path.Join(rel, name), deploy.MaxServiceFileBytes>>20)
		}
		content, err := f.Contents()
		mode := int64(0o644)
		if f.Mode == filemode.Executable {
			mode = 0o755
		}
		return domain.ServiceFile{Path: name, Content: []byte(content), Mode: mode}, err
	}
	if f, err := commit.File(p); err == nil {
		sf, err := read(f, "")
		return []domain.ServiceFile{sf}, err
	}
	tree, err := commit.Tree()
	if err == nil && p != "." {
		tree, err = tree.Tree(p)
	}
	if err != nil {
		return nil, fmt.Errorf("not found on %s", branch)
	}
	var out []domain.ServiceFile
	total := int64(0)
	err = tree.Files().ForEach(func(f *object.File) error {
		if total += f.Size; total > deploy.MaxServiceFileBytes || len(out) >= deploy.MaxServiceFiles {
			return fmt.Errorf("more than %d files or %d MB", deploy.MaxServiceFiles, deploy.MaxServiceFileBytes>>20)
		}
		sf, err := read(f, f.Name)
		out = append(out, sf)
		return err
	})
	return out, err
}
