package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
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

	c := New(config.Config{DataDir: t.TempDir()}, nil, nil, nil)
	g := store.ProjectGit{RepoURL: dir, Branch: "main", Path: "deploy/compose.yaml"}
	err = c.withGitFile(ctx, g, func(data []byte, _ string, files repoFiles) error {
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

func TestServiceFiles(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID, Env: map[string]string{"TOKEN": "t0k"}})
	if err != nil {
		t.Fatal(err)
	}
	src := `
services:
  web:
    image: nginx
    configs: [{source: site, target: /etc/nginx/conf.d/site.conf, mode: 0640}]
    secrets: [token]
configs:
  site: {content: "listen 80; # $$host"}
secrets:
  token: {environment: TOKEN}
`
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	web := servicesByName(t, c, p.ID)["web"]
	if len(web.Files) != 2 || string(web.Files[0].Content) != "listen 80; # $host" || web.Files[1].Path != "/run/secrets/token" ||
		string(web.Files[1].Content) != "{{ project.TOKEN }}" || web.Files[0].Size != 18 {
		t.Fatalf("files = %+v", web.Files)
	}
	got := serviceFiles(web, envSources{project: p.Env})
	if string(got[1].Content) != "t0k" || got[0].Mode != 0o640 {
		t.Errorf("container files = %+v", got)
	}

	// API responses leave the content out; sent back like that, it's kept.
	view := masked(web)
	if view.Files[0].Content != nil || view.Files[0].Size != 18 || web.Files[0].Content == nil {
		t.Fatalf("view files = %+v", view.Files)
	}
	if _, err := c.updateService(ctx, web.ID, ServicePatch{Files: view.Files}); err != nil {
		t.Fatal(err)
	}
	if again := servicesByName(t, c, p.ID)["web"]; !sameFiles(again.Files, web.Files) {
		t.Errorf("files after a round trip through the API = %+v", again.Files)
	}

	out, err := c.ExportCompose(ctx, p.ID, ExportOptions{Secrets: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.ApplyCompose(ctx, p.ID, out.Compose, ApplyOptions{DryRun: true})
	if err != nil || !slices.Equal(plan.Unchanged, []string{"web"}) {
		t.Errorf("own export re-applied: %+v, %v\n%s", plan, err, out.Compose)
	}

	for name, s := range map[string]store.Service{
		"read-only root": {Runtime: store.Runtime{ReadOnly: true}, Files: []store.ServiceFile{{Path: "/a"}}},
		"in a volume":    {Volumes: []store.Volume{{Name: "d", Path: "/data"}}, Files: []store.ServiceFile{{Path: "/data/x"}}},
		"relative":       {Files: []store.ServiceFile{{Path: "etc/x"}}},
		"twice":          {Files: []store.ServiceFile{{Path: "/a"}, {Path: "/a"}}},
	} {
		s.Kind, s.Image, s.Replicas = store.ServiceKindApp, "x", 1
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
