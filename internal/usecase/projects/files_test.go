package projects

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestGitRepoFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"deploy/compose.yaml": "services: {web: {image: nginx}}\n", "deploy/nginx.conf": "server {}", "deploy/conf/a.yml": "a: 1",
		"deploy/conf/run.sh": "#!/bin/sh", "secret.txt": "top",
	} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		mode := os.FileMode(0o644)
		if filepath.Ext(name) == ".sh" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "deploy", "link")); err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("files", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}

	c := New(ctx, &config.Config{DataDir: t.TempDir()}, nil, nil, nil, nil)
	g := domain.ProjectGit{RepoURL: dir, Branch: "main", Path: "deploy/compose.yaml"}
	err = c.WithGitFile(ctx, g, func(_ []byte, _ string, files repoFiles) error {
		f, err := files("./nginx.conf")
		if err != nil || len(f) != 1 || f[0].Path != "" || string(f[0].Content) != "server {}" {
			t.Errorf("file = %+v, %v", f, err)
		}
		d, err := files("./conf")
		if err != nil || len(d) != 2 || d[0].Path != "a.yml" || d[1].Path != "run.sh" || d[1].Mode != 0o755 {
			t.Errorf("dir = %+v, %v", d, err)
		}
		if f, err := files("../secret.txt"); err != nil || string(f[0].Content) != "top" {
			t.Errorf("parent dir inside the repository = %+v, %v", f, err)
		}
		for _, bad := range []string{"../../etc/passwd", "/etc/passwd", "./link", "./missing"} {
			if _, err := files(bad); err == nil {
				t.Errorf("%s: read", bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
