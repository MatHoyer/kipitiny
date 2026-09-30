package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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

func TestUsersAndSessions(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("users = %d", n)
	}
	u, err := s.CreateUser(ctx, store.User{Username: "admin", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, store.User{Username: "admin", PasswordHash: "y"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate user: %v", err)
	}
	if got, err := s.GetUserByUsername(ctx, "admin"); err != nil || got.ID != u.ID {
		t.Fatalf("by username: %+v %v", got, err)
	}

	future := time.Now().Add(time.Hour)
	if err := s.CreateSession(ctx, store.Session{TokenHash: "live", UserID: u.ID, ExpiresAt: future}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, store.Session{TokenHash: "dead", UserID: u.ID, ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "live"); err != nil {
		t.Fatalf("live session: %v", err)
	}
	if _, err := s.GetSession(ctx, "dead"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session should be not found, got %v", err)
	}
	if err := s.SetPassword(ctx, u.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "live"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("password change must revoke sessions, got %v", err)
	}
	if err := s.CreateSession(ctx, store.Session{TokenHash: "live", UserID: u.ID, ExpiresAt: future}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ctx, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "live"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
}
