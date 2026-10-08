package core

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestRenameDatabaseRefs(t *testing.T) {
	env := map[string]string{
		"URL":   "{{ db.main.URL }}",
		"DSN":   "host={{db.main.HOST}} port={{ db.main.PORT }}",
		"OTHER": "{{ db.mainx.URL }}",
		"VAR":   "{{ project.main }}",
	}
	want := map[string]string{
		"URL":   "{{ db.pg.URL }}",
		"DSN":   "host={{ db.pg.HOST }} port={{ db.pg.PORT }}",
		"OTHER": "{{ db.mainx.URL }}",
		"VAR":   "{{ project.main }}",
	}
	if got := renameDatabaseRefs(env, "main", "pg"); !maps.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestRenameRefused(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.CreateProject(ctx, store.Project{Name: "blog"}); err != nil {
		t.Fatal(err)
	}
	web, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "api", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.RenameProject(ctx, p.ID, "Bad Name"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad project name: got %v, want ErrInvalid", err)
	}
	if _, err := c.RenameProject(ctx, p.ID, "blog"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("taken project name: got %v, want ErrConflict", err)
	}
	if _, err := c.RenameService(ctx, web.ID, "-web"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad service name: got %v, want ErrInvalid", err)
	}
	if _, err := c.RenameService(ctx, web.ID, "api"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("taken service name: got %v, want ErrConflict", err)
	}
	if _, err := c.store.SaveProjectGit(ctx, store.ProjectGit{ProjectID: p.ID, RepoURL: "https://github.com/me/shop.git", Branch: "main", Path: "compose.yaml"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RenameService(ctx, web.ID, "front"); !errors.Is(err, ErrGitManaged) {
		t.Errorf("git-managed project: got %v, want ErrGitManaged", err)
	}
	if got, _ := c.store.GetService(ctx, web.ID); got.Name != "web" {
		t.Errorf("name after refusals = %q", got.Name)
	}
}

// TestRenameDocker renames a service and its project with a running replica
// on the local Docker daemon. It only touches what it creates.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run RenameDocker
func TestRenameDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	c.pool = docker.NewPool(dk, c.connectServer)

	p, err := c.store.CreateProject(ctx, store.Project{Name: "renametest", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	db, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "main", Kind: store.ServiceKindApp, Image: "busybox:stable", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Not kipitiny.managed: a manager running on this daemon would remove
	// them as orphans.
	netName := docker.ProjectNetwork(p.ID)
	if _, err := dk.NetworkCreate(ctx, netName, client.NetworkCreateOptions{Driver: "bridge"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveNetwork(context.Background(), netName) })
	if err := dk.EnsureImage(ctx, "busybox:stable", ""); err != nil {
		t.Fatal(err)
	}
	id, err := dk.Run(ctx, client.ContainerCreateOptions{
		Name: "renametest-main-1-abcdef",
		Config: &container.Config{
			Image:  "busybox:stable",
			Cmd:    []string{"sleep", "3600"},
			Labels: map[string]string{docker.LabelProject: p.ID, docker.LabelService: db.ID},
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {Aliases: []string{"main"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainer(context.Background(), id, 0) })

	if _, err := c.RenameService(ctx, db.ID, "pg"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RenameProject(ctx, p.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	res, err := dk.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if name := strings.TrimPrefix(res.Container.Name, "/"); name != "renamed-pg-1-abcdef" {
		t.Errorf("container name = %q", name)
	}
	ep := res.Container.NetworkSettings.Networks[netName]
	if ep == nil || !slices.Contains(ep.Aliases, "pg") || !slices.Contains(ep.Aliases, "main") {
		t.Errorf("aliases = %+v, want pg and main", ep)
	}
	if res.Container.State.Status != container.StateRunning {
		t.Errorf("state = %s, want it still running", res.Container.State.Status)
	}
}
