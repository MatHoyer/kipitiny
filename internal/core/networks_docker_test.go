package core

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// TestNetworksDocker joins, renames on and leaves a network created by hand
// with a container of the local Docker daemon.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run NetworksDocker
func TestNetworksDocker(t *testing.T) {
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
	p, err := c.store.CreateProject(ctx, store.Project{Name: "nettest", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "busybox", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := dk.EnsureImage(ctx, "busybox", ""); err != nil {
		t.Fatal(err)
	}
	// Not kipitiny.managed: a manager running on this daemon would remove
	// it as an orphan.
	id, err := dk.Run(ctx, client.ContainerCreateOptions{
		Name:   "kipitiny-test-" + strings.ToLower(svc.ID),
		Config: &container.Config{Image: "busybox", Cmd: []string{"sleep", "300"}, Labels: map[string]string{docker.LabelProject: p.ID, docker.LabelService: svc.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainer(context.Background(), id, 0) })

	n, err := c.CreateNetwork(ctx, NetworkInput{Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveNetwork(context.Background(), n.DockerName) })

	aliases := func() ([]string, bool) {
		t.Helper()
		res, err := dk.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ep := res.Container.NetworkSettings.Networks[n.DockerName]
		if ep == nil {
			return nil, false
		}
		return ep.Aliases, true
	}

	if _, err := c.SetServiceNetworks(ctx, svc.ID, []string{n.ID}); err != nil {
		t.Fatal(err)
	}
	if a, ok := aliases(); !ok || !slices.Contains(a, "nettest-web") {
		t.Fatalf("after join: attached %v, aliases %v", ok, a)
	}
	nets, err := c.ListNetworks(ctx)
	if err != nil || len(nets) != 1 || len(nets[0].Services) != 1 || nets[0].Services[0].Alias != "nettest-web" {
		t.Fatalf("list = %+v, %v", nets, err)
	}

	if _, err := c.RenameService(ctx, svc.ID, "api"); err != nil {
		t.Fatal(err)
	}
	if a, ok := aliases(); !ok || !slices.Contains(a, "nettest-api") || slices.Contains(a, "nettest-web") {
		t.Fatalf("after rename: attached %v, aliases %v", ok, a)
	}

	if _, err := c.SetServiceNetworks(ctx, svc.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := aliases(); ok {
		t.Fatal("still attached after leaving")
	}

	if _, err := c.SetServiceNetworks(ctx, svc.ID, []string{n.ID}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteNetwork(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dk.NetworkInspect(ctx, n.DockerName, client.NetworkInspectOptions{}); err == nil {
		t.Fatal("network still on the server")
	}
	if got, _ := c.store.GetService(ctx, svc.ID); len(got.Networks) != 0 {
		t.Fatalf("service still lists %v", got.Networks)
	}
}
