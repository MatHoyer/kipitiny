package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProjectsAndServices(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	p, err := s.CreateProject(ctx, store.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(ctx, store.Project{Name: "shop"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate name: got %v, want ErrConflict", err)
	}

	got, err := s.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "shop" || !got.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, p)
	}

	ps, err := s.ListProjects(ctx)
	if err != nil || len(ps) != 1 {
		t.Fatalf("list: %v %v", ps, err)
	}

	svc, err := s.CreateService(ctx, store.Service{
		ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1,
		Port: 80, Domain: "shop.example.com", Env: map[string]string{"A": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateService(ctx, store.Service{
		ProjectID: p.ID, Name: "web2", Kind: store.ServiceKindApp, Image: "nginx", Domain: "shop.example.com",
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate domain: got %v, want ErrConflict", err)
	}
	// Empty domains don't collide.
	for _, name := range []string{"worker1", "worker2"} {
		if _, err := s.CreateService(ctx, store.Service{
			ProjectID: p.ID, Name: name, Kind: store.ServiceKindApp, Image: "busybox",
		}); err != nil {
			t.Fatal(err)
		}
	}

	svc.Env["B"] = "2"
	svc.Replicas = 3
	if _, err := s.UpdateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	got2, err := s.GetService(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Replicas != 3 || got2.Env["A"] != "1" || got2.Env["B"] != "2" || got2.Domain != "shop.example.com" {
		t.Fatalf("service round-trip mismatch: %+v", got2)
	}

	d, err := s.CreateDeployment(ctx, store.Deployment{ServiceID: svc.ID, Status: store.DeploymentRunning, Image: "nginx"})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.FailRunningDeployments(ctx, "interrupted"); err != nil || n != 1 {
		t.Fatalf("fail running: n=%d err=%v", n, err)
	}
	d, err = s.GetDeployment(ctx, d.ID)
	if err != nil || d.Status != store.DeploymentFailed || d.FinishedAt == nil || d.Error != "interrupted" {
		t.Fatalf("deployment after fail: %+v %v", d, err)
	}
	ds, err := s.ListDeployments(ctx, svc.ID, 10)
	if err != nil || len(ds) != 1 {
		t.Fatalf("list deployments: %v %v", ds, err)
	}

	if err := s.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetService(ctx, svc.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("service should cascade-delete, got %v", err)
	}
	if err := s.DeleteProject(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: got %v, want ErrNotFound", err)
	}
}
