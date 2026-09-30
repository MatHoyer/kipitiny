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
		ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17", Replicas: 1,
	})
	if err != nil {
		t.Fatal(err)
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
