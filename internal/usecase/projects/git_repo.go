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

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/gitprovider"
)

// gitRemote is how to reach the repository: its provider's token (none for
// a public repository), and the dial guard of a member's fetches.
func (c *Projects) gitRemote(ctx context.Context, g domain.ProjectGit) (domain.GitRemote, error) {
	r := domain.GitRemote{URL: g.RepoURL, Branch: g.Branch, CheckAddr: repoAddrCheck(ctx, g)}
	if g.ProviderID == "" {
		return r, nil
	}
	p, token, err := c.gitProviderToken(ctx, g.ProviderID)
	if err != nil {
		return r, err
	}
	if err := checkProviderRepo(p, g.RepoURL); err != nil {
		return r, err
	}
	r.Username, r.Password = gitprovider.CloneUsername(p.Kind), token
	return r, nil
}

// gitAccessError explains a refused repository.
func gitAccessError(g domain.ProjectGit, op string, err error) error {
	if errors.Is(err, domain.ErrGitRefused) {
		if g.ProviderID == "" {
			return fmt.Errorf("%s %s: %w (a private repository? read it through a git provider)", op, g.RepoURL, err)
		}
		return fmt.Errorf("%s %s: %w (does the git provider have access to it?)", op, g.RepoURL, err)
	}
	return fmt.Errorf("%s %s: %w", op, g.RepoURL, err)
}

// gitHead returns the commit the branch points at, without fetching.
func (c *Projects) gitHead(ctx context.Context, g domain.ProjectGit) (string, error) {
	r, err := c.gitRemote(ctx, g)
	if err != nil {
		return "", err
	}
	head, err := c.git.Head(ctx, r)
	if err != nil {
		return "", gitAccessError(g, "list", err)
	}
	if head == "" {
		return "", fmt.Errorf("branch %s not found in %s", g.Branch, g.RepoURL)
	}
	return head, nil
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
	r, err := c.gitRemote(ctx, g)
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
		runtime.GC() // the clone's pooled buffers only go on the second cycle
		debug.FreeOSMemory()
	}()
	commit, err := c.git.Clone(ctx, dir, r)
	if err != nil {
		return gitAccessError(g, "clone", err)
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
	return fn(data, commit.Hash(), func(rel string) ([]domain.ServiceFile, error) {
		return commitFiles(commit, path.Dir(g.Path), rel, g.Branch)
	})
}

// commitFiles reads rel, relative to dir, from a commit: a file (Path "")
// or a directory's files, within a service's limits.
func commitFiles(commit domain.GitCommit, dir, rel, branch string) ([]domain.ServiceFile, error) {
	p := path.Clean(path.Join(dir, rel))
	if p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(rel) {
		return nil, fmt.Errorf("outside the repository")
	}
	read := func(f domain.GitFile, name string) (domain.ServiceFile, error) {
		if f.Mode == domain.GitSymlink || f.Mode == domain.GitSubmodule {
			return domain.ServiceFile{}, fmt.Errorf("%s is a symlink or submodule", path.Join(rel, name))
		}
		if f.Size > deploy.MaxServiceFileBytes {
			return domain.ServiceFile{}, fmt.Errorf("%s is larger than %d MB", path.Join(rel, name), deploy.MaxServiceFileBytes>>20)
		}
		content, err := f.Contents()
		mode := int64(0o644)
		if f.Mode == domain.GitExecutable {
			mode = 0o755
		}
		return domain.ServiceFile{Path: name, Content: content, Mode: mode}, err
	}
	if f, err := commit.File(p); err == nil {
		sf, err := read(f, "")
		return []domain.ServiceFile{sf}, err
	}
	var out []domain.ServiceFile
	total := int64(0)
	walked := false // an error before the first file: no such directory
	err := commit.Walk(p, func(f domain.GitFile) error {
		walked = true
		if total += f.Size; total > deploy.MaxServiceFileBytes || len(out) >= deploy.MaxServiceFiles {
			return fmt.Errorf("more than %d files or %d MB", deploy.MaxServiceFiles, deploy.MaxServiceFileBytes>>20)
		}
		sf, err := read(f, f.Name)
		out = append(out, sf)
		return err
	})
	if err != nil && !walked {
		return nil, fmt.Errorf("not found on %s", branch)
	}
	return out, err
}
