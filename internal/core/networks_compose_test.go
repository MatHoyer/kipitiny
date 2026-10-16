package core

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestComposeNetworks(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := c.store.CreateNetwork(ctx, store.Network{ServerID: store.LocalServerID, Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	src := `
services:
  web:
    image: a
    networks: [front, shared]
  api:
    image: a
    networks: [front, back]
  db:
    image: postgres:16
    networks: [back]
  plain:
    image: a
networks:
  front:
  back:
  shared: {external: true}
`
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	by := map[string]store.Service{}
	for _, s := range svcs {
		by[s.Name] = s
	}
	if w := by["web"]; !slices.Equal(w.ProjectNetworks, []string{"front"}) || !slices.Equal(w.Networks, []string{shared.ID}) {
		t.Errorf("web = %v %v", w.ProjectNetworks, w.Networks)
	}
	spec := containerSpec(p, by["api"], envSources{}, "D1", 1, route{})
	if len(spec.NetworkingConfig.EndpointsConfig) != 2 || spec.NetworkingConfig.EndpointsConfig[docker.ProjectNetworkNamed(p.ID, "back")] == nil ||
		spec.NetworkingConfig.EndpointsConfig[docker.ProjectNetwork(p.ID)] != nil {
		t.Errorf("api endpoints = %v", spec.NetworkingConfig.EndpointsConfig)
	}
	if spec := containerSpec(p, by["plain"], envSources{}, "D1", 1, route{}); spec.NetworkingConfig.EndpointsConfig[docker.ProjectNetwork(p.ID)] == nil {
		t.Errorf("plain endpoints = %v", spec.NetworkingConfig.EndpointsConfig)
	}

	exp, err := c.ExportCompose(ctx, p.ID, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"shared:\n    external: true", "- front"} {
		if !strings.Contains(string(exp.Compose), want) {
			t.Errorf("export lacks %q:\n%s", want, exp.Compose)
		}
	}
	plan, err := c.ApplyCompose(ctx, p.ID, exp.Compose, ApplyOptions{DryRun: true})
	if err != nil || len(plan.Update) > 0 {
		t.Errorf("re-import: %v, updates %+v", err, plan.Update)
	}

	bad := strings.Replace(src, "shared: {external: true}", "shared: {external: true, name: nope}", 1)
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(bad), ApplyOptions{DryRun: true}); err == nil || !strings.Contains(err.Error(), "network nope doesn't exist") {
		t.Errorf("unknown external network: %v", err)
	}
}
