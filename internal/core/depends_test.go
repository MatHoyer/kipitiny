package core

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestStartOrder(t *testing.T) {
	app := func(name string, deps ...string) store.Service {
		s := store.Service{Name: name, Kind: store.ServiceKindApp}
		for _, d := range deps {
			s.DependsOn = append(s.DependsOn, store.Dependency{Service: d})
		}
		return s
	}
	svcs := []store.Service{app("web", "api", "migrate"), app("api", "db"), app("migrate", "db"),
		{Name: "db", Kind: store.ServiceKindPostgres}, app("loop-a", "loop-b"), app("loop-b", "loop-a"), app("orphan", "gone")}
	var names []string
	for _, s := range startOrder(svcs) {
		names = append(names, s.Name)
	}
	if want := []string{"db", "api", "migrate", "web", "loop-b", "loop-a", "orphan"}; !slices.Equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}

	svcs[0].DependsOn[1].Condition = "completed"
	svcs[1].DependsOn = append(svcs[1].DependsOn, store.Dependency{Service: "migrate", Condition: "healthy"})
	got := awaited(svcs)
	if got["db"] != "healthy" || got["migrate"] != "completed" || got["api"] != "" {
		t.Errorf("awaited = %v", got)
	}
}

func TestValidateDependsOn(t *testing.T) {
	for _, deps := range [][]store.Dependency{
		{{Service: "web"}},
		{{Service: "Bad Name"}},
		{{Service: "db"}, {Service: "db"}},
		{{Service: "db", Condition: "service_healthy"}},
	} {
		s := store.Service{Name: "web", Kind: store.ServiceKindApp, Image: "a", Replicas: 1, DependsOn: deps}
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: err = %v", deps, err)
		}
	}
	ok := store.Service{Name: "web", Kind: store.ServiceKindApp, Image: "a", Replicas: 1, DependsOn: []store.Dependency{{Service: "db", Condition: "healthy"}, {Service: "migrate", Condition: "completed"}},
		Runtime: store.Runtime{Restart: "on-failure:3"}}
	if err := validateService(ok); err != nil {
		t.Error(err)
	}
	ok.Runtime.Restart = "always"
	if err := validateService(ok); !errors.Is(err, ErrInvalid) {
		t.Errorf("restart always: %v", err)
	}
	db := store.Service{Name: "db", Kind: store.ServiceKindPostgres, Replicas: 1, DependsOn: []store.Dependency{{Service: "web"}}}
	if err := validateService(db); !errors.Is(err, ErrInvalid) {
		t.Errorf("database depends_on: %v", err)
	}
}

func TestFailedDependency(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "deps", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	deployed := map[string]string{}
	add := func(name string, status store.DeploymentStatus) store.Service {
		svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: name, Kind: store.ServiceKindApp, Image: "a", Replicas: 1})
		if err != nil {
			t.Fatal(err)
		}
		d, err := c.store.CreateDeployment(ctx, store.Deployment{ServiceID: svc.ID, Status: store.DeploymentRunning, Image: "a"})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.store.FinishDeployment(ctx, d.ID, status, ""); err != nil {
			t.Fatal(err)
		}
		deployed[name] = d.ID
		return svc
	}
	add("db", store.DeploymentSucceeded)
	add("migrate", store.DeploymentFailed)
	add("cache", store.DeploymentFailed)
	dep := func(name, cond string) store.Dependency { return store.Dependency{Service: name, Condition: cond} }
	for want, deps := range map[string][]store.Dependency{
		"":        {dep("db", "healthy"), dep("cache", ""), dep("gone", "completed")}, // started only: a failed deploy keeps the old one
		"migrate": {dep("db", "healthy"), dep("migrate", "completed")},
	} {
		if got := c.failedDependency(ctx, store.Service{DependsOn: deps}, deployed); got != want {
			t.Errorf("%v: got %q, want %q", deps, got, want)
		}
	}

	// A skipped service's dependents are skipped in turn.
	web := add("web", store.DeploymentSucceeded)
	d, err := c.skipDeploy(ctx, web, "", "not deployed: migrate failed")
	if err != nil || d.Status != store.DeploymentSkipped {
		t.Fatalf("skip: %+v %v", d, err)
	}
	if got, _ := c.store.GetDeployment(ctx, d.ID); got.Status != store.DeploymentSkipped || got.Error == "" {
		t.Errorf("stored = %+v", got)
	}
	deployed["web"] = d.ID
	if got := c.failedDependency(ctx, store.Service{DependsOn: []store.Dependency{dep("web", "healthy")}}, deployed); got != "web" {
		t.Errorf("after a skipped service: got %q", got)
	}
}
