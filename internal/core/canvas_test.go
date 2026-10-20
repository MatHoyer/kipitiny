package core

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestCanvasLayout(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}

	in := map[string]Point{"project:" + p.ID: {X: 10.4, Y: -20}, "svc:" + svc.ID: {X: 5, Y: 6}, "traefik:local": {X: 1, Y: 2}, "in:" + p.ID + "/traefik:local": {X: 7, Y: 8}}
	if err := c.SaveCanvasLayout(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := c.CanvasLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got["project:"+p.ID] != (Point{X: 10, Y: -20}) || got["in:"+p.ID+"/traefik:local"] != (Point{X: 7, Y: 8}) {
		t.Fatalf("layout = %v", got)
	}

	// A member sees no position of a project they can't see, nor of its map.
	if got, err := c.CanvasLayout(WithActor(ctx, Actor{Kind: "user", Name: "alice", Role: store.RoleMember})); err != nil || len(got) != 1 {
		t.Errorf("member's layout = %v, %v", got, err)
	}

	for name, bad := range map[string]map[string]Point{
		"unknown node": {"svc:nope": {}},
		"bad kind":     {"thing:local": {}},
		"view service": {"in:" + p.ID + "/svc:" + svc.ID: {}},
		"view project": {"in:nope/traefik:local": {}},
		"view nested":  {"in:" + p.ID + "/in:" + p.ID + "/traefik:local": {}},
		"NaN":          {"svc:" + svc.ID: {X: math.NaN()}},
		"far away":     {"svc:" + svc.ID: {X: 2e6}},
	} {
		if err := c.SaveCanvasLayout(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}

	// A deleted service's position is no longer returned, and is dropped
	// on the next save.
	if err := c.store.DeleteService(ctx, svc.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.CanvasLayout(ctx); len(got) != 3 {
		t.Fatalf("after delete = %v", got)
	}
	if err := c.SaveCanvasLayout(ctx, map[string]Point{"traefik:local": {X: 3, Y: 4}}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := c.store.ListCanvasPositions(ctx); len(ps) != 3 {
		t.Fatalf("stale position kept: %v", ps)
	}

	if err := c.ResetCanvasLayout(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.CanvasLayout(ctx); len(got) != 0 {
		t.Fatalf("after reset = %v", got)
	}
}
