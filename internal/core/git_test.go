package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestWebhookAuthorized(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	for name, tc := range map[string]struct {
		header http.Header
		want   bool
	}{
		"github":       {http.Header{"X-Hub-Signature-256": {sig}}, true},
		"github wrong": {http.Header{"X-Hub-Signature-256": {"sha256=00"}}, false},
		"gitlab":       {http.Header{"X-Gitlab-Token": {"s3cret"}}, true},
		"bearer":       {http.Header{"Authorization": {"Bearer s3cret"}}, true},
		"bearer wrong": {http.Header{"Authorization": {"Bearer nope"}}, false},
		"none":         {http.Header{}, false},
	} {
		if got := webhookAuthorized("s3cret", tc.header.Get, body); got != tc.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestValidateGit(t *testing.T) {
	ok := store.ProjectGit{RepoURL: "https://github.com/me/infra.git", Branch: "main", Path: "compose.yaml", PollSeconds: 300}
	for name, mutate := range map[string]func(*store.ProjectGit){
		"ssh url":        func(g *store.ProjectGit) { g.RepoURL = "git@github.com:me/infra.git" },
		"creds in url":   func(g *store.ProjectGit) { g.RepoURL = "https://u:p@github.com/me/infra.git" },
		"bad branch":     func(g *store.ProjectGit) { g.Branch = "a..b" },
		"poll too often": func(g *store.ProjectGit) { g.PollSeconds = 5 },
	} {
		g := ok
		mutate(&g)
		if err := validateGit(g); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := validateGit(ok); err != nil {
		t.Error(err)
	}
}

// localRepo makes a repository with compose.yaml on main; commit adds a
// new version.
func localRepo(t *testing.T) (dir string, commit func(content string) string) {
	dir = t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	return dir, func(content string) string {
		if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "deploy", "compose.yaml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("deploy/compose.yaml"); err != nil {
			t.Fatal(err)
		}
		h, err := wt.Commit("update", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}})
		if err != nil {
			t.Fatal(err)
		}
		return h.String()
	}
}

func TestGitFetch(t *testing.T) {
	ctx := context.Background()
	dir, commit := localRepo(t)
	sha := commit("services: {web: {image: nginx}}\n")
	c := New(config.Config{DataDir: t.TempDir()}, nil, nil, nil)
	g := store.ProjectGit{RepoURL: dir, Branch: "main", Path: "deploy/compose.yaml"}
	head, err := c.gitHead(ctx, g)
	if err != nil || head != sha {
		t.Fatalf("head = %q, %v; want %s", head, err, sha)
	}
	data, got, err := c.gitReadFile(ctx, g)
	if err != nil || got != sha || string(data) != "services: {web: {image: nginx}}\n" {
		t.Fatalf("read = %q %q %v", data, got, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(c.cfg.DataDir, "git")); len(entries) != 0 {
		t.Errorf("clone left behind: %v", entries)
	}
	g.Path = "missing.yaml"
	if _, _, err := c.gitReadFile(ctx, g); err == nil {
		t.Error("want an error for a missing file")
	}
	g.Branch = "nope"
	if _, err := c.gitHead(ctx, g); err == nil {
		t.Error("want an error for a missing branch")
	}
}

func TestGitApply(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.SaveProjectGit(ctx, store.ProjectGit{ProjectID: p.ID, RepoURL: "https://example.com/r.git", Branch: "main",
		Path: "compose.yaml", PollSeconds: 300, WebhookSecret: "w"}); err != nil {
		t.Fatal(err)
	}
	gitCtx := fromGitSync(ctx)
	v1 := "services:\n  web: {image: 'ghcr.io/me/web:1'}\n  api: {image: 'ghcr.io/me/api:1'}\n  db: {image: 'postgres:17-alpine'}\n"
	if _, err := c.applyCompose(gitCtx, p.ID, []byte(v1), ApplyOptions{}, true); err != nil {
		t.Fatal(err)
	}
	svcs := servicesByName(t, c, p.ID)

	// Edits outside the sync are refused; ApplyCompose too.
	img := "nginx"
	if _, err := c.UpdateService(ctx, svcs["web"].ID, ServicePatch{Image: &img}); !errors.Is(err, ErrGitManaged) {
		t.Errorf("update: %v", err)
	}
	if _, err := c.CreateService(ctx, p.ID, ServiceInput{Name: "x", Image: "nginx"}); !errors.Is(err, ErrGitManaged) {
		t.Errorf("create: %v", err)
	}
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(v1), ApplyOptions{}); !errors.Is(err, ErrGitManaged) {
		t.Errorf("apply: %v", err)
	}

	// CI deploys web:2 (a tag override): a sync where web is unchanged in
	// the file keeps it, a change to web in the file wins.
	if err := c.store.SetServiceImage(ctx, svcs["web"].ID, "ghcr.io/me/web:2"); err != nil {
		t.Fatal(err)
	}
	v2 := "services:\n  web: {image: 'ghcr.io/me/web:1'}\n  api: {image: 'ghcr.io/me/api:3'}\n"
	a, err := c.applyCompose(gitCtx, p.ID, []byte(v2), ApplyOptions{Prune: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	svcs = servicesByName(t, c, p.ID)
	if svcs["web"].Image != "ghcr.io/me/web:2" || svcs["api"].Image != "ghcr.io/me/api:3" {
		t.Errorf("images: web %s, api %s", svcs["web"].Image, svcs["api"].Image)
	}
	if !svcs["db"].Orphaned || len(a.plan.Orphaned) != 1 {
		t.Errorf("db not orphaned: %+v", a.plan)
	}
	g, _ := c.store.GetProjectGit(ctx, p.ID)
	g.Applied = a.images
	if err := c.store.SetProjectGitSync(ctx, g); err != nil {
		t.Fatal(err)
	}
	if st, err := c.GetProjectGit(ctx, p.ID); err != nil || len(st.Drift) != 1 || st.Drift[0] != "web" {
		t.Errorf("status = %+v, %v", st, err)
	}

	v3 := "services:\n  web: {image: 'ghcr.io/me/web:4'}\n  api: {image: 'ghcr.io/me/api:3'}\n"
	if _, err := c.applyCompose(gitCtx, p.ID, []byte(v3), ApplyOptions{Prune: true}, true); err != nil {
		t.Fatal(err)
	}
	if got := servicesByName(t, c, p.ID)["web"].Image; got != "ghcr.io/me/web:4" {
		t.Errorf("web image = %s, want the file's", got)
	}
}

func servicesByName(t *testing.T, c *Core, projectID string) map[string]store.Service {
	t.Helper()
	svcs, err := c.store.ListServices(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.Service{}
	for _, s := range svcs {
		out[s.Name] = s
	}
	return out
}
