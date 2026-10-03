package core

import (
	"context"
	"errors"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestRetagImage(t *testing.T) {
	const dgst = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		image, tag, digest, want string
	}{
		{"nginx", "1.27", "", "nginx:1.27"},
		{"nginx:1.25", "1.27", "", "nginx:1.27"},
		{"ghcr.io/org/app:latest", "sha-abc123", "", "ghcr.io/org/app:sha-abc123"},
		{"localhost:5000/app:v1", "v2", "", "localhost:5000/app:v2"},
		{"ghcr.io/org/app:v1@" + dgst, "v2", "", "ghcr.io/org/app:v2"},
		{"ghcr.io/org/app:v1", "", dgst, "ghcr.io/org/app@" + dgst},
		{"ghcr.io/org/app", "v2", dgst, "ghcr.io/org/app:v2@" + dgst},
	} {
		got, err := retagImage(tc.image, tc.tag, tc.digest)
		if err != nil || got != tc.want {
			t.Errorf("retag(%q, %q, %q) = %q, %v; want %q", tc.image, tc.tag, tc.digest, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ image, tag, digest string }{
		{"nginx", "bad tag", ""},
		{"nginx", "-dash", ""},
		{"nginx", "", "sha256:short"},
		{"nginx", "", "md5:0123"},
		{"Not Valid", "v1", ""},
	} {
		if _, err := retagImage(tc.image, tc.tag, tc.digest); !errors.Is(err, ErrInvalid) {
			t.Errorf("retag(%q, %q, %q): want ErrInvalid, got %v", tc.image, tc.tag, tc.digest, err)
		}
	}
}

func TestDeployRejects(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	git, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, ServerID: p.ServerID, Name: "api", Kind: store.ServiceKindApp,
		Source: store.SourceGit, GitURL: "https://github.com/org/api.git", GitBranch: "main", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	img, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, ServerID: p.ServerID, Name: "web", Kind: store.ServiceKindApp,
		Source: store.SourceImage, Image: "nginx:1.25", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	db, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, ServerID: p.ServerID, Name: "db", Kind: store.ServiceKindPostgres,
		Source: store.SourceImage, Image: "postgres:17", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		id   string
		opts DeployOptions
	}{
		"tag on a git service": {git.ID, DeployOptions{Tag: "v2"}},
		"tag on a database":    {db.ID, DeployOptions{Tag: "18"}},
		"invalid tag":          {img.ID, DeployOptions{Tag: "a b"}},
		"invalid digest":       {img.ID, DeployOptions{Digest: "sha256:nope"}},
		"invalid commit":       {img.ID, DeployOptions{Tag: "v2", Commit: "main"}},
	} {
		if _, err := c.Deploy(ctx, tc.id, tc.opts); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if deps, _ := c.store.ListDeployments(ctx, img.ID, 0); len(deps) != 0 {
		t.Errorf("rejected deploys created %d deployments", len(deps))
	}

	if err := c.store.SetServiceImage(ctx, img.ID, "nginx:1.27"); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.store.GetService(ctx, img.ID); got.Image != "nginx:1.27" {
		t.Errorf("image = %q after SetServiceImage", got.Image)
	}
}

func TestPinDigest(t *testing.T) {
	const d1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	const d2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	for _, tc := range []struct {
		image   string
		digests []string
		want    string
	}{
		{"nginx:1.27", []string{"nginx@" + d1}, "nginx:1.27@" + d1},
		{"nginx", []string{"docker.io/library/nginx@" + d1}, "nginx:latest@" + d1},
		{"ghcr.io/org/app:v2", []string{"ghcr.io/org/other@" + d2, "ghcr.io/org/app@" + d1}, "ghcr.io/org/app:v2@" + d1},
		{"ghcr.io/org/app:v2@" + d2, []string{"ghcr.io/org/app@" + d1}, "ghcr.io/org/app:v2@" + d2},
		{"ghcr.io/org/app:v2", nil, "ghcr.io/org/app:v2"},
		{"ghcr.io/org/app:v2", []string{"ghcr.io/org/other@" + d1}, "ghcr.io/org/app:v2"},
	} {
		if got := pinDigest(tc.image, tc.digests); got != tc.want {
			t.Errorf("pinDigest(%q, %v) = %q, want %q", tc.image, tc.digests, got, tc.want)
		}
	}
}
