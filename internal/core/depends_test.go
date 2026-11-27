package core

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestStartOrder(t *testing.T) {
	app := func(name string, deps ...string) domain.Service {
		s := domain.Service{Name: name, Kind: domain.ServiceKindApp}
		for _, d := range deps {
			s.DependsOn = append(s.DependsOn, domain.Dependency{Service: d})
		}
		return s
	}
	svcs := []domain.Service{app("web", "api", "migrate"), app("api", "db"), app("migrate", "db"),
		{Name: "db", Kind: domain.ServiceKindPostgres}, app("loop-a", "loop-b"), app("loop-b", "loop-a"), app("orphan", "gone")}
	var names []string
	for _, s := range startOrder(svcs) {
		names = append(names, s.Name)
	}
	if want := []string{"db", "api", "migrate", "web", "loop-b", "loop-a", "orphan"}; !slices.Equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}

	svcs[0].DependsOn[1].Condition = "completed"
	svcs[1].DependsOn = append(svcs[1].DependsOn, domain.Dependency{Service: "migrate", Condition: "healthy"})
	got := awaited(svcs)
	if got["db"] != "healthy" || got["migrate"] != "completed" || got["api"] != "" {
		t.Errorf("awaited = %v", got)
	}
}

func TestValidateDependsOn(t *testing.T) {
	for _, deps := range [][]domain.Dependency{
		{{Service: "web"}},
		{{Service: "Bad Name"}},
		{{Service: "db"}, {Service: "db"}},
		{{Service: "db", Condition: "service_healthy"}},
	} {
		s := domain.Service{Name: "web", Kind: domain.ServiceKindApp, Image: "a", Replicas: 1, DependsOn: deps}
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: err = %v", deps, err)
		}
	}
	ok := domain.Service{Name: "web", Kind: domain.ServiceKindApp, Image: "a", Replicas: 1, DependsOn: []domain.Dependency{{Service: "db", Condition: "healthy"}, {Service: "migrate", Condition: "completed"}},
		Runtime: domain.Runtime{Restart: "on-failure:3"}}
	if err := validateService(ok); err != nil {
		t.Error(err)
	}
	ok.Runtime.Restart = "always"
	if err := validateService(ok); !errors.Is(err, ErrInvalid) {
		t.Errorf("restart always: %v", err)
	}
	db := domain.Service{Name: "db", Kind: domain.ServiceKindPostgres, Replicas: 1, DependsOn: []domain.Dependency{{Service: "web"}}}
	if err := validateService(db); !errors.Is(err, ErrInvalid) {
		t.Errorf("database depends_on: %v", err)
	}
}

func TestFailedDependency(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "deps", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	deployed := map[string]string{}
	add := func(name string, status domain.DeploymentStatus) domain.Service {
		svc, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: name, Kind: domain.ServiceKindApp, Image: "a", Replicas: 1})
		if err != nil {
			t.Fatal(err)
		}
		d, err := c.store.CreateDeployment(ctx, domain.Deployment{ServiceID: svc.ID, Status: domain.DeploymentRunning, Image: "a"})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.store.FinishDeployment(ctx, d.ID, status, ""); err != nil {
			t.Fatal(err)
		}
		deployed[name] = d.ID
		return svc
	}
	add("db", domain.DeploymentSucceeded)
	add("migrate", domain.DeploymentFailed)
	add("cache", domain.DeploymentFailed)
	dep := func(name, cond string) domain.Dependency { return domain.Dependency{Service: name, Condition: cond} }
	for want, deps := range map[string][]domain.Dependency{
		"":        {dep("db", "healthy"), dep("cache", ""), dep("gone", "completed")}, // started only: a failed deploy keeps the old one
		"migrate": {dep("db", "healthy"), dep("migrate", "completed")},
	} {
		if got := c.failedDependency(ctx, domain.Service{DependsOn: deps}, deployed); got != want {
			t.Errorf("%v: got %q, want %q", deps, got, want)
		}
	}

	// A skipped service's dependents are skipped in turn.
	web := add("web", domain.DeploymentSucceeded)
	d, err := c.skipDeploy(ctx, web, "", "not deployed: migrate failed")
	if err != nil || d.Status != domain.DeploymentSkipped {
		t.Fatalf("skip: %+v %v", d, err)
	}
	if got, _ := c.store.GetDeployment(ctx, d.ID); got.Status != domain.DeploymentSkipped || got.Error == "" {
		t.Errorf("stored = %+v", got)
	}
	deployed["web"] = d.ID
	if got := c.failedDependency(ctx, domain.Service{DependsOn: []domain.Dependency{dep("web", "healthy")}}, deployed); got != "web" {
		t.Errorf("after a skipped service: got %q", got)
	}
}
